package automation

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Connecting a browser by approval (MH-478) — the "OAuth" of the extension.

The owner's words: "the extension is very bad and doesn't have OAuth". Before
this, pairing meant typing the workspace's slug into the extension, reading a
six-digit code off it, and typing that code into the app. This is the
device-authorization flow fadymondy.com uses (FM-89), made multi-tenant:

 1. The extension calls  connect/start  (no credential yet). It gets a CODE, a
    POLL TOKEN and a BROWSER KEY, and opens the site's approval page in a new
    tab: /{locale}/automation/connect/{code}#k={key}.
 2. On that page the signed-in member sees what is asking, PICKS THE WORKSPACE
    from their own memberships, names the browser, lists the sites it may be
    driven on, and presses Approve or Deny.
 3. connect/approve mints the browser's token inside that workspace.
 4. The extension's connect/poll collects it EXACTLY ONCE and connects.

Three secrets, three jobs:

	CODE         names the request. Shown in the popup and in the page address,
	             so a person can check the two match. Knowing it is NOT enough
	             to approve.
	BROWSER KEY  proves the approver is looking at the tab the extension
	             opened. It travels in the URL FRAGMENT, which no browser ever
	             sends to a server, and the page sends it back only with the
	             approval. In a multi-tenant product this matters: without it,
	             anyone who glimpsed a code could approve that browser into
	             THEIR workspace, set its allowed sites, and drive it.
	POLL TOKEN   collects the minted token. Never leaves the extension.

The request has no workspace until it is approved, so it lives in a platform
table (agent_connects) that only the owner pool touches, always through a hash.
The moment it is approved, the browser row is written inside the chosen
workspace on the app pool, under RLS like everything else.
*/

const (
	// Long enough to walk to the other window and sign in; short enough that a
	// code left on a screen stops being useful quickly.
	connectTTL = 10 * time.Minute

	// No O/0, I/1, S/5: the code is read by eye and compared across two screens.
	connectAlphabet = "ABCDEFGHJKLMNPQRTUVWXYZ234679"
	connectCodeLen  = 8

	// Requests per address per hour. pair/start is the one route a stranger can
	// reach, and without a limit anyone could fill the table with requests.
	connectPerHour = 20
)

// Connect request statuses.
const (
	connectPending  = "pending"
	connectApproved = "approved"
	connectClaimed  = "claimed"
	connectDenied   = "denied"
)

// ErrKeyMismatch is an approval that did not come from the tab the extension
// opened.
var ErrKeyMismatch = errors.New("open this page with the extension's Connect button — the link it opens carries a key this approval needs")

// newConnectCode returns "K7QF-3MTX": eight characters in two groups.
func newConnectCode() string {
	out := make([]byte, 0, connectCodeLen+1)
	for i := 0; i < connectCodeLen; i++ {
		if i == connectCodeLen/2 {
			out = append(out, '-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(connectAlphabet))))
		if err != nil {
			panic("automation: no entropy for a connect code: " + err.Error())
		}
		out = append(out, connectAlphabet[n.Int64()])
	}
	return string(out)
}

// NormaliseConnectCode makes what was typed or pasted comparable with what was
// minted: case and the grouping dash are presentation, not identity.
func NormaliseConnectCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

// connectLimiter damps abuse of the one unauthenticated endpoint. In memory and
// per process — abuse damping, not a quota anyone pays for.
type connectLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *connectLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Hour)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= connectPerHour {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	// A key whose window empties is dropped, so a long-lived process does not
	// hold an entry for every address that ever called.
	for k, v := range l.hits {
		if len(v) == 0 || v[len(v)-1].Before(cutoff) {
			delete(l.hits, k)
		}
	}
	return true
}

// ConnectWorkspace is one workspace the signed-in member could connect into.
type ConnectWorkspace struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
	// Connecting a browser hands a program a token that drives a signed-in
	// browser, so it is an admin action (automation:manage), exactly as
	// pairing was.
	CanApprove bool `json:"can_approve"`
}

