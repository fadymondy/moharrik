// Package appsignin signs the Moharrik apps in, and keeps the phones that
// receive native push (docs/apps-plan.md §2, §4).
//
// The desktop and mobile apps sign in in the SYSTEM browser and are redirected
// back — never an embedded web view (RFC 8252, "OAuth 2.0 for Native Apps"):
//
//  1. The app makes a PKCE verifier and opens
//     <web>/<locale>/app-signin?redirect_uri=…&code_challenge=…&state=…&device=…
//     in the default browser.
//  2. The person signs in there the way they sign in to app.moharrik.com and
//     confirms "Continue to the Moharrik app".
//  3. The page calls POST /api/me/app-signin (signed in, CSRF-checked): a
//     one-time code, two minutes, bound to the challenge and the redirect,
//     stored only as a hash.
//  4. The browser is sent to the app's redirect (a 127.0.0.1 port, or
//     moharrik://auth/callback) with the code and state.
//  5. The app calls POST /api/auth/app/token with the code and its verifier and
//     gets its own device token: a personal access token in the auth plugin's
//     format, so the plugin's middleware authenticates it with no new code path.
//
// A code alone is useless without the verifier, which never left the app; a
// verifier alone is useless without a code, which only the signed-in person's
// click produces. Ported from Mahaam (internal/account/appsignin.go).
package appsignin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/platform"
)

const (
	appCodeTTL = 2 * time.Minute
	// maxDevices bounds one person's push registrations; the oldest go first.
	maxDevices = 20
	patPrefix  = "togo_pat_"
)

var tags = []string{"Apps"}

// tokenLimiter bounds code-for-token trades per client network: the code is
// 24 random bytes, so this is about load, not guessing.
var tokenLimiter = inbox.NewMemoryLimiter()

// validAppRedirect accepts exactly the two shapes the apps use:
//   - http://127.0.0.1:<any port>/callback (or localhost, [::1]) — the desktop
//     app's loopback listener; the port is ephemeral, so it cannot be pinned
//     (RFC 8252 §7.3);
//   - moharrik://auth/callback — the mobile app (ASWebAuthenticationSession /
//     Custom Tabs).
func validAppRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.RawQuery != "" {
		return false
	}
	switch u.Scheme {
	case "http":
		h := u.Hostname()
		return (h == "127.0.0.1" || h == "localhost" || h == "::1") && u.Port() != "" && u.Path == "/callback"
	case "moharrik":
		return u.Opaque == "" && u.Host == "auth" && u.Path == "/callback"
	}
	return false
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// label keeps a device name readable and bounded; it is shown in the person's
// device list.
func label(name, fallback string, max int) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	if name == "" {
		name = fallback
	}
	if len([]rune(name)) > max {
		name = string([]rune(name)[:max])
	}
	return name
}

// bearerPAT is the caller's personal access token, when that is how they are
// signed in (the apps are; the web is not).
func bearerPAT(r *http.Request) string {
	if r == nil {
		return ""
	}
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		if t := strings.TrimSpace(h[7:]); strings.HasPrefix(t, patPrefix) {
			return t
		}
	}
	return ""
}

// mintDeviceToken issues a personal access token in the auth plugin's own
// format (auth@v0.8 pat.go: "togo_pat_" + 40 hex, only the sha256 stored, empty
// abilities = the user's own access; Moharrik's authorization is the
// workspace role), replacing any earlier token of the same name so a device
// never accumulates rows. Push registrations of a replaced token go with it.
func mintDeviceToken(ctx context.Context, db *sql.DB, userID, name string) (string, error) {
	plain := patPrefix + randHex(16) + randHex(4)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_tokens WHERE user_id = $1 AND pat_id IN
		(SELECT id FROM personal_access_tokens WHERE user_id = $1 AND name = $2)`, userID, name); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM personal_access_tokens WHERE user_id = $1 AND name = $2`, userID, name); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO personal_access_tokens (id, user_id, name, token_hash, abilities, expires_at, created_at)
		VALUES ($1, $2, $3, $4, '', NULL, $5)`,
		randHex(16), userID, name, sha256Hex(plain), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	return plain, tx.Commit()
}

// AppDevice is one of the caller's own app installs that receives push.
type AppDevice struct {
	ID          string     `json:"id"`
	Platform    string     `json:"platform" enum:"ios,android"`
	Name        string     `json:"name"`
	AppVersion  string     `json:"app_version"`
	Locale      string     `json:"locale" enum:"en,ar"`
	Current     bool       `json:"current" doc:"This is the install making the request."`
	CreatedAt   time.Time  `json:"created_at"`
	LastOKAt    *time.Time `json:"last_ok_at"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// AppAuth reads the caller's bearer token, to tell which app install is asking.
