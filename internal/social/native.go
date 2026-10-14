package social

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/appsignin"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/inbox"
)

/*
The apps' native sign-in. Same result as the browser flow's POST /api/auth/app/token: a device
token (a personal access token) for this install.

  - Apple (iOS, expo-apple-authentication): the identityToken, the authorizationCode and the raw
    nonce the app generated. The identity token must be Apple-signed for one of APPLE_BUNDLE_IDS
    and carry the nonce (raw or its sha256 hex — the app hands Apple the hash); the nonce is
    single use. The code is exchanged at Apple's token endpoint with the bundle id as client: that
    proves it is fresh and yields the refresh token kept for revocation (5.1.1(v)).
  - Google: an id_token from a native Google SDK, for one of OAUTH_GOOGLE_AUDIENCES (or the web
    client). The current apps use the browser flow for Google instead; this is here so a native
    button can be added without a server change.

There is no browser here, so nobody is "signed in in the same browser": an existing password
account with the address is account_conflict — the person signs in with e-mail and connects the
provider on the web (Account → Sign-in methods).
*/

var nativeLimiter = inbox.NewMemoryLimiter()

// deviceSignInOut is a native sign-in's result: the same device token and refresh token as
// POST /api/auth/app/token.
type deviceSignInOut struct{ Body appsignin.Session }

// AppleNativeInput is what expo-apple-authentication returns, plus the raw nonce.
type AppleNativeInput struct {
	IdentityToken     string `json:"identity_token" minLength:"20" maxLength:"8192"`
	AuthorizationCode string `json:"authorization_code" minLength:"8" maxLength:"1024"`
	Nonce             string `json:"nonce" minLength:"16" maxLength:"256" doc:"The raw nonce; Apple was given it or its sha256 hex."`
	GivenName         string `json:"given_name,omitempty" maxLength:"100" doc:"Apple shares the name on the first consent only."`
	FamilyName        string `json:"family_name,omitempty" maxLength:"100"`
	Device            string `json:"device,omitempty" maxLength:"200"`
}

// GoogleNativeInput is an id_token from a native Google SDK.
type GoogleNativeInput struct {
	IDToken string `json:"id_token" minLength:"20" maxLength:"8192"`
	Device  string `json:"device,omitempty" maxLength:"200"`
}

// NativeError is a refused native sign-in: the code the app translates (signin.error.<code>).
type NativeError struct {
	status int
	Code   string `json:"code"`
	Detail string `json:"error"`
}

func (e *NativeError) Error() string  { return e.Detail }
func (e *NativeError) GetStatus() int { return e.status }

func nativeRefusal(r *Refusal) error {
	status := http.StatusUnauthorized
	switch r.Code {
	case ErrConflict, ErrTaken, ErrAlready:
		status = http.StatusConflict
	case ErrSuspended:
		status = http.StatusForbidden
	case ErrUnavailable:
		status = http.StatusServiceUnavailable
	case ErrNotAvailable:
		status = http.StatusNotFound
	}
	msg := r.Code
	if r.Err != nil {
		msg = r.Err.Error()
	}
	return &NativeError{status: status, Code: r.Code, Detail: msg}
}

func limited(ctx context.Context) error {
	if ok, _ := nativeLimiter.Take(time.Now(), inbox.Rule{Key: "native-signin:" + clientip.Bucket(clientip.From(ctx)), Limit: 30, Window: 15 * time.Minute}); !ok {
		return huma.Error429TooManyRequests("too many sign-in attempts — wait a few minutes")
	}
	return nil
}

func (s *Service) device(ctx context.Context, userID, device string) (*deviceSignInOut, error) {
	sess, err := appsignin.IssueSession(ctx, s.DB, userID, device)
	if err != nil {
		var se huma.StatusError
		if errors.As(err, &se) {
			return nil, err
		}
		s.Log.Error("social: minting the device token", "err", err)
		return nil, nativeRefusal(&Refusal{ErrUnavailable, err})
	}
	return &deviceSignInOut{Body: sess}, nil
}