// ConnectView is what the approval page shows. Everything about the browser
// came from an unauthenticated caller and is displayed as a claim.
type ConnectView struct {
	Code             string             `json:"code"`
	RequestedName    string             `json:"requested_name"`
	Platform         string             `json:"platform"`
	BrowserVersion   string             `json:"browser_version"`
	ExtensionVersion string             `json:"extension_version"`
	IP               string             `json:"ip"`
	Status           string             `json:"status" doc:"pending | approved | claimed | denied | expired"`
	ExpiresAt        time.Time          `json:"expires_at"`
	// Whether the page holds this request's browser key. Without it the page
	// can only say "open this from the extension".
	KeyOK      bool               `json:"key_ok"`
	Workspaces       []ConnectWorkspace `json:"workspaces" doc:"The caller's own workspaces, and whether they may connect a browser to each."`
	// Only for the member who decided it: which workspace it went to.
	Workspace *ConnectWorkspace `json:"workspace,omitempty"`
}

type connectRow struct {
	ID, CodeHash, KeyHash, Fingerprint, Platform, BrowserVersion, ExtensionVersion string
	RequestedName, IP, Status, AgentID, DecidedBy, TokenOnce                      string
	WorkspaceID                                                                  sql.NullString
	ExpiresAt                                                                    time.Time
}

const connectColumns = `id, code_hash, key_hash, fingerprint, platform, browser_version, extension_version,
	requested_name, ip, status, agent_id, decided_by, token_once, workspace_id, expires_at`

func scanConnect(r rowScanner) (connectRow, error) {
	var c connectRow
	err := r.Scan(&c.ID, &c.CodeHash, &c.KeyHash, &c.Fingerprint, &c.Platform, &c.BrowserVersion, &c.ExtensionVersion,
		&c.RequestedName, &c.IP, &c.Status, &c.AgentID, &c.DecidedBy, &c.TokenOnce, &c.WorkspaceID, &c.ExpiresAt)
	return c, err
}

// findConnect resolves a code to its request, on the owner pool: the request
// belongs to no workspace yet, and the hash is the predicate.
func findConnect(ctx context.Context, sys *sql.DB, code string) (connectRow, error) {
	norm := NormaliseConnectCode(code)
	if len(norm) != connectCodeLen {
		return connectRow{}, sql.ErrNoRows
	}
	return scanConnect(sys.QueryRowContext(ctx, `SELECT `+connectColumns+` FROM agent_connects WHERE code_hash = $1`, hash(norm)))
}

func (c connectRow) expired(now time.Time) bool {
	return c.Status == connectPending && !now.Before(c.ExpiresAt)
}

func (c connectRow) keyMatches(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash(key)), []byte(c.KeyHash)) == 1
}

