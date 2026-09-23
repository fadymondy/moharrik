package youtube

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
One channel's sync (the Google collector runs it every SyncEvery):

	channels.list mine=true                 1 unit   the channel and its counters
	playlistItems.list (uploads, 50/page)   1 unit a page
	videos.list (50 ids)                    1 unit a page
	commentThreads.list (channel, newest)   1 unit   new comments → notification center

A channel with no uploads has an uploads playlist Google answers 404
playlistNotFound for: that is zero videos, not an error. The walk stops at
MaxUploadPages; only a complete walk may mark a video gone. Comments published
after the watermark, written by someone other than the channel, become
notification-center items (source "YouTube"); the first sync only seeds the
watermark, so connecting never floods the center.
*/

// SyncEvery is how often a channel is synced.
const SyncEvery = 3 * time.Hour

// MaxUploadPages bounds one walk of the uploads playlist (50 a page).
var MaxUploadPages = 40

// data is a Data API list call, charged to the ledger first.
func data(ctx context.Context, a *app.App, ws string, c *google.Client, resource string, q url.Values, out any) error {
	if err := Reserve(ctx, a, ws, CostList); err != nil {
		return err
	}
	return c.Get(ctx, google.API().YouTube+"/"+resource+"?"+q.Encode(), out)
}

type thumbs map[string]struct {
	URL string `json:"url"`
}

func (t thumbs) best(prefer ...string) string {
	for _, p := range append(prefer, "medium", "high", "default", "standard", "maxres") {
		if v, ok := t[p]; ok && v.URL != "" {
			return v.URL
		}
	}
	return ""
}

