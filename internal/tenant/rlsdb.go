package tenant

/*
The RLS backstop, and why it is a connection wrapper.

The plan was `SET LOCAL app.workspace_id` per request inside a transaction.
The togo kernel does not allow that: the ORM (togo-framework/orm v0.1.0) holds a
concrete *sql.DB — orm.For[T](db *sql.DB, ...) — and every generated model,
REST handler and GraphQL resolver calls it with a.SQLDB. There is no executor
interface to hand it a request-scoped *sql.Tx, and db-postgres v0.1.0 exposes no
pgx acquire/release hooks either (it only blank-imports pgx/stdlib).

So the scope is applied one level lower: a database/sql driver.Connector whose
connections run

    SELECT set_config('app.workspace_id', <workspace on ctx>, false)

before every statement, on the same physical connection, with the context the
statement was issued under. database/sql never shares a connection between two
goroutines at once, so the setting cannot bleed into another request's query;
and because it is re-applied before EVERY statement (not cached), a rolled-back
transaction that reverted it cannot leave a stale value either.

Cost: one extra round trip per statement. Acceptable for P0; revisit when the
ORM grows an executor interface (then this becomes SET LOCAL in a per-request
transaction, as planned).
*/

import (
	"os"
	"strconv"
	"time"

	"context"
	"database/sql"
	"database/sql/driver"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const setScopeSQL = "SELECT set_config('app.workspace_id', $1, false)"

/*
OpenRLS opens the app pool: pgx connections that scope every statement to the
workspace on its context.

The pool is BOUNDED, and the bound is the point. Without one, a burst of
parallel requests asks Postgres for a connection each, and Postgres — shared
here with every other product on the server — answers "sorry, too many clients
already". Every refused request then fails and the work it carried is lost.
That was found in Mahaam (MH-250), where fifty parallel timer starts produced
forty-four time entries; this app had the identical pattern.

A bounded pool makes the next request WAIT for a connection instead of
demanding a new one. Waiting is slower; running out is an outage, and not only
for this app.

DB_MAX_CONNS overrides the default for a deployment with its own Postgres.
*/
func OpenRLS(url string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector{base: stdlib.GetConnector(*cfg)})
	Tune(db)
	return db, nil
}

// DefaultMaxConns is deliberately modest: this app shares one Postgres with
// several others, and its share has to leave room for them.
const DefaultMaxConns = 20

// Tune bounds a pool. Exported so the owner pool gets the same treatment —
// "unbounded" is the wrong answer for both.
func Tune(db *sql.DB) {
	max := DefaultMaxConns
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			max = n
		}
	}
	db.SetMaxOpenConns(max)
	idle := max / 4
	if idle < 2 {
		idle = 2
	}
	db.SetMaxIdleConns(idle)
	db.SetConnMaxLifetime(time.Hour)
	db.SetConnMaxIdleTime(5 * time.Minute)
}

type connector struct{ base driver.Connector }

func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rlsConn{Conn: conn}, nil
}

func (c connector) Driver() driver.Driver { return c.base.Driver() }

type rlsConn struct{ driver.Conn }

var errUnsupported = errors.New("tenant: driver lacks a context-aware method the RLS wrapper needs")

func (c *rlsConn) scope(ctx context.Context) error {
	ex, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return errUnsupported
	}
	_, err := ex.ExecContext(ctx, setScopeSQL, []driver.NamedValue{{Ordinal: 1, Value: WorkspaceID(ctx)}})
	return err
}

func (c *rlsConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, errUnsupported
	}
	if err := c.scope(ctx); err != nil {
		return nil, err
	}
	return qc.QueryContext(ctx, q, args)
}

func (c *rlsConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, errUnsupported
	}
	if err := c.scope(ctx); err != nil {
		return nil, err
	}
	return ec.ExecContext(ctx, q, args)
}

func (c *rlsConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	pc, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return nil, errUnsupported
	}
	st, err := pc.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return &rlsStmt{Stmt: st, conn: c}, nil
}

// Prepare is the legacy, context-free path. database/sql never uses it when
// PrepareContext exists; it is wrapped anyway so no statement escapes scoping.
func (c *rlsConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}

func (c *rlsConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	bt, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errUnsupported
	}
	return bt.BeginTx(ctx, opts)
}

func (c *rlsConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *rlsConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *rlsConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *rlsConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ch, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type rlsStmt struct {
	driver.Stmt
	conn *rlsConn
}

func (s *rlsStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, errUnsupported
	}
	if err := s.conn.scope(ctx); err != nil {
		return nil, err
	}
	return ec.ExecContext(ctx, args)
}

func (s *rlsStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, errUnsupported
	}
	if err := s.conn.scope(ctx); err != nil {
		return nil, err
	}
	return qc.QueryContext(ctx, args)
}

// VerifyAppRole fails when the app pool's role could see through RLS — a
// superuser or BYPASSRLS role makes every policy decorative.
func VerifyAppRole(ctx context.Context, db *sql.DB) error {
	var super, bypass bool
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&name, &super, &bypass)
	if err != nil {
		return err
	}
	if super || bypass {
		return errors.New("tenant: APP_DATABASE_URL role " + name + " is a superuser or has BYPASSRLS — RLS would not apply")
	}
	return nil
}
