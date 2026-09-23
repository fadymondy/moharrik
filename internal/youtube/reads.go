package youtube

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
)

// YTChannel is the channel as last synced. Nil fields were never synced.
type YTChannel struct {
	ChannelID         string       `json:"channel_id"`
	Title             string       `json:"title"`
	Handle            string       `json:"handle"`
	AvatarURL         string       `json:"avatar_url"`
	Subscribers       int64        `json:"subscribers"`
	SubscribersHidden bool         `json:"subscribers_hidden" doc:"The channel hides its subscriber count; subscribers is then not shown."`
	Videos            int64        `json:"videos" doc:"YouTube's own channel counter (statistics.videoCount): public videos only, and it can lag or leave some out."`
	Uploads           int64        `json:"uploads" doc:"The uploads Moharrik read from the channel's uploads playlist, every privacy included."`
	PublicUploads     int64        `json:"public_uploads" doc:"Of those, the public ones: what to compare videos with."`
	Steps             []YTSyncStep `json:"steps" doc:"How each part of the last sync went."`
	Views             int64        `json:"views" doc:"Lifetime views."`
	UploadsComplete   bool         `json:"uploads_complete" doc:"The last sync read the whole uploads playlist."`
	UpdatedAt         time.Time    `json:"updated_at"`
	CommentsSeenUntil *time.Time   `json:"comments_seen_until"`
	URL               string       `json:"url"`
}

// GetChannel reads the stored channel; nil when it was never synced.
func GetChannel(ctx context.Context, db *sql.DB, ws, resourceID string) (*YTChannel, error) {
	var c YTChannel
	var steps []byte
	err := db.QueryRowContext(ctx, `SELECT channel_id, title, handle, avatar_url, subscriber_count, subscribers_hidden,
		video_count, view_count, uploads_complete, updated_at, comments_seen_until, step_status,
		(SELECT count(*) FROM youtube_videos v WHERE v.workspace_id = c.workspace_id AND v.resource_id = c.resource_id AND v.present),
		(SELECT count(*) FROM youtube_videos v WHERE v.workspace_id = c.workspace_id AND v.resource_id = c.resource_id AND v.present
		   AND v.privacy_status = 'public')
		FROM youtube_channels c WHERE workspace_id = $1 AND resource_id = $2`, ws, resourceID).
		Scan(&c.ChannelID, &c.Title, &c.Handle, &c.AvatarURL, &c.Subscribers, &c.SubscribersHidden, &c.Videos, &c.Views,
			&c.UploadsComplete, &c.UpdatedAt, &c.CommentsSeenUntil, &steps, &c.Uploads, &c.PublicUploads)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(steps, &c.Steps)
	if c.Steps == nil {
		c.Steps = []YTSyncStep{}
	}
	c.URL = "https://www.youtube.com/channel/" + url.PathEscape(c.ChannelID)
	if c.Handle != "" {
		c.URL = "https://www.youtube.com/" + url.PathEscape(c.Handle)
	}
	return &c, nil
}

// YTVideo is one upload.
type YTVideo struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	Tags             []string   `json:"tags"`
	ThumbnailURL     string     `json:"thumbnail_url"`
	PublishedAt      *time.Time `json:"published_at"`
	ScheduledAt      *time.Time `json:"scheduled_at"`
	Scheduled        bool       `json:"scheduled"`
	PrivacyStatus    string     `json:"privacy_status" doc:"public, unlisted or private."`
	DurationSeconds  int64      `json:"duration_seconds"`
	Views            int64      `json:"views"`
	Likes            *int64     `json:"likes" doc:"Null when the video hides its likes."`
	Comments         *int64     `json:"comments" doc:"Null when comments are turned off."`
	CommentsDisabled bool       `json:"comments_disabled"`
	SyncedAt         time.Time  `json:"synced_at"`
	URL              string     `json:"url"`
	StudioURL        string     `json:"studio_url"`
}

const videoColumns = `video_id, title, description, tags, thumbnail_url, published_at, scheduled_at, privacy_status,
	duration_seconds, view_count, like_count, comment_count, comments_disabled, synced_at`

type scanner interface{ Scan(...any) error }

func scanVideo(r scanner) (YTVideo, error) {
	var v YTVideo
	var tags []byte
	err := r.Scan(&v.ID, &v.Title, &v.Description, &tags, &v.ThumbnailURL, &v.PublishedAt, &v.ScheduledAt, &v.PrivacyStatus,
		&v.DurationSeconds, &v.Views, &v.Likes, &v.Comments, &v.CommentsDisabled, &v.SyncedAt)
	if err != nil {
		return v, err
	}
	_ = json.Unmarshal(tags, &v.Tags)
	if v.Tags == nil {
		v.Tags = []string{}
	}
	v.Scheduled = v.PrivacyStatus == "private" && v.ScheduledAt != nil && v.ScheduledAt.After(time.Now())
	v.URL = "https://www.youtube.com/watch?v=" + url.QueryEscape(v.ID)
	v.StudioURL = "https://studio.youtube.com/video/" + url.PathEscape(v.ID) + "/edit"
	return v, nil
}