// count parses the Data API's string counters; nil when absent.
func count(s *string) *int64 {
	if s == nil {
		return nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(*s), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

var isoDuration = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// DurationSeconds parses the Data API's ISO 8601 durations (PT1H2M3S); else 0.
func DurationSeconds(iso string) int64 {
	m := isoDuration.FindStringSubmatch(strings.TrimSpace(iso))
	if m == nil {
		return 0
	}
	mult := []int64{7 * 86400, 86400, 3600, 60, 1}
	var total int64
	for i, f := range m[1:] {
		if f != "" {
			n, _ := strconv.ParseInt(f, 10, 64)
			total += n * mult[i]
		}
	}
	return total
}

type channelState struct {
	ChannelID, Uploads string
	SeenUntil          *time.Time
}

// YTSyncStep is how one part of the last sync went.
type YTSyncStep struct {
	Name string `json:"name" enum:"channel,uploads,comments,analytics"`
	// ok | failed | unavailable (this server cannot do it: say why) | skipped
	State string    `json:"state" enum:"ok,failed,unavailable,skipped"`
	At    time.Time `json:"at"`
	Error string    `json:"error,omitempty"`
}

// StepNames are the steps, in the order a sync runs them.
var StepNames = []string{"channel", "uploads", "comments", "analytics"}

// ErrCommentsUnavailable: this server has no way to read comments. Google
// lets comments be read with a user's token only under youtube.force-ssl — a
// manage-your-YouTube-account permission Moharrik does not ask for — or with
// an API key, which is YOUTUBE_API_KEY.
var ErrCommentsUnavailable = errors.New("reading YouTube comments needs a YouTube Data API key on this server (YOUTUBE_API_KEY): " +
	"with read-only access, Google only lets comments be read with a key, and Moharrik does not ask for permission to manage the channel")

// APIKey is the YouTube Data API key public reads (comments) use; "" = none.
func APIKey() string { return strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY")) }

// readComments is a commentThreads.list call with the API key, charged to
// the ledger first. Comments are public data: the key reads what anyone can.
func readComments(ctx context.Context, a *app.App, ws string, q url.Values, out any) error {
	key := APIKey()
	if key == "" {
		return ErrCommentsUnavailable
	}
	if err := Reserve(ctx, a, ws, CostList); err != nil {
		return err
	}
	return google.PublicGet(ctx, google.API().YouTube+"/commentThreads?"+q.Encode(), key, out)
}

// Sync refreshes one channel: counters, every upload, new comments and the
// 28-day analytics snapshot. Each step's outcome is recorded on its own; the
// sync as a whole fails only when the channel or its uploads could not be
// read (the comments and analytics steps say so without failing the rest).
func Sync(ctx context.Context, a *app.App, r google.Resource) error {
	ws := tenant.WorkspaceID(ctx)
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return err
	}
	c := src.Client(google.ScopeYouTube)
	steps := []YTSyncStep{}
	record := func(name string, err error) {
		st := YTSyncStep{Name: name, State: "ok", At: time.Now().UTC()}
		switch {
		case errors.Is(err, ErrCommentsUnavailable):
			st.State, st.Error = "unavailable", err.Error()
		case err != nil:
			st.State, st.Error = "failed", google.Describe(err)
		}
		steps = append(steps, st)
	}
	st, err := syncChannel(ctx, a, ws, r, c)
	record("channel", err)
	if err != nil {
		for _, n := range StepNames[1:] {
			steps = append(steps, YTSyncStep{Name: n, State: "skipped", At: time.Now().UTC()})
		}
		saveSteps(ctx, a, ws, r.ID, steps)
		return err
	}
	uerr := syncUploads(ctx, a, ws, r, c, st)
	record("uploads", uerr)
	_, cerr := syncNewComments(ctx, a, ws, r, st)
	record("comments", cerr)
	record("analytics", refreshChannelSnapshot(ctx, a, ws, r))
	saveSteps(ctx, a, ws, r.ID, steps)
	return uerr
}

func saveSteps(ctx context.Context, a *app.App, ws, resourceID string, steps []YTSyncStep) {
	raw, _ := json.Marshal(steps)
	_, _ = a.SQLDB.ExecContext(ctx, `UPDATE youtube_channels SET step_status = $3 WHERE workspace_id = $1 AND resource_id = $2`,
		ws, resourceID, string(raw))
}

func syncChannel(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client) (*channelState, error) {
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title      string `json:"title"`
				CustomURL  string `json:"customUrl"`
				Thumbnails thumbs `json:"thumbnails"`
			} `json:"snippet"`
			ContentDetails struct {
				RelatedPlaylists struct {
					Uploads string `json:"uploads"`
				} `json:"relatedPlaylists"`
			} `json:"contentDetails"`
			Statistics struct {
				ViewCount             *string `json:"viewCount"`
				SubscriberCount       *string `json:"subscriberCount"`
				HiddenSubscriberCount bool    `json:"hiddenSubscriberCount"`
				VideoCount            *string `json:"videoCount"`
			} `json:"statistics"`
		} `json:"items"`
	}
	if err := data(ctx, a, ws, c, "channels", url.Values{"part": {"snippet,contentDetails,statistics"}, "mine": {"true"}}, &out); err != nil {
		return nil, err
	}
	var it *struct {
		ID      string `json:"id"`
		Snippet struct {
			Title      string `json:"title"`
			CustomURL  string `json:"customUrl"`
			Thumbnails thumbs `json:"thumbnails"`
		} `json:"snippet"`
		ContentDetails struct {
			RelatedPlaylists struct {
				Uploads string `json:"uploads"`
			} `json:"relatedPlaylists"`
		} `json:"contentDetails"`
		Statistics struct {
			ViewCount             *string `json:"viewCount"`
			SubscriberCount       *string `json:"subscriberCount"`
			HiddenSubscriberCount bool    `json:"hiddenSubscriberCount"`
			VideoCount            *string `json:"videoCount"`
		} `json:"statistics"`
	}
	for i := range out.Items {
		if out.Items[i].ID == r.ExternalID {
			it = &out.Items[i]
		}
	}
	if it == nil {
		return nil, &google.APIError{Status: http.StatusNotFound, Reason: "channelNotFound",
			Message: "this Google account no longer answers for channel " + r.ExternalID}
	}
	st := &channelState{ChannelID: it.ID, Uploads: it.ContentDetails.RelatedPlaylists.Uploads}
	err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO youtube_channels (workspace_id, resource_id, channel_id, title, handle, avatar_url, uploads_playlist_id,
			subscriber_count, subscribers_hidden, video_count, view_count, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
		ON CONFLICT (workspace_id, resource_id) DO UPDATE SET channel_id = EXCLUDED.channel_id, title = EXCLUDED.title,
			handle = EXCLUDED.handle, avatar_url = EXCLUDED.avatar_url, uploads_playlist_id = EXCLUDED.uploads_playlist_id,
			subscriber_count = EXCLUDED.subscriber_count, subscribers_hidden = EXCLUDED.subscribers_hidden,
			video_count = EXCLUDED.video_count, view_count = EXCLUDED.view_count, updated_at = now()
		RETURNING comments_seen_until`,
		ws, r.ID, it.ID, it.Snippet.Title, it.Snippet.CustomURL, it.Snippet.Thumbnails.best("medium"), st.Uploads,
		val(count(it.Statistics.SubscriberCount)), it.Statistics.HiddenSubscriberCount,
		val(count(it.Statistics.VideoCount)), val(count(it.Statistics.ViewCount))).Scan(&st.SeenUntil)
	return st, err
}

func syncUploads(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client, st *channelState) error {
	seen := []string{}
	complete := false
	page := ""
	position := int64(0)
	if st.Uploads == "" {
		complete = true
	}
	for pages := 0; !complete && pages < MaxUploadPages; pages++ {
		q := url.Values{"part": {"contentDetails"}, "playlistId": {st.Uploads}, "maxResults": {"50"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var out struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ContentDetails struct {
					VideoID string `json:"videoId"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		err := data(ctx, a, ws, c, "playlistItems", q, &out)
		var ae *google.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound && page == "" {
			// A channel with no uploads has no uploads playlist to read.
			complete = true
			break
		}
		if err != nil {
			return err
		}
		ids := []string{}
		for _, it := range out.Items {
			if it.ContentDetails.VideoID != "" {
				ids = append(ids, it.ContentDetails.VideoID)
			}
		}
		if len(ids) > 0 {
			if err := syncVideos(ctx, a, ws, r, c, ids, position); err != nil {
				return err
			}
			position += int64(len(ids))
			seen = append(seen, ids...)
		}
		if out.NextPageToken == "" {
			complete = true
			break
		}
		page = out.NextPageToken
	}
	if complete {
		raw, _ := json.Marshal(seen)
		if _, err := a.SQLDB.ExecContext(ctx, `UPDATE youtube_videos SET present = false
			WHERE workspace_id = $1 AND resource_id = $2 AND present
			  AND NOT (video_id = ANY (SELECT jsonb_array_elements_text($3::jsonb)))`, ws, r.ID, string(raw)); err != nil {
			return err
		}
	}
	_, err := a.SQLDB.ExecContext(ctx, `UPDATE youtube_channels SET uploads_complete = $3 WHERE workspace_id = $1 AND resource_id = $2`, ws, r.ID, complete)
	return err
}

