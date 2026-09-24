package tenant

/*
Who is writing — the audit log's half of the context (MH-392).

The audit log is written below the handlers, by a trigger on every audited
table (internal/db/post/96_audit_log.sql). The trigger learns who is acting
from the `app.audit` setting, which the RLS connection wrapper (rlsdb.go) sets
in the SAME round trip as app.workspace_id, before every statement. So the
actor cannot be forgotten by a handler, and cannot disagree with the tenant
scope the statement runs under.

The actor is, in order:

 1. an explicit one (WithActor) — the platform admin MCP, a browser agent, the
    scheduler: callers that are not a member's request;
 2. the tenant Scope on the context — a member in the browser (Via
    "session"), an MCP token ("mcp") or an OAuth client ("oauth");
 3. the signed-in identity, for account-level routes that act before any
    workspace scope exists (creating a workspace, accepting an invitation).

The system's own work (Via "system": collectors, sweepers, webhooks) has no
actor, the setting is empty, and the trigger records nothing.
*/

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"

	"github.com/togo-framework/auth"
)

// Actor is who a write is recorded against.
type Actor struct {
	// member | token | oauth | agent | admin
	Kind string
	// users.id, mcp_tokens.id, oauth_grants.id, browser_agents.id …
	ID string
	// The email, token name, client name or browser name, as of now.
	Name string
	// The member a token or client acts for.
	UserID string
	// web | mcp | graphql | agent | admin
	Via string
}

// Request is the HTTP request a write happened in: set once by the audit
// middleware, the operation filled in by the REST or MCP layer once known.
type Request struct {
	ID        string
	IP        string
	UserAgent string
	Method    string
	Path      string

	mu        sync.Mutex
	operation string
}

// SetOperation names what the request is doing ("update-contact",
// "mcp:create_deal", "graphql").
func (r *Request) SetOperation(op string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.operation = op
	r.mu.Unlock()
}

// Operation is what SetOperation last named.
func (r *Request) Operation() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.operation
}

type requestKey struct{}
type actorKey struct{}

// WithRequest puts the request's audit details on ctx.
func WithRequest(ctx context.Context, r *Request) context.Context {
	return context.WithValue(ctx, requestKey{}, r)
}

// RequestFrom returns the request on ctx, or nil.
func RequestFrom(ctx context.Context) *Request {
	r, _ := ctx.Value(requestKey{}).(*Request)
	return r
}

// WithActor names who is acting explicitly, overriding the tenant scope.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom resolves who is acting on ctx; ok=false for the system's own work.
func ActorFrom(ctx context.Context) (Actor, bool) {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok && a.Kind != "" {
		return a, true
	}
	if s, ok := From(ctx); ok {
		switch s.Via {
		case "session":
			via := "web"
			if r := RequestFrom(ctx); r != nil && r.Path == "/graphql" {
				via = "graphql"
			}
			return Actor{Kind: "member", ID: s.UserID, Name: s.Email, UserID: s.UserID, Via: via}, true
		case "mcp":
			return Actor{Kind: "token", ID: s.TokenID, Name: s.TokenName, UserID: s.UserID, Via: "mcp"}, true
		case "oauth":
			return Actor{Kind: "oauth", ID: s.TokenID, Name: s.TokenName, UserID: s.UserID, Via: "mcp"}, true
		}
		return Actor{}, false
	}
	if id, ok := auth.IdentityFrom(ctx); ok && id != nil && id.ID != "" {
		return Actor{Kind: "member", ID: id.ID, Name: id.Email, UserID: id.ID, Via: "web"}, true
	}
	return Actor{}, false
}

// AuditJSON is the value of the app.audit setting for a statement run under
// ctx: "" when nobody in particular is acting.
func AuditJSON(ctx context.Context) string {
	a, ok := ActorFrom(ctx)
	if !ok {
		return ""
	}
	m := map[string]string{"k": a.Kind, "id": a.ID, "n": a.Name, "u": a.UserID, "v": a.Via}
	if r := RequestFrom(ctx); r != nil {
		m["rid"], m["ip"], m["ua"], m["op"] = r.ID, r.IP, clip(r.UserAgent, 300), r.Operation()
	}
	// The automation rules whose actions are running (MH-427): the database
	// triggers that raise rule events read it, so an event a rule caused
	// carries the rule — the loop guard's input (126_automation_rule_triggers.sql).
	if chain := RuleChain(ctx); len(chain) > 0 {
		m["rc"] = strings.Join(chain, ">")
	}
	b, _ := json.Marshal(m)
	return string(b)
}

type ruleChainKey struct{}

// WithRuleChain marks ctx as carrying out the actions of the last rule in
// chain, which the earlier ones caused (internal/triggers.WithRule).
func WithRuleChain(ctx context.Context, chain []string) context.Context {
	return context.WithValue(ctx, ruleChainKey{}, append([]string(nil), chain...))
}

// RuleChain is the chain of automation rules acting on ctx, oldest first.
func RuleChain(ctx context.Context) []string {
	c, _ := ctx.Value(ruleChainKey{}).([]string)
	return c
}

// AuditTx makes the rest of tx record against ctx's actor. For writes on the
// OWNER pool, which is not the RLS wrapper and so sets nothing by itself:
// SET LOCAL semantics, so the setting ends with the transaction.
func AuditTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('app.audit', $1, true)`, AuditJSON(ctx))
	return err
}

// SysExec runs one write on the owner pool with the audit context applied —
// for the handful of writes that must bypass RLS (platform admin tools,
// membership) and still be recorded.
func SysExec(ctx context.Context, sys *sql.DB, query string, args ...any) (sql.Result, error) {
	tx, err := sys.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := AuditTx(ctx, tx); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return res, tx.Commit()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