// Exported: huma only reads headers from exported embedded structs.
type AppAuth struct {
	Authorization string `header:"Authorization" doc:"Bearer <the app's device token>."`
}

func (h AppAuth) pat() string {
	r := &http.Request{Header: http.Header{"Authorization": []string{h.Authorization}}}
	return bearerPAT(r)
}

func identity(ctx context.Context) (*auth.Identity, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id == nil {
		return nil, huma.Error401Unauthorized("unauthorized")
	}
	return id, nil
}

// patID is the id of the caller's own device token ("" when they are not
// signed in with one).
func patID(ctx context.Context, db *sql.DB, userID, plain string) string {
	if plain == "" {
		return ""
	}
	var id string
	_ = db.QueryRowContext(ctx, `SELECT id FROM personal_access_tokens WHERE token_hash = $1 AND user_id = $2`, sha256Hex(plain), userID).Scan(&id)
	return id
}

// Register mounts the app sign-in and device endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SysDB }

	huma.Register(api, huma.Operation{OperationID: "me-app-signin", Method: http.MethodPost, Path: "/api/me/app-signin",
		Summary: "Hand this browser's sign-in to a Moharrik app", Tags: tags,
		Description: "Called by the web's app sign-in page after the person confirms. Returns where to send the browser: the app's " +
			"redirect (a loopback port or moharrik://auth/callback) with a one-time code bound to the app's PKCE challenge."},
		func(ctx context.Context, in *struct {
			Body struct {
				RedirectURI         string `json:"redirect_uri" maxLength:"200"`
				CodeChallenge       string `json:"code_challenge" minLength:"43" maxLength:"128"`
				CodeChallengeMethod string `json:"code_challenge_method" enum:"S256"`
				State               string `json:"state" minLength:"16" maxLength:"128"`
				Device              string `json:"device" maxLength:"200"`
			}
		}) (*body[struct {
			RedirectTo string `json:"redirect_to" doc:"Send the browser here: the app's redirect with code and state."`
		}], error) {
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			if !validAppRedirect(in.Body.RedirectURI) {
				return nil, huma.Error400BadRequest("that is not a Moharrik app's redirect")
			}
			code := "mas_" + randHex(24)
			if _, err := db().ExecContext(ctx, `
				INSERT INTO app_signin_codes (code_hash, user_id, redirect_uri, code_challenge, device_name, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				sha256Hex(code), id.ID, in.Body.RedirectURI, in.Body.CodeChallenge, label(in.Body.Device, "Moharrik app", 120),
				time.Now().Add(appCodeTTL)); err != nil {
				a.Log.Error("app sign-in code", "err", err)
				return nil, huma.Error500InternalServerError("could not start the app sign-in")
			}
			// Housekeeping: expired codes are useless; drop them as new ones are made.
			_, _ = db().ExecContext(ctx, `DELETE FROM app_signin_codes WHERE expires_at < now() - interval '1 hour'`)
			dest, _ := url.Parse(in.Body.RedirectURI)
			q := url.Values{}
			q.Set("code", code)
			q.Set("state", in.Body.State)
			dest.RawQuery = q.Encode()
			return out(struct {
				RedirectTo string `json:"redirect_to" doc:"Send the browser here: the app's redirect with code and state."`
			}{dest.String()}), nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-app-token", Method: http.MethodPost, Path: "/api/auth/app/token",
		Summary: "Trade an app sign-in code for this device's token", Tags: tags,
		Description: "The last step of signing a Moharrik app in through the browser: the one-time code from the redirect and the " +
			"PKCE verifier the app kept. Returns a personal access token named for the device. Public: the code and its verifier " +
			"are the credential; rate-limited per client network."},
		func(ctx context.Context, in *struct {
			Body struct {
				Code         string `json:"code" minLength:"1" maxLength:"128"`
				CodeVerifier string `json:"code_verifier" minLength:"43" maxLength:"128"`
				RedirectURI  string `json:"redirect_uri" maxLength:"200"`
			}
		}) (*body[struct {
			Token string `json:"token" doc:"This device's personal access token. Returned once; send it as Authorization: Bearer."`
			Email string `json:"email"`
		}], error) {
			if ok, _ := tokenLimiter.Take(time.Now(), inbox.Rule{Key: "app-token:" + clientip.Bucket(clientip.From(ctx)), Limit: 30, Window: 15 * time.Minute}); !ok {
				return nil, huma.Error429TooManyRequests("too many sign-in attempts — wait a few minutes")
			}
			var userID, redirect, challenge, device string
			var expires time.Time
			// Looked up first, consumed only once every check passes, so a wrong
			// verifier cannot burn the legitimate app's code.
			err := db().QueryRowContext(ctx, `
				SELECT user_id, redirect_uri, code_challenge, device_name, expires_at FROM app_signin_codes WHERE code_hash = $1`,
				sha256Hex(in.Body.Code)).Scan(&userID, &redirect, &challenge, &device, &expires)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error400BadRequest("unknown or already-used code")
			}
			if err != nil {
				a.Log.Error("app sign-in token", "err", err)
				return nil, huma.Error500InternalServerError("something went wrong")
			}
			if time.Now().After(expires) {
				return nil, huma.Error400BadRequest("the code expired — sign in again")
			}
			if in.Body.RedirectURI != redirect || pkceS256(in.Body.CodeVerifier) != challenge {
				return nil, huma.Error400BadRequest("the code does not belong to this sign-in")
			}
			res, err := db().ExecContext(ctx, `DELETE FROM app_signin_codes WHERE code_hash = $1`, sha256Hex(in.Body.Code))
			if err != nil {
				return nil, huma.Error500InternalServerError("something went wrong")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error400BadRequest("unknown or already-used code")
			}
			u, err := platform.Load(ctx, db(), userID)
			if err != nil {
				return nil, huma.Error400BadRequest("the account is gone")
			}
			if u.Suspended() {
				return nil, huma.Error403Forbidden(u.SuspendedMessage())
			}
			token, err := mintDeviceToken(ctx, db(), userID, device)
			if err != nil {
				a.Log.Error("app sign-in mint", "err", err)
				return nil, huma.Error500InternalServerError("could not issue the device token")
			}
			return out(struct {
				Token string `json:"token" doc:"This device's personal access token. Returned once; send it as Authorization: Bearer."`
				Email string `json:"email"`
			}{token, u.Email}), nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-app-signout", Method: http.MethodPost, Path: "/api/me/app-signout",
		DefaultStatus: http.StatusNoContent, Summary: "Sign this app out: revoke its device token and stop its push", Tags: tags},
		func(ctx context.Context, in *AppAuth) (*struct{}, error) {
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			pid := patID(ctx, db(), id.ID, in.pat())
			if pid == "" {
				return nil, huma.Error409Conflict("this request is not signed in with an app's device token")
			}
			if _, err := db().ExecContext(ctx, `DELETE FROM device_tokens WHERE user_id = $1 AND pat_id = $2`, id.ID, pid); err != nil {
				return nil, huma.Error500InternalServerError("could not sign the app out")
			}
			if _, err := db().ExecContext(ctx, `DELETE FROM personal_access_tokens WHERE user_id = $1 AND id = $2`, id.ID, pid); err != nil {
				return nil, huma.Error500InternalServerError("could not sign the app out")
			}
			return &struct{}{}, nil
		})

	// ---- the phones that receive push ----------------------------------------

	scan := func(rows *sql.Rows, current string) ([]AppDevice, error) {
		defer rows.Close()
		list := []AppDevice{}
		for rows.Next() {
			var d AppDevice
			var pid sql.NullString
			if err := rows.Scan(&d.ID, &d.Platform, &d.Name, &d.AppVersion, &d.Locale, &pid, &d.CreatedAt, &d.LastOKAt, &d.LastError, &d.LastErrorAt); err != nil {
				return nil, err
			}
			d.Current = current != "" && pid.String == current
			list = append(list, d)
		}
		return list, rows.Err()
	}
	const cols = `id, platform, name, app_version, locale, pat_id, created_at, last_ok_at, last_error, last_error_at`

	huma.Register(api, huma.Operation{OperationID: "my-app-devices", Method: http.MethodGet, Path: "/api/me/devices",
		Summary: "My app installs that receive push notifications", Tags: tags},
		func(ctx context.Context, in *AppAuth) (*body[struct {
			Devices []AppDevice `json:"devices"`
		}], error) {
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			rows, err := db().QueryContext(ctx, `SELECT `+cols+` FROM device_tokens WHERE user_id = $1 ORDER BY created_at DESC`, id.ID)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list your devices")
			}
			list, err := scan(rows, patID(ctx, db(), id.ID, in.pat()))
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list your devices")
			}
			return out(struct {
				Devices []AppDevice `json:"devices"`
			}{list}), nil
		})

	huma.Register(api, huma.Operation{OperationID: "register-my-app-device", Method: http.MethodPost, Path: "/api/me/devices",
		Summary: "Register this app install for push notifications (idempotent per FCM token)", Tags: tags,
		Description: "One registration reaches the member from every workspace they belong to; what each workspace pushes is " +
			"decided there (PUT /api/w/push/prefs). Registering the same token again — or after another person signs in on the " +
			"same phone — moves the row to the caller."},
		func(ctx context.Context, in *struct {
			AppAuth
			Body struct {
				Platform   string `json:"platform" enum:"ios,android"`
				Token      string `json:"token" minLength:"20" maxLength:"4096" doc:"The FCM registration token."`
				Name       string `json:"name,omitempty" maxLength:"200" doc:"The phone's name, shown in the device list."`
				AppVersion string `json:"app_version,omitempty" maxLength:"40"`
				Locale     string `json:"locale,omitempty" enum:"en,ar" doc:"The language the notifications are written in."`
			}
		}) (*body[struct {
			Device AppDevice `json:"device"`
		}], error) {
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			locale := in.Body.Locale
			if locale != "en" {
				locale = "ar"
			}
			pid := patID(ctx, db(), id.ID, in.pat())
			var pidArg any
			if pid != "" {
				pidArg = pid
			}
			rows, err := db().QueryContext(ctx, `
				INSERT INTO device_tokens (user_id, platform, token, pat_id, name, app_version, locale)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (token) DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, pat_id = EXCLUDED.pat_id,
				  name = EXCLUDED.name, app_version = EXCLUDED.app_version, locale = EXCLUDED.locale,
				  last_error = '', last_error_at = NULL, updated_at = now()
				RETURNING `+cols,
				id.ID, in.Body.Platform, strings.TrimSpace(in.Body.Token), pidArg,
				label(in.Body.Name, "Phone", 120), label(in.Body.AppVersion, "", 40), locale)
			if err != nil {
				a.Log.Error("register app device", "err", err)
				return nil, huma.Error500InternalServerError("could not register the device")
			}
			list, err := scan(rows, pid)
			if err != nil || len(list) != 1 {
				return nil, huma.Error500InternalServerError("could not register the device")
			}
			// Bounded per person: the oldest registrations beyond the cap go.
			_, _ = db().ExecContext(ctx, `DELETE FROM device_tokens WHERE user_id = $1 AND id IN (
				SELECT id FROM device_tokens WHERE user_id = $1 ORDER BY updated_at DESC OFFSET $2)`, id.ID, maxDevices)
			return out(struct {
				Device AppDevice `json:"device"`
			}{list[0]}), nil
		})

	huma.Register(api, huma.Operation{OperationID: "remove-my-app-device", Method: http.MethodDelete, Path: "/api/me/devices/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Stop push to one of my app installs", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id" maxLength:"64"`
		}) (*struct{}, error) {
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			res, err := db().ExecContext(ctx, `DELETE FROM device_tokens WHERE user_id = $1 AND id = $2`, id.ID, in.ID)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not remove the device")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("device not found")
			}
			return &struct{}{}, nil
		})
}