type apiVideo struct {
	ID      string `json:"id"`
	Snippet struct {
		Title       string     `json:"title"`
		Description string     `json:"description"`
		PublishedAt *time.Time `json:"publishedAt"`
		Tags        []string   `json:"tags"`
		Thumbnails  thumbs     `json:"thumbnails"`
	} `json:"snippet"`
	Status struct {
		PrivacyStatus string     `json:"privacyStatus"`
		UploadStatus  string     `json:"uploadStatus"`
		PublishAt     *time.Time `json:"publishAt"`
	} `json:"status"`
	Statistics struct {
		ViewCount    *string `json:"viewCount"`
		LikeCount    *string `json:"likeCount"`
		CommentCount *string `json:"commentCount"`
	} `json:"statistics"`
	ContentDetails struct {
		Duration string `json:"duration"`
	} `json:"contentDetails"`
}

func syncVideos(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client, ids []string, position int64) error {
	var out struct {
		Items []apiVideo `json:"items"`
	}
	if err := data(ctx, a, ws, c, "videos", url.Values{
		"part": {"snippet,status,statistics,contentDetails"}, "id": {strings.Join(ids, ",")}, "maxResults": {"50"},
	}, &out); err != nil {
		return err
	}
	order := map[string]int64{}
	for i, id := range ids {
		order[id] = position + int64(i)
	}
	for _, v := range out.Items {
		tags := v.Snippet.Tags
		if tags == nil {
			tags = []string{}
		}
		raw, _ := json.Marshal(tags)
		if _, err := a.SQLDB.ExecContext(ctx, `
			INSERT INTO youtube_videos (workspace_id, resource_id, video_id, title, description, tags, thumbnail_url, published_at,
				scheduled_at, privacy_status, upload_status, duration_seconds, view_count, like_count, comment_count, position,
				present, synced_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,true,now())
			ON CONFLICT (workspace_id, resource_id, video_id) DO UPDATE SET title = EXCLUDED.title,
				description = EXCLUDED.description, tags = EXCLUDED.tags, thumbnail_url = EXCLUDED.thumbnail_url,
				published_at = EXCLUDED.published_at, scheduled_at = EXCLUDED.scheduled_at,
				privacy_status = EXCLUDED.privacy_status, upload_status = EXCLUDED.upload_status,
				duration_seconds = EXCLUDED.duration_seconds, view_count = EXCLUDED.view_count,
				like_count = EXCLUDED.like_count, comment_count = EXCLUDED.comment_count, position = EXCLUDED.position,
				present = true, synced_at = now()`,
			ws, r.ID, v.ID, v.Snippet.Title, v.Snippet.Description, string(raw), v.Snippet.Thumbnails.best("medium"),
			v.Snippet.PublishedAt, v.Status.PublishAt, v.Status.PrivacyStatus, v.Status.UploadStatus,
			DurationSeconds(v.ContentDetails.Duration), val(count(v.Statistics.ViewCount)), count(v.Statistics.LikeCount),
			count(v.Statistics.CommentCount), order[v.ID]); err != nil {
			return err
		}
	}
	return nil
}