// memberWorkspaces lists the caller's active memberships. Cross-tenant by
// definition, so it runs on the owner pool with the user as the predicate —
// the same query /api/me/workspaces answers.
func memberWorkspaces(ctx context.Context, sys *sql.DB, userID string) ([]ConnectWorkspace, error) {
	rows, err := sys.QueryContext(ctx, `
		SELECT w.id, w.slug, w.name, m.role
		FROM workspaces w JOIN workspace_members m ON m.workspace_id = w.id
		WHERE m.user_id = $1 AND m.status = 'active'
		ORDER BY w.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConnectWorkspace{}
	for rows.Next() {
		var w ConnectWorkspace
		if err := rows.Scan(&w.ID, &w.Slug, &w.Name, &w.Role); err != nil {
			return nil, err
		}
		w.CanApprove = authz.Can(w.Role, authz.AutomationManage)
		out = append(out, w)
	}
	return out, rows.Err()
}

func signedIn(ctx context.Context) (string, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id == nil || id.ID == "" {
		return "", huma.Error401Unauthorized("sign in to connect a browser")
	}
	return id.ID, nil
}

// RegisterConnect mounts the connect-by-approval flow. start and poll are
// public (the extension has no credential yet); the rest need a session and
// are checked against the chosen workspace's membership and role here.
func RegisterConnect(api huma.API, a *app.App, hub *Hub) {
	limiter := &connectLimiter{hits: map[string][]time.Time{}}

	type startOut struct {
		Code       string    `json:"code" doc:"Shown in the popup and on the approval page, so a person can check they match."`
		PollToken  string    `json:"poll_token" doc:"Keep it. It collects the browser's token; never send it anywhere else."`
		BrowserKey string    `json:"browser_key" doc:"Goes in the approval page's URL fragment only."`
		VerifyPath string    `json:"verify_path" doc:"Open siteOrigin + this in a new tab."`
		ExpiresAt  time.Time `json:"expires_at"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "connect-start", Method: http.MethodPost, Path: "/api/agents/connect/start",
		DefaultStatus: http.StatusCreated,
		Summary:       "Ask to connect this browser (public: creates a request a member must approve on the site)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Name             string `json:"name,omitempty" maxLength:"80" doc:"A suggestion. Whoever approves can change it."`
			Fingerprint      string `json:"fingerprint,omitempty" maxLength:"120"`
			Platform         string `json:"platform,omitempty" maxLength:"80"`
			BrowserVersion   string `json:"browser_version,omitempty" maxLength:"60"`
			ExtensionVersion string `json:"extension_version,omitempty" maxLength:"40"`
			Locale           string `json:"locale,omitempty" enum:",en,ar" doc:"The approval page's language."`
		}
	}) (*body[startOut], error) {
		ip := clientip.From(ctx)
		if !limiter.allow(clientip.Bucket(ip), time.Now()) {
			return nil, huma.Error429TooManyRequests("too many connect requests from this address; try again in a while")
		}
		// Expired requests are useless and the table is only read by hash, so a
		// day's grace is plenty for anyone asking what happened.
		_, _ = a.SysDB.ExecContext(ctx, `DELETE FROM agent_connects WHERE expires_at < now() - interval '1 day'`)

		code := newConnectCode()
		poll := newSecret("moh_poll_")
		key := newSecret("")
		expires := time.Now().UTC().Add(connectTTL).Truncate(time.Second)
		if _, err := a.SysDB.ExecContext(ctx, `
			INSERT INTO agent_connects (code_hash, poll_hash, key_hash, fingerprint, platform, browser_version,
			                            extension_version, requested_name, ip, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			hash(NormaliseConnectCode(code)), hash(poll), hash(key),
			trim(strings.TrimSpace(in.Body.Fingerprint), 120), trim(strings.TrimSpace(in.Body.Platform), 80),
			trim(strings.TrimSpace(in.Body.BrowserVersion), 60), trim(strings.TrimSpace(in.Body.ExtensionVersion), 40),
			trim(strings.TrimSpace(in.Body.Name), 80), trim(ip, 60), expires); err != nil {
			return nil, huma.Error500InternalServerError("could not start connecting")
		}
		locale := in.Body.Locale
		if locale == "" {
			locale = "en"
		}
		return out(startOut{
			Code: code, PollToken: poll, BrowserKey: key, ExpiresAt: expires,
			VerifyPath: "/" + locale + "/automation/connect/" + code + "#k=" + key,
		}), nil
	})

	type pollOut struct {
		Status        string `json:"status" doc:"pending | approved | claimed | denied | expired"`
		Token         string `json:"token,omitempty" doc:"Present exactly once, on the first poll after approval."`
		AgentID       string `json:"agent_id,omitempty"`
		Name          string `json:"name,omitempty"`
		WorkspaceName string `json:"workspace_name,omitempty"`
		WorkspaceSlug string `json:"workspace_slug,omitempty"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "connect-poll", Method: http.MethodPost, Path: "/api/agents/connect/poll",
		Summary: "Collect the browser's token once a member has approved (public: the poll token is the authorization)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			PollToken string `json:"poll_token" minLength:"16" maxLength:"120"`
		}
	}) (*body[pollOut], error) {
		var id, status, agentID, token string
		var wsID sql.NullString
		var expires time.Time
		err := a.SysDB.QueryRowContext(ctx, `
			SELECT id, status, agent_id, token_once, workspace_id, expires_at FROM agent_connects WHERE poll_hash = $1`,
			hash(strings.TrimSpace(in.Body.PollToken))).Scan(&id, &status, &agentID, &token, &wsID, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			// The poll token is long and random: an unknown one is the caller's
			// mistake, not something to hide.
			return nil, huma.Error404NotFound("no such connect request")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the connect request")
		}
		switch {
		case status == connectApproved && token != "":
			// Handed over once, then blanked, in one statement: two polls racing
			// cannot both collect it.
			var got string
			err := a.SysDB.QueryRowContext(ctx, `
				UPDATE agent_connects SET status = 'claimed', token_once = '', updated_at = now()
				WHERE id = $1 AND status = 'approved' AND token_once <> ''
				RETURNING $2::text`, id, token).Scan(&got)
			if errors.Is(err, sql.ErrNoRows) {
				return out(pollOut{Status: connectClaimed}), nil
			}
			if err != nil {
				return nil, huma.Error500InternalServerError("could not complete connecting")
			}
			o := pollOut{Status: connectApproved, Token: got, AgentID: agentID}
			if wsID.Valid {
				_ = a.SysDB.QueryRowContext(ctx, `SELECT name, slug FROM workspaces WHERE id = $1`, wsID.String).
					Scan(&o.WorkspaceName, &o.WorkspaceSlug)
				_ = a.SysDB.QueryRowContext(ctx, `SELECT name FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
					wsID.String, agentID).Scan(&o.Name)
			}
			return out(o), nil
		case status == connectApproved:
			// Approved a moment ago and the token is still being minted.
			return out(pollOut{Status: connectPending}), nil
		case status == connectPending && !time.Now().Before(expires):
			return out(pollOut{Status: "expired"}), nil
		default:
			return out(pollOut{Status: status}), nil
		}
	})

	huma.Register(api, huma.Operation{
		OperationID: "connect-get", Method: http.MethodGet, Path: "/api/agents/connect/{code}",
		Summary: "What is asking to connect, and the workspaces the signed-in member could connect it to", Tags: tags,
	}, func(ctx context.Context, in *struct {
		// No maxLength: huma's validation echoes the path back in its error,
		// and the isolation sweep rightly counts that as handing another
		// workspace's id back to the caller. findConnect refuses anything that
		// is not a code.
		Code string `path:"code"`
		// The browser key from the page's URL fragment. Without it the page is
		// told the request exists and nothing about the browser: its claimed
		// name, platform and address are for whoever holds the tab the
		// extension opened.
		Key string `header:"X-Connect-Key"`
	}) (*body[ConnectView], error) {
		userID, err := signedIn(ctx)
		if err != nil {
			return nil, err
		}
		c, err := findConnect(ctx, a.SysDB, in.Code)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("no such connect request — press Connect in the extension again")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the connect request")
		}
		wss, err := memberWorkspaces(ctx, a.SysDB, userID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read your workspaces")
		}
		v := ConnectView{Code: formatConnectCode(in.Code), Status: c.Status, ExpiresAt: c.ExpiresAt, Workspaces: wss}
		if c.keyMatches(in.Key) {
			v.KeyOK = true
			v.RequestedName, v.Platform, v.IP = c.RequestedName, c.Platform, c.IP
			v.BrowserVersion, v.ExtensionVersion = c.BrowserVersion, c.ExtensionVersion
		}
		if c.expired(time.Now()) {
			v.Status = "expired"
		}
		// Which workspace it went to is told only to whoever decided it: the
		// code alone must not reveal another tenant's name.
		if c.WorkspaceID.Valid && c.DecidedBy == userID {
			for i := range wss {
				if wss[i].ID == c.WorkspaceID.String {
					v.Workspace = &wss[i]
				}
			}
		}
		return out(v), nil
	})

	type approveOut struct {
		Browser   AgentOut         `json:"browser"`
		Workspace ConnectWorkspace `json:"workspace"`
		Replaced  bool             `json:"replaced" doc:"This browser was already connected to the workspace; its old token stopped working."`
	}
	huma.Register(api, huma.Operation{
		OperationID: "connect-approve", Method: http.MethodPost, Path: "/api/agents/connect/approve",
		Summary: "Approve a browser into one of your workspaces, and mint its token", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Code         string   `json:"code" minLength:"8" maxLength:"20"`
			Key          string   `json:"key" maxLength:"120" doc:"The browser key from the page's URL fragment."`
			Workspace    string   `json:"workspace" minLength:"1" maxLength:"80" doc:"Workspace id or slug."`
			Name         string   `json:"name,omitempty" maxLength:"80"`
			AllowedHosts []string `json:"allowed_hosts,omitempty" maxItems:"100" doc:"Where it may be driven. Empty allows nothing."`
		}
	}) (*body[approveOut], error) {
		userID, err := signedIn(ctx)
		if err != nil {
			return nil, err
		}
		c, err := findConnect(ctx, a.SysDB, in.Body.Code)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("no such connect request — press Connect in the extension again")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the connect request")
		}
		if !c.keyMatches(in.Body.Key) {
			return nil, huma.Error403Forbidden(ErrKeyMismatch.Error())
		}
		if c.expired(time.Now()) {
			return nil, huma.Error410Gone("this request expired — press Connect in the extension again")
		}
		if c.Status != connectPending {
			return nil, huma.Error409Conflict("this request was already " + c.Status)
		}

		// Membership and role in the CHOSEN workspace, checked here: this route
		// has no X-Workspace-ID, because the whole point is that the member
		// picks one.
		s, err := tenant.Resolve(ctx, a.SysDB, userID, in.Body.Workspace)
		if errors.Is(err, tenant.ErrNotMember) {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if errors.Is(err, tenant.ErrSuspended) || errors.Is(err, tenant.ErrAccountSuspended) {
			return nil, huma.Error403Forbidden(err.Error())
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not resolve the workspace")
		}
		if !authz.Can(s.Role, authz.AutomationManage) {
			return nil, huma.Error403Forbidden("connecting a browser needs the admin role in " + s.WorkspaceName)
		}

		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			name = strings.TrimSpace(c.RequestedName)
		}
		if name == "" {
			name = "Browser"
		}
		name = trim(name, 80)
		hosts := cleanHosts(in.Body.AllowedHosts)

		// Reserve the request first, conditionally: two members approving the
		// same request at once cannot both mint a browser for it.
		res, err := a.SysDB.ExecContext(ctx, `
			UPDATE agent_connects SET status = 'approved', decided_by = $2, decided_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'pending' AND expires_at > now()`, c.ID, userID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not approve that browser")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error409Conflict("this request was just decided by someone else, or it expired")
		}
		release := func() {
			_, _ = a.SysDB.ExecContext(context.WithoutCancel(ctx), `
				UPDATE agent_connects SET status = 'pending', decided_by = '', decided_at = NULL, updated_at = now()
				WHERE id = $1 AND status = 'approved' AND token_once = ''`, c.ID)
		}

		agent, replaced, token, err := mintConnected(ctx, a, s.WorkspaceID, userID, name, hosts, c)
		if err != nil {
			release()
			if errors.Is(err, errNameTaken) {
				return nil, huma.Error409Conflict("a browser called \"" + name + "\" is already connected to " + s.WorkspaceName + " — give this one another name")
			}
			return nil, huma.Error500InternalServerError("could not connect that browser")
		}
		if replaced != "" && hub != nil {
			// The old token no longer exists; its socket must not outlive it.
			hub.Disconnect(replaced, 4003, "this browser was connected again")
		}

		// The token waits here for exactly one collection by the extension that
		// started this request (the poll route blanks it).
		if _, err := a.SysDB.ExecContext(ctx, `
			UPDATE agent_connects SET workspace_id = $2, agent_id = $3, token_once = $4, updated_at = now()
			WHERE id = $1 AND status = 'approved'`, c.ID, s.WorkspaceID, agent.ID, token); err != nil {
			return nil, huma.Error500InternalServerError("could not complete connecting")
		}
		if a.Events != nil {
			a.Events.Publish(s.WorkspaceID, realtime.Event{Kind: "browser", Permission: authz.AutomationRead})
		}
		a.Log.Info("browser connected by approval", "workspace", s.WorkspaceID, "agent", agent.ID, "by", userID, "replaced", replaced != "")

		return out(approveOut{
			Browser:   agent,
			Workspace: ConnectWorkspace{ID: s.WorkspaceID, Slug: s.WorkspaceSlug, Name: s.WorkspaceName, Role: string(s.Role), CanApprove: true},
			Replaced:  replaced != "",
		}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "connect-deny", Method: http.MethodPost, Path: "/api/agents/connect/deny",
		Summary: "Refuse a browser's request to connect", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Code string `json:"code" minLength:"8" maxLength:"20"`
			Key  string `json:"key" maxLength:"120"`
		}
	}) (*body[struct {
		Status string `json:"status"`
	}], error) {
		userID, err := signedIn(ctx)
		if err != nil {
			return nil, err
		}
		c, err := findConnect(ctx, a.SysDB, in.Body.Code)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("no such connect request")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the connect request")
		}
		// The key here too: a stranger who saw the code must not be able to
		// refuse someone else's browser either.
		if !c.keyMatches(in.Body.Key) {
			return nil, huma.Error403Forbidden(ErrKeyMismatch.Error())
		}
		res, err := a.SysDB.ExecContext(ctx, `
			UPDATE agent_connects SET status = 'denied', decided_by = $2, decided_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'pending'`, c.ID, userID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not refuse that browser")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error409Conflict("this request was already decided")
		}
		return out(struct {
			Status string `json:"status"`
		}{connectDenied}), nil
	})
}

var errNameTaken = errors.New("a browser with that name already exists")

// mintConnected writes the approved browser inside its workspace and returns
// it with its new token. A browser that is already connected to this
// workspace — same fingerprint, not revoked — is RE-KEYED rather than added a
// second time: a reinstall is the same browser, and its runs, schedules and
// allowed sites stay with it. `replaced` is the id whose old token stopped
// working.
func mintConnected(ctx context.Context, a *app.App, workspaceID, userID, name string, hosts []string, c connectRow) (agent AgentOut, replaced, token string, err error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	token, prefix := NewToken()

	var existing string
	if c.Fingerprint != "" {
		_ = a.SQLDB.QueryRowContext(sctx, `
			SELECT id FROM browser_agents
			WHERE workspace_id = $1 AND fingerprint = $2 AND revoked_at IS NULL
			ORDER BY created_at DESC LIMIT 1`, workspaceID, c.Fingerprint).Scan(&existing)
	}

	var id string
	if existing != "" {
		err = a.SQLDB.QueryRowContext(sctx, `
			UPDATE browser_agents SET name = $3, allowed_hosts = $4::text[], token_hash = $5, token_prefix = $6,
			    platform = COALESCE(NULLIF($7, ''), platform), browser_version = COALESCE(NULLIF($8, ''), browser_version),
			    extension_version = COALESCE(NULLIF($9, ''), extension_version), status = 'offline', updated_at = now()
			WHERE workspace_id = $1 AND id = $2 RETURNING id`,
			workspaceID, existing, name, pgtext.Literal(hosts), HashToken(token), prefix,
			c.Platform, c.BrowserVersion, c.ExtensionVersion).Scan(&id)
		replaced = existing
	} else {
		err = a.SQLDB.QueryRowContext(sctx, `
			INSERT INTO browser_agents (workspace_id, name, fingerprint, platform, browser_version, extension_version,
			                            token_hash, token_prefix, allowed_hosts, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::text[], $10) RETURNING id`,
			workspaceID, name, c.Fingerprint, c.Platform, c.BrowserVersion, c.ExtensionVersion,
			HashToken(token), prefix, pgtext.Literal(hosts), userID).Scan(&id)
	}
	if pgCode(err) == "23505" {
		return AgentOut{}, "", "", errNameTaken
	}
	if err != nil {
		return AgentOut{}, "", "", err
	}
	now := time.Now().UTC()
	return AgentOut{
		ID: id, Name: name, Platform: c.Platform, BrowserVersion: c.BrowserVersion, ExtensionVersion: c.ExtensionVersion,
		Status: "offline", AllowedHosts: hosts, TokenPrefix: prefix, LastSeenAt: now, CreatedAt: now,
	}, replaced, token, nil
}

// formatConnectCode shows a code grouped, however it was typed.
func formatConnectCode(code string) string {
	n := NormaliseConnectCode(code)
	if len(n) != connectCodeLen {
		return n
	}
	return n[:connectCodeLen/2] + "-" + n[connectCodeLen/2:]
}
