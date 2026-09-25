package trends

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/marketing"
)

// RegisterDigest mounts the trends digest (MH-389): a member's own choice, so
// every route reads and writes the caller's row only. Reading trends is the
// right it needs — a digest of the trends center goes to whoever may read it.
func RegisterDigest(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-trends-digest", Method: http.MethodGet, Path: "/api/w/notify/digest",
		Summary: "Your trends digest: how often, when, in which language", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Locale string `query:"locale" enum:",en,ar" doc:"The page's language, the default for a digest never set."`
	}) (*body[DigestState], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		st, err := GetDigest(ctx, a, s.WorkspaceID, s.UserID, in.Locale)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read your digest settings")
		}
		return out(st), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-trends-digest-put", Method: http.MethodPut, Path: "/api/w/notify/digest",
		Summary: "Choose your trends digest", Description: "frequency off stops it. It goes to your own account email " +
			"through the workspace's SMTP service, at the hour given in your time zone (or the workspace's).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body DigestSettings
	}) (*body[DigestState], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		if err := SaveDigest(ctx, a, s.WorkspaceID, s.UserID, in.Body); err != nil {
			if errors.Is(err, ErrDigestBadZone) {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return nil, huma.Error500InternalServerError("could not save your digest settings")
		}
		st, err := GetDigest(ctx, a, s.WorkspaceID, s.UserID, in.Body.Locale)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read your digest settings")
		}
		return out(st), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-trends-digest-test", Method: http.MethodPost, Path: "/api/w/notify/digest/test",
		Summary: "Send yourself the trends digest now", Description: "As it stands, with your saved language and size; " +
			"a day's stories, or a week's for a weekly digest. Nothing is sent when there is nothing to send (status empty).",
		Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[DigestResult], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		st, err := GetDigest(ctx, a, s.WorkspaceID, s.UserID, "")
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read your digest settings")
		}
		// One test a minute: it is an email to yourself, not a sending tool.
		var recent int
		_ = a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM trend_digest_sends WHERE workspace_id = $1 AND user_id = $2
			AND kind = 'test' AND status <> 'failed' AND created_at > $3`, s.WorkspaceID, s.UserID, DigestClock().Add(-time.Minute)).Scan(&recent)
		if recent > 0 {
			return nil, huma.Error429TooManyRequests("a test digest went less than a minute ago")
		}
		res, err := SendDigest(ctx, a, s.WorkspaceID, s.UserID, st.DigestSettings, "test", "")
		switch {
		case errors.Is(err, marketing.ErrNoService):
			return nil, huma.Error409Conflict("this workspace has no email service to send it through; an admin adds one under Email sending")
		case errors.Is(err, ErrDigestNoEmail):
			return nil, huma.Error409Conflict(err.Error())
		case err != nil:
			return nil, huma.NewError(http.StatusFailedDependency, "the mail server refused it: " + res.Error)
		}
		return out(res), nil
	})
}