// ---- comments ---------------------------------------------------------------

type apiThread struct {
	ID      string `json:"id"`
	Snippet struct {
		VideoID         string `json:"videoId"`
		TotalReplyCount int64  `json:"totalReplyCount"`
		TopLevelComment struct {
			Snippet struct {
				AuthorDisplayName     string `json:"authorDisplayName"`
				AuthorProfileImageURL string `json:"authorProfileImageUrl"`
				AuthorChannelID       struct {
					Value string `json:"value"`
				} `json:"authorChannelId"`
				TextOriginal string     `json:"textOriginal"`
				TextDisplay  string     `json:"textDisplay"`
				LikeCount    int64      `json:"likeCount"`
				PublishedAt  *time.Time `json:"publishedAt"`
			} `json:"snippet"`
		} `json:"topLevelComment"`
	} `json:"snippet"`
	Replies struct {
		Comments []struct {
			Snippet struct {
				AuthorChannelID struct {
					Value string `json:"value"`
				} `json:"authorChannelId"`
			} `json:"snippet"`
		} `json:"comments"`
	} `json:"replies"`
}

func (t apiThread) text() string {
	s := t.Snippet.TopLevelComment.Snippet
	if s.TextOriginal != "" {
		return s.TextOriginal
	}
	return s.TextDisplay
}

func (t apiThread) answeredBy(channelID string) bool {
	if t.Snippet.TopLevelComment.Snippet.AuthorChannelID.Value == channelID {
		return true
	}
	for _, r := range t.Replies.Comments {
		if r.Snippet.AuthorChannelID.Value == channelID {
			return true
		}
	}
	return false
}

// ReplyURL links to the comment on YouTube, where the reply box is.
func ReplyURL(videoID, commentID string) string {
	return "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID) + "&lc=" + url.QueryEscape(commentID)
}

func upsertThread(ctx context.Context, db *sql.DB, ws, resourceID, channelID string, t apiThread, top bool) error {
	s := t.Snippet.TopLevelComment.Snippet
	_, err := db.ExecContext(ctx, `
		INSERT INTO youtube_comments (workspace_id, resource_id, comment_id, video_id, author, author_channel_id,
			author_avatar_url, text, like_count, reply_count, answered, top, published_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now())
		ON CONFLICT (workspace_id, resource_id, comment_id) DO UPDATE SET video_id = EXCLUDED.video_id,
			author = EXCLUDED.author, author_channel_id = EXCLUDED.author_channel_id,
			author_avatar_url = EXCLUDED.author_avatar_url, text = EXCLUDED.text, like_count = EXCLUDED.like_count,
			reply_count = EXCLUDED.reply_count, answered = EXCLUDED.answered,
			top = youtube_comments.top OR EXCLUDED.top, updated_at = now()`,
		ws, resourceID, t.ID, t.Snippet.VideoID, s.AuthorDisplayName, s.AuthorChannelID.Value, s.AuthorProfileImageURL,
		t.text(), s.LikeCount, t.Snippet.TotalReplyCount, t.answeredBy(channelID), top, s.PublishedAt)
	return err
}

