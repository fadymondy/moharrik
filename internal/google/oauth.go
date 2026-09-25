package google

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/pgtext"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var httpClient = &http.Client{Timeout: httpTimeout}

func ready(a *app.App) error {
	if a == nil || a.SQLDB == nil || a.SysDB == nil {
		return errors.New("google: no database")
	}
	return nil
}

func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Challenge is the S256 PKCE challenge of a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func pageLocale(l string) string {
	if l == "ar" {
		return "ar"
	}
	return "en"
}

// integrationsPage is where the browser lands after a connect. Built from the
// workspace's own slug, never from the request.
func integrationsPage(locale, slug string) string {
	return "/" + pageLocale(locale) + "/w/" + url.PathEscape(slug) + "/settings/integrations"
}

// ---- start ------------------------------------------------------------------

// Started is a connect attempt: send the browser to URL, with State in the cookie.
type Started struct {
	URL    string
	State  string
	Scopes []string
}

// StartConnect records a connect attempt by the caller in s's workspace and
// builds Google's consent URL. The caller has already been checked for
// integrations:manage.
//
// reconnect, when set, is a connection in this workspace being repaired: the
// attempt asks again for every product scope that connection had (a grant
// Google dropped is gone in full, so asking for one product would store only
// that one) and pre-selects its account.
func StartConnect(ctx context.Context, a *app.App, s *tenant.Scope, product Product, monetary bool, locale, reconnect string) (*Started, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	cfg := LoadConfig()
	if !cfg.Ready() {
		return nil, ErrNotConfigured
	}
	if s == nil || s.UserID == "" || s.WorkspaceID == "" {
		return nil, errors.New("google: no signed-in user in a workspace")
	}
	state, err := randToken()
	if err != nil {
		return nil, err
	}
	verifier, err := randToken()
	if err != nil {
		return nil, err
	}
	sealed, err := verifierBox.Seal(verifier)
	if err != nil {
		return nil, err
	}
	// monetary is overloaded per product: YouTube revenue reporting, or
	// (MH-585) Contacts' "Other contacts" scope. Any other product ignores it.
	need := product.Scopes(monetary && (product == YouTube || product == Contacts))
	now := time.Now().UTC()
	wctx := tenant.WithWorkspace(ctx, s.WorkspaceID)
	hint := ""
	if reconnect != "" {
		var had pgtext.Array
		err := a.SQLDB.QueryRowContext(wctx, `
			SELECT email, granted_scopes FROM google_connections WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, reconnect).Scan(&hint, &had)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotConnected
		}
		if err != nil {
			return nil, err
		}
		for _, sc := range had {
			if productScopes[sc] {
				need = append(need, sc)
			}
		}
		need = sortedUnique(need)
	}
	if _, err := a.SQLDB.ExecContext(wctx,
		`DELETE FROM google_oauth_states WHERE workspace_id = $1 AND expires_at < $2`, s.WorkspaceID, now); err != nil {
		return nil, err
	}
	if _, err := a.SQLDB.ExecContext(wctx, `
		INSERT INTO google_oauth_states (state_hash, verifier_sealed, workspace_id, user_id, product, scopes, return_to, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		hashOf(state), sealed, s.WorkspaceID, s.UserID, string(product), pgtext.Literal(need),
		integrationsPage(locale, s.WorkspaceSlug), now, now.Add(stateTTL)); err != nil {
		return nil, err
	}
	q := url.Values{
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {cfg.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {strings.Join(append([]string{ScopeOpenID, ScopeEmail}, need...), " ")},
		"state":                 {state},
		"code_challenge":        {Challenge(verifier)},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		// Incremental: what this account already granted this client stays
		// granted, so connecting YouTube does not drop Analytics.
		"include_granted_scopes": {"true"},
		// consent: a refresh token only comes with a fresh consent.
		// select_account: the right Google account is often not the one
		// signed in to the browser.
		"prompt": {"consent select_account"},
	}
	if hint != "" {
		q.Set("login_hint", hint)
	}
	return &Started{URL: current().AuthURL + "?" + q.Encode(), State: state, Scopes: need}, nil
}

// ---- callback ---------------------------------------------------------------

// Callback results, carried to the page as ?google=<result>.
const (
	ResultConnected    = "connected"
	ResultCancelled    = "cancelled"
	ResultAccessDenied = "access_denied"
	ResultState        = "state"
	ResultForbidden    = "forbidden"
	ResultFailed       = "failed"
	ResultNoRefresh    = "no_refresh"
	ResultScope        = "scope"
	ResultNoIdentity   = "no_identity"
	ResultNotReady     = "not_configured"
)

// CallbackInput is what the callback request carried.
type CallbackInput struct {
	UserID      string // the session's user
	CookieState string // the state cookie
	State       string // ?state=
	Code        string // ?code=
	Error       string // ?error=
}

// CallbackOutcome is where the browser goes, and why.
type CallbackOutcome struct {
	Result   string
	Product  string
	ReturnTo string // a local path
}

// Location is the redirect target: the page with ?google=result.
func (o CallbackOutcome) Location() string {
	q := url.Values{"google": {o.Result}}
	if o.Product != "" {
		q.Set("product", o.Product)
	}
	return o.ReturnTo + "?" + q.Encode()
}

type stateRow struct {
	verifierSealed, workspaceID, userID, product, returnTo string
	scopes                                                 []string
	expires                                                time.Time
}

// Callback finishes a connect. It never returns or logs a token.
func Callback(ctx context.Context, a *app.App, in CallbackInput) CallbackOutcome {
	out := CallbackOutcome{Result: ResultState, ReturnTo: "/en/workspaces"}
	if err := ready(a); err != nil {
		out.Result = ResultFailed
		return out
	}

	// Single use: the row is consumed before anything else is decided, so a
	// replay finds nothing whatever happened the first time. On the OWNER pool:
	// no workspace is known until this row names one.
	var st stateRow
	if in.State != "" {
		err := a.SysDB.QueryRowContext(ctx, `
			DELETE FROM google_oauth_states WHERE state_hash = $1
			RETURNING verifier_sealed, workspace_id, user_id, product, scopes, return_to, expires_at`,
			hashOf(in.State)).Scan(&st.verifierSealed, &st.workspaceID, &st.userID, &st.product,
			(*pgtext.Array)(&st.scopes), &st.returnTo, &st.expires)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			logWarn(a, "google connect: could not read the attempt", err)
			out.Result = ResultFailed
			return out
		}
	}
	if st.workspaceID == "" {
		return out // unknown or already used
	}
	if strings.HasPrefix(st.returnTo, "/") && !strings.HasPrefix(st.returnTo, "//") {
		out.ReturnTo = st.returnTo
	}
	out.Product = st.product

	// The other two bindings: the cookie this browser was given, and the user.
	if in.CookieState == "" || subtle.ConstantTimeCompare([]byte(in.State), []byte(in.CookieState)) != 1 ||
		time.Now().After(st.expires) || in.UserID == "" ||
		subtle.ConstantTimeCompare([]byte(st.userID), []byte(in.UserID)) != 1 {
		return out
	}
	// Still a member, still allowed to manage integrations there.
	scope, err := tenant.Resolve(ctx, a.SysDB, in.UserID, st.workspaceID)
	if err != nil || !authz.Can(scope.Role, authz.IntegrationsManage) {
		out.Result = ResultForbidden
		return out
	}
	out.ReturnTo = integrationsPage(localeOf(st.returnTo), scope.WorkspaceSlug)

	cfg := LoadConfig()
	if !cfg.Ready() {
		out.Result = ResultNotReady
		return out
	}
	if in.Error != "" {
		if in.Error == "access_denied" {
			out.Result = ResultAccessDenied
		} else {
			out.Result = ResultCancelled
		}
		return out
	}
	if in.Code == "" {
		out.Result = ResultFailed
		return out
	}
	verifier, err := verifierBox.Open(st.verifierSealed)
	if err != nil {
		logWarn(a, "google connect: the stored attempt is unreadable", err)
		out.Result = ResultFailed
		return out
	}

	tok, err := exchangeCode(ctx, cfg, in.Code, verifier)
	if err != nil {
		logWarn(a, "google connect: code exchange failed", err)
		out.Result = ResultFailed
		return out
	}
	// Anything held from here on is a live grant: every refusal below gives
	// it back to Google.
	grant := tok.RefreshToken
	if grant == "" {
		grant = tok.AccessToken
	}

	sub, email, err := identify(ctx, cfg, tok)
	if err != nil {
		logWarn(a, "google connect: no account identity", err)
		giveBack(ctx, a, "", grant)
		out.Result = ResultNoIdentity
		return out
	}
	if tok.RefreshToken == "" {
		giveBack(ctx, a, sub, grant)
		out.Result = ResultNoRefresh
		return out
	}
	granted := sortedUnique(strings.Fields(tok.Scope))
	if !hasAll(granted, st.scopes...) {
		// Google's granular consent lets a box be unticked.
		giveBack(ctx, a, sub, grant)
		out.Result = ResultScope
		return out
	}
	id, err := saveConnection(ctx, a, st.workspaceID, sub, email, tok.RefreshToken, granted, in.UserID)
	if err != nil {
		logWarn(a, "google connect: the grant was not stored", err)
		giveBack(ctx, a, sub, grant)
		out.Result = ResultFailed
		return out
	}
	cacheAccess(id, tok.RefreshToken, tok.AccessToken, tok.ExpiresIn)
	// A reconnect repairs whatever was waiting on it: its resources are due
	// now, and discovery runs at once so a new account's data appears
	// without another step (MH-440).
	_, _ = a.SQLDB.ExecContext(tenant.WithWorkspace(ctx, st.workspaceID), `
		UPDATE google_resources SET next_sync_at = now(), failures = 0, backoff_until = NULL
		WHERE workspace_id = $1 AND connection_id = $2`, st.workspaceID, id)
	DiscoverSoon(a, st.workspaceID, id)
	out.Result = ResultConnected
	return out
}

func localeOf(returnTo string) string {
	if strings.HasPrefix(returnTo, "/ar/") {
		return "ar"
	}
	return "en"
}

// giveBack revokes a grant the callback is refusing, unless this Google
// account is connected somewhere in Moharrik already. Google revokes the whole
// (account, client) grant, so revoking here would silently break a connection
// that is working — including one in this same workspace when the person was
// only adding a product. The check is a boolean on the owner pool and nothing
// about the other connection is revealed.
func giveBack(ctx context.Context, a *app.App, sub, token string) {
	if token == "" {
		return
	}
	if sub != "" {
		var inUse bool
		if err := a.SysDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM google_connections WHERE google_sub = $1)`, sub).Scan(&inUse); err != nil || inUse {
			return
		}
	}
	if err := revoke(ctx, token); err != nil {
		logWarn(a, "google connect: could not revoke a refused grant", err)
	}
}

// saveConnection stores or refreshes the (workspace, account) row, inside the
// workspace on the app pool, so RLS decides where it can land.
func saveConnection(ctx context.Context, a *app.App, workspaceID, sub, email, refresh string, granted []string, userID string) (string, error) {
	sealed, err := refreshBox.Seal(refresh)
	if err != nil {
		return "", err
	}
	var id string
	err = a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, workspaceID), `
		INSERT INTO google_connections (workspace_id, google_sub, email, refresh_token_sealed, granted_scopes, status, auth_error, connected_by)
		VALUES ($1, $2, $3, $4, $5, '', '', $6)
		ON CONFLICT (workspace_id, google_sub) DO UPDATE SET
			email = EXCLUDED.email, refresh_token_sealed = EXCLUDED.refresh_token_sealed,
			granted_scopes = EXCLUDED.granted_scopes, status = '', auth_error = '',
			connected_by = EXCLUDED.connected_by, updated_at = now()
		RETURNING id`,
		workspaceID, sub, email, sealed, pgtext.Literal(granted), userID).Scan(&id)
	return id, err
}

// ---- Google's token endpoint ---------------------------------------------------

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
}

// tokenError is Google's token-endpoint refusal. It carries the status and the
// error code only — never the request, which holds the secret and the token.
type tokenError struct {
	Status int
	Code   string
}

func (e *tokenError) Error() string {
	return fmt.Sprintf("google token endpoint: %d %s", e.Status, e.Code)
}

func tokenCall(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, current().TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google token endpoint unreachable: %w", scrubURLError(err))
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	var out tokenResponse
	_ = json.Unmarshal(body, &out)
	if res.StatusCode != http.StatusOK || out.AccessToken == "" {
		code := out.Error
		if code == "" {
			code = "no_access_token"
		}
		return nil, &tokenError{Status: res.StatusCode, Code: code}
	}
	return &out, nil
}

func exchangeCode(ctx context.Context, cfg Config, code, verifier string) (*tokenResponse, error) {
	return tokenCall(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"redirect_uri":  {cfg.RedirectURL},
	})
}

// scrubURLError drops the URL from a transport error.
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %v", ue.Op, ue.Err)
	}
	return err
}

// ---- who is this account -----------------------------------------------------

// identify reads the account's stable id and email. The id_token came straight
// from Google's token endpoint over TLS, in answer to our own client
// credentials, so (as OpenID Connect allows for exactly this case) its claims
// are read without a signature check. They are still checked for audience,
// issuer and expiry. Without an id_token, the userinfo endpoint answers.
func identify(ctx context.Context, cfg Config, tok *tokenResponse) (sub, email string, err error) {
	if tok.IDToken != "" {
		if sub, email, err = idTokenClaims(tok.IDToken, cfg.ClientID); err == nil {
			return sub, email, nil
		}
	}
	return userInfo(ctx, tok.AccessToken)
}

func idTokenClaims(raw, clientID string) (string, string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", "", errors.New("id_token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errors.New("id_token payload is not base64url")
	}
	var c struct {
		Iss   string          `json:"iss"`
		Aud   json.RawMessage `json:"aud"`
		Sub   string          `json:"sub"`
		Email string          `json:"email"`
		Exp   int64           `json:"exp"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", "", errors.New("id_token claims are not JSON")
	}
	if c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com" {
		return "", "", errors.New("id_token has the wrong issuer")
	}
	if !audienceHas(c.Aud, clientID) {
		return "", "", errors.New("id_token is for another client")
	}
	if c.Exp != 0 && time.Now().After(time.Unix(c.Exp, 0).Add(5*time.Minute)) {
		return "", "", errors.New("id_token has expired")
	}
	if c.Sub == "" {
		return "", "", errors.New("id_token has no subject")
	}
	return c.Sub, c.Email, nil
}

func audienceHas(raw json.RawMessage, clientID string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == clientID {
				return true
			}
		}
	}
	return false
}

func userInfo(ctx context.Context, access string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, current().UserInfoURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("google userinfo unreachable: %w", scrubURLError(err))
	}
	defer res.Body.Close()
	var u struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&u)
	if res.StatusCode != http.StatusOK || u.Sub == "" {
		return "", "", fmt.Errorf("google userinfo: %d", res.StatusCode)
	}
	return u.Sub, u.Email, nil
}

// ---- revoke -------------------------------------------------------------------

// revoke asks Google to revoke a token. The token goes in the POST body, never
// the URL. 400 invalid_token means it is already gone, which is the goal.
func revoke(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, current().RevokeURL,
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("google revoke unreachable: %w", scrubURLError(err))
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<14))
	if res.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	if res.StatusCode == http.StatusBadRequest && e.Error == "invalid_token" {
		return nil
	}
	return fmt.Errorf("google revoke: %d %s", res.StatusCode, e.Error)
}

func logWarn(a *app.App, msg string, err error) {
	if a != nil && a.Log != nil {
		a.Log.Warn(msg, "err", err)
	}
}