// YTVideoFilter narrows the list.
type YTVideoFilter struct {
	Query   string
	Privacy string // "", public, unlisted, private, scheduled
	Cursor  string
	Limit   int
}

// YTVideoPage is one page of uploads, newest first.
type YTVideoPage struct {
	Videos     []YTVideo        `json:"videos"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
	Total      int64            `json:"total" doc:"Under the search, whatever the privacy filter."`
	Counts     map[string]int64 `json:"counts" doc:"public, unlisted, private, scheduled — under the search."`
}

// ErrInvalid is a bad filter.
type ErrInvalid struct{ Msg string }

func (e ErrInvalid) Error() string { return e.Msg }

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListVideos pages through the stored uploads of one channel.
func ListVideos(ctx context.Context, db *sql.DB, ws, resourceID string, f YTVideoFilter) (*YTVideoPage, error) {
	limit := f.Limit
	if limit <= 0 || limit > 50 {
		limit = 24
	}
	args := []any{ws, resourceID}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where := []string{"workspace_id = $1", "resource_id = $2", "present"}
	if q := strings.TrimSpace(f.Query); q != "" {
		p := arg("%" + likeEscape(q) + "%")
		where = append(where, fmt.Sprintf(`(title ILIKE %[1]s ESCAPE '\' OR description ILIKE %[1]s ESCAPE '\' OR tags::text ILIKE %[1]s ESCAPE '\')`, p))
	}
	base, baseArgs := append([]string(nil), where...), append([]any(nil), args...)
	switch f.Privacy {
	case "", "all":
	case "public", "unlisted":
		where = append(where, "privacy_status = "+arg(f.Privacy))
	case "private":
		where = append(where, "privacy_status = 'private' AND (scheduled_at IS NULL OR scheduled_at <= now())")
	case "scheduled":
		where = append(where, "privacy_status = 'private' AND scheduled_at > now()")
	default:
		return nil, ErrInvalid{"privacy must be public, unlisted, private or scheduled"}
	}
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		ts, id, ok := strings.Cut(string(raw), "|")
		t, perr := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !ok || perr != nil || id == "" {
			return nil, ErrInvalid{"cursor is not valid"}
		}
		where = append(where, fmt.Sprintf("(coalesce(published_at, 'epoch'), video_id) < (%s, %s)", arg(t), arg(id)))
	}
	rows, err := db.QueryContext(ctx, `SELECT `+videoColumns+` FROM youtube_videos WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` ORDER BY coalesce(published_at, 'epoch') DESC, video_id DESC LIMIT %d`, limit+1), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &YTVideoPage{Videos: []YTVideo{}, Counts: map[string]int64{}}
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		page.Videos = append(page.Videos, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Videos) > limit {
		page.Videos = page.Videos[:limit]
		last := page.Videos[limit-1]
		at := time.Unix(0, 0).UTC()
		if last.PublishedAt != nil {
			at = *last.PublishedAt
		}
		page.HasMore = true
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
	}
	var pub, unl, priv, sched int64
	if err := db.QueryRowContext(ctx, `SELECT count(*),
		count(*) FILTER (WHERE privacy_status = 'public'), count(*) FILTER (WHERE privacy_status = 'unlisted'),
		count(*) FILTER (WHERE privacy_status = 'private' AND (scheduled_at IS NULL OR scheduled_at <= now())),
		count(*) FILTER (WHERE privacy_status = 'private' AND scheduled_at > now())
		FROM youtube_videos WHERE `+strings.Join(base, " AND "), baseArgs...).Scan(&page.Total, &pub, &unl, &priv, &sched); err != nil {
		return nil, err
	}
	page.Counts["public"], page.Counts["unlisted"], page.Counts["private"], page.Counts["scheduled"] = pub, unl, priv, sched
	return page, nil
}

// YTComment is a top-level comment thread.
type YTComment struct {
	ID          string     `json:"id"`
	VideoID     string     `json:"video_id"`
	Author      string     `json:"author"`
	AuthorImage string     `json:"author_avatar_url"`
	Text        string     `json:"text"`
	Likes       int64      `json:"likes"`
	Replies     int64      `json:"replies"`
	Answered    bool       `json:"answered" doc:"The channel has replied in the thread."`
	PublishedAt *time.Time `json:"published_at"`
	ReplyURL    string     `json:"reply_url"`
}

// YTVideoDetail is one upload with its top comments.
type YTVideoDetail struct {
	YTVideo
	TopComments []YTComment `json:"top_comments"`
	// Why the comments shown may be old (quota, comments off, Google failing).
	CommentNote string `json:"comment_note,omitempty"`
}

// errNotPublic: the video's comments cannot be read with a key (not public).
var errNotPublic = errors.New("comments are only readable on public videos")

// ErrNotFound: no such video on this channel in this workspace.
var ErrNotFound = errors.New("that video is not on this channel")

// commentsFresh is how long a video's top comments are served from storage.
const commentsFresh = time.Hour

// GetVideo reads one video and its top comments, re-reading them from YouTube
// when older than an hour and the quota allows. ctx carries the workspace.
func GetVideo(ctx context.Context, a *app.App, ws string, r google.Resource, videoID string) (*YTVideoDetail, error) {
	v, err := scanVideo(a.SQLDB.QueryRowContext(ctx, `SELECT `+videoColumns+` FROM youtube_videos
		WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`, ws, r.ID, videoID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &YTVideoDetail{YTVideo: v, TopComments: []YTComment{}}
	var synced *time.Time
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT comments_synced_at FROM youtube_videos WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`,
		ws, r.ID, videoID).Scan(&synced)
	if v.CommentsDisabled {
		d.CommentNote = "comments_disabled"
	} else if synced == nil || time.Since(*synced) > commentsFresh {
		disabled, err := refreshTopComments(ctx, a, ws, r, videoID)
		switch {
		case errors.Is(err, ErrCommentsUnavailable):
			d.CommentNote = "comments_unavailable"
		case errors.Is(err, errNotPublic):
			d.CommentNote = "comments_not_public"
		case err != nil:
			d.CommentNote = google.Describe(err)
		}
		if disabled {
			d.CommentsDisabled, d.Comments = true, nil
		}
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT comment_id, video_id, author, author_avatar_url, text, like_count, reply_count, answered, published_at
		FROM youtube_comments WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3
		ORDER BY top DESC, like_count DESC, published_at DESC NULLS LAST LIMIT 20`, ws, r.ID, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c YTComment
		if err := rows.Scan(&c.ID, &c.VideoID, &c.Author, &c.AuthorImage, &c.Text, &c.Likes, &c.Replies, &c.Answered, &c.PublishedAt); err != nil {
			return nil, err
		}
		c.ReplyURL = ReplyURL(c.VideoID, c.ID)
		d.TopComments = append(d.TopComments, c)
	}
	if d.CommentsDisabled {
		d.CommentNote = "comments_disabled"
	}
	return d, rows.Err()
}

// refreshTopComments re-reads a video's top comments; disabled reports that
// YouTube says its comments are turned off.
func refreshTopComments(ctx context.Context, a *app.App, ws string, r google.Resource, videoID string) (disabled bool, err error) {
	var out struct {
		Items []apiThread `json:"items"`
	}
	err = readComments(ctx, a, ws, url.Values{
		"part": {"snippet,replies"}, "videoId": {videoID}, "order": {"relevance"}, "maxResults": {"20"}, "textFormat": {"plainText"},
	}, &out)
	var ae *google.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusForbidden && ae.Reason == "commentsDisabled" {
		_, err = a.SQLDB.ExecContext(ctx, `UPDATE youtube_videos SET comments_disabled = true, comments_synced_at = now()
			WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`, ws, r.ID, videoID)
		return true, err
	}
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		// A private or unlisted video: a key reads public comments only.
		if _, err := a.SQLDB.ExecContext(ctx, `UPDATE youtube_videos SET comments_synced_at = now()
			WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`, ws, r.ID, videoID); err != nil {
			return false, err
		}
		return false, errNotPublic
	}
	if err != nil {
		return false, err
	}
	channelID := r.ExternalID
	if _, err := a.SQLDB.ExecContext(ctx, `UPDATE youtube_comments SET top = false WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`,
		ws, r.ID, videoID); err != nil {
		return false, err
	}
	for _, t := range out.Items {
		if t.Snippet.VideoID == "" {
			t.Snippet.VideoID = videoID
		}
		if err := upsertThread(ctx, a.SQLDB, ws, r.ID, channelID, t, true); err != nil {
			return false, err
		}
	}
	_, err = a.SQLDB.ExecContext(ctx, `UPDATE youtube_videos SET comments_disabled = false, comments_synced_at = now()
		WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`, ws, r.ID, videoID)
	return false, err
}