// syncNewComments reads the channel's newest threads (one unit, up to 100)
// and announces the ones after the watermark. It returns how many it announced.
func syncNewComments(ctx context.Context, a *app.App, ws string, r google.Resource, st *channelState) (int, error) {
	var out struct {
		Items []apiThread `json:"items"`
	}
	err := readComments(ctx, a, ws, url.Values{
		"part": {"snippet,replies"}, "allThreadsRelatedToChannelId": {st.ChannelID},
		"order": {"time"}, "maxResults": {"100"}, "textFormat": {"plainText"},
	}, &out)
	var ae *google.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusForbidden && ae.Reason == "commentsDisabled" {
		return 0, nil // the whole channel has comments off
	}
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return 0, nil // no public videos, so no public comment threads
	}
	if err != nil {
		return 0, err
	}
	first := st.SeenUntil == nil
	var watermark time.Time
	if !first {
		watermark = *st.SeenUntil
	}
	newest := watermark
	announced := 0
	titles := map[string]string{}
	for _, t := range out.Items {
		if err := upsertThread(ctx, a.SQLDB, ws, r.ID, st.ChannelID, t, false); err != nil {
			return announced, err
		}
		pub := t.Snippet.TopLevelComment.Snippet.PublishedAt
		if pub == nil {
			continue
		}
		if pub.After(newest) {
			newest = *pub
		}
		if first || !pub.After(watermark) || t.Snippet.TopLevelComment.Snippet.AuthorChannelID.Value == st.ChannelID {
			continue
		}
		title, ok := titles[t.Snippet.VideoID]
		if !ok {
			_ = a.SQLDB.QueryRowContext(ctx, `SELECT title FROM youtube_videos WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`,
				ws, r.ID, t.Snippet.VideoID).Scan(&title)
			titles[t.Snippet.VideoID] = title
		}
		if title == "" {
			title = t.Snippet.VideoID
		}
		if ok, err := announce(ctx, a, ws, r.ID, t, title); err != nil {
			return announced, err
		} else if ok {
			announced++
		}
	}
	if first && newest.IsZero() {
		newest = time.Now().UTC()
	}
	if first || newest.After(watermark) {
		if _, err := a.SQLDB.ExecContext(ctx, `UPDATE youtube_channels SET comments_seen_until = $3 WHERE workspace_id = $1 AND resource_id = $2`,
			ws, r.ID, newest.UTC()); err != nil {
			return announced, err
		}
	}
	return announced, nil
}

// announce files one new comment in the notification center, once, and tells
// the members who read the feed (the bell, and their devices).
func announce(ctx context.Context, a *app.App, ws, resourceID string, t apiThread, title string) (bool, error) {
	ext := "youtube:comment:" + t.ID
	var exists bool
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM notification_items
		WHERE workspace_id = $1 AND source_id IS NULL AND external_id = $2)`, ws, ext).Scan(&exists); err != nil || exists {
		return false, err
	}
	s := t.Snippet.TopLevelComment.Snippet
	if err := notifications.Emit(ctx, a.SQLDB, ws, notifications.Internal{
		SourceName: "YouTube", ExternalID: ext, Kind: "youtube_comment", Severity: "info",
		Title: "New comment on “" + title + "”", Body: t.text(), Actor: s.AuthorDisplayName,
		URL: ReplyURL(t.Snippet.VideoID, t.ID),
	}); err != nil {
		return false, err
	}
	var itemID string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT id FROM notification_items WHERE workspace_id = $1 AND source_id IS NULL AND external_id = $2`,
		ws, ext).Scan(&itemID)
	_, _ = a.SQLDB.ExecContext(ctx, `UPDATE youtube_comments SET notified_at = now() WHERE workspace_id = $1 AND resource_id = $2 AND comment_id = $3`,
		ws, resourceID, t.ID)
	alerts.Emit(ctx, a, ws, realtime.Event{Kind: "notification", ItemID: itemID, Title: "New comment on “" + title + "”",
		Severity: "info", Source: "YouTube", Permission: authz.NotificationsRead, Alerting: true})
	return true, nil
}

// RegisterSync plugs YouTube into the Google collector, and its quota ledger
// into discovery's channels.list call.
func RegisterSync() {
	google.RegisterSyncer(google.KindYT, Sync, google.Every(SyncEvery))
	google.SetYouTubeCharger(Reserve)
}