// useNonce records a native nonce; false when it was seen before (a replay).
func (s *Service) useNonce(ctx context.Context, nonce string) bool {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO social_used_nonces (nonce_hash, expires_at) VALUES ($1, now() + interval '1 day') ON CONFLICT DO NOTHING`, sha256Hex(nonce))
	if err != nil {
		return false
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM social_used_nonces WHERE expires_at < now()`)
	n, _ := res.RowsAffected()
	return n == 1
}

// RegisterNative mounts the native endpoints. svc returns nil during the OpenAPI export.
func RegisterNative(api huma.API, svc func() *Service) {
	tags := []string{"Apps"}
	huma.Register(api, huma.Operation{OperationID: "auth-native-apple", Method: http.MethodPost, Path: "/api/auth/native/apple",
		Summary: "Sign a Moharrik app in with Sign in with Apple (iOS)", Tags: tags,
		Description: "Takes the identity token and authorization code from the native Sign in with Apple sheet and the raw nonce " +
			"the app generated; returns this device's token, like POST /api/auth/app/token. A refusal carries a code: " +
			"account_conflict (an account with this address has a password — sign in with e-mail, then connect Apple), " +
			"identity_taken, email_unverified, no_email, suspended, failed, not_configured. Public; rate-limited per network."},
		func(ctx context.Context, in *struct{ Body AppleNativeInput }) (*deviceSignInOut, error) {
			if err := limited(ctx); err != nil {
				return nil, err
			}
			s := svc()
			c := s.Env()
			if !c.AppleNative() {
				return nil, nativeRefusal(refuse(ErrNotAvailable, "Sign in with Apple is not configured on this server"))
			}
			claims, err := verifyIDToken(ctx, in.Body.IdentityToken, Endpoints.AppleJWKS, []string{Endpoints.AppleIssuer}, c.AppleBundleIDs, in.Body.Nonce)
			if err != nil {
				s.Log.Warn("social: apple native token refused", "err", err)
				return nil, nativeRefusal(refuse(ErrFailed, "the Apple identity token could not be verified"))
			}
			p := personFrom("apple", claims)
			if !s.useNonce(ctx, in.Body.Nonce) {
				return nil, nativeRefusal(refuse(ErrFailed, "that sign-in was already used"))
			}
			res, err := appleExchange(ctx, c, p.Audience, in.Body.AuthorizationCode, "", "")
			if err != nil || res.Person.Subject != p.Subject {
				s.Log.Warn("social: apple native code refused", "err", err)
				return nil, nativeRefusal(refuse(ErrFailed, "the Apple authorization code could not be verified"))
			}
			p.Name = strings.TrimSpace(strings.TrimSpace(in.Body.GivenName) + " " + strings.TrimSpace(in.Body.FamilyName))
			uid, ref := s.Resolve(ctx, p, IntentLogin, "")
			if ref != nil {
				return nil, nativeRefusal(ref)
			}
			s.keepAppleToken(ctx, p.Subject, res.ClientID, res.RefreshToken)
			return s.device(ctx, uid, in.Body.Device)
		})

	huma.Register(api, huma.Operation{OperationID: "auth-native-google", Method: http.MethodPost, Path: "/api/auth/native/google",
		Summary: "Sign a Moharrik app in with a native Google id_token", Tags: tags,
		Description: "For a native Google Sign-In SDK: the id_token must be for one of the server's Google client ids " +
			"(OAUTH_GOOGLE_AUDIENCES). Returns this device's token. Refusal codes as for Apple. Public; rate-limited per network."},
		func(ctx context.Context, in *struct{ Body GoogleNativeInput }) (*deviceSignInOut, error) {
			if err := limited(ctx); err != nil {
				return nil, err
			}
			s := svc()
			c := s.Env()
			if !c.GoogleNative() {
				return nil, nativeRefusal(refuse(ErrNotAvailable, "native Google sign-in is not configured on this server"))
			}
			p, err := googleNative(ctx, c, in.Body.IDToken)
			if err != nil {
				s.Log.Warn("social: google native token refused", "err", err)
				return nil, nativeRefusal(refuse(ErrFailed, "the Google token could not be verified"))
			}
			uid, ref := s.Resolve(ctx, p, IntentLogin, "")
			if ref != nil {
				return nil, nativeRefusal(ref)
			}
			return s.device(ctx, uid, in.Body.Device)
		})
}
