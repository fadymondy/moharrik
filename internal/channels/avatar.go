package channels

/*
avatar.go is MH-588's Telegram/Slack/Discord half: fetch the sender's current
profile photo through whichever platform delivered the message, download it
through internal/photocache (resize to 256px, never hotlink the provider's
own URL), and cache it on the linked contact — or, unlinked, the conversation
itself — exactly the shape internal/whatsapp/avatar.go established first and
internal/inbox/channel.go's queueChannelPhotoRefresh wires in as a
background, rate-limited, best-effort fetch (a failure here must never block
filing the message that triggered it).

Each platform resolves to a downloadable photo URL differently:
  - Telegram: getUserProfilePhotos (the largest size) then getFile, whose
    file_path is appended to the Bot API's own /file/bot<token>/ base.
  - Slack: users.info's profile.image_192 — already a plain HTTPS URL.
  - Discord: GET /users/{id}'s avatar hash, turned into a cdn.discordapp.com
    URL (no auth needed to download it, only to ask Discord for the hash).

A Telegram chat id that starts with "-" is a group/channel, which has no
personal profile photo to fetch — skipped before any network call, the same
way a WhatsApp group would be (out of scope for this card).
*/

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/photocache"
)

// discordCDNBaseOverride is set by SetDiscordCDNBase (tests) — Discord's REST
// API and its CDN are different hosts, so this is a second override beside
// SetDiscordAPIBase.
var discordCDNBaseOverride string

func discordCDNBase() string {
	if discordCDNBaseOverride != "" {
		return discordCDNBaseOverride
	}
	return "https://cdn.discordapp.com"
}

// SetDiscordCDNBase points avatar CDN URLs at a fake server for tests.
func SetDiscordCDNBase(base string) func() {
	prev := discordCDNBaseOverride
	discordCDNBaseOverride = strings.TrimRight(base, "/")
	return func() { discordCDNBaseOverride = prev }
}

// channelPhotoLimiter is a shared per-workspace rate cap across all three
// platforms: a workspace with a busy Telegram group AND a busy Slack channel
// at once still only asks its providers for one new photo at a time.
var channelPhotoLimiter = photocache.NewLimiter(1500 * time.Millisecond)

func telegramToken(ctx context.Context, db *sql.DB, workspaceID, connID string) (string, error) {
	_, botSealed, _, _, err := getTelegram(ctx, db, workspaceID, connID)
	if err != nil {
		return "", err
	}
	if botSealed == "" {
		return "", ErrTelegramNotFound
	}
	return telegramBotBox.Open(botSealed)
}

type tgUserProfilePhotos struct {
	Result struct {
		Photos [][]struct {
			FileID string `json:"file_id"`
		} `json:"photos"`
	} `json:"result"`
}

type tgFile struct {
	Result struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

// fetchTelegramPhotoURL asks Telegram for userID's current profile photo (the
// largest size Telegram offers) and resolves it to a downloadable file URL.
// "" with a nil error means Telegram genuinely has none for this user.
func fetchTelegramPhotoURL(ctx context.Context, token, userID string) (string, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return "", nil // not a numeric Telegram user id — nothing to fetch
	}
	raw, err := telegramCall(ctx, token, "getUserProfilePhotos", map[string]any{"user_id": uid, "limit": 1})
	if err != nil {
		return "", err
	}
	var pp tgUserProfilePhotos
	if json.Unmarshal(raw, &pp) != nil || len(pp.Result.Photos) == 0 || len(pp.Result.Photos[0]) == 0 {
		return "", nil
	}
	sizes := pp.Result.Photos[0]
	fileID := sizes[len(sizes)-1].FileID // Telegram lists sizes smallest first
	raw2, err := telegramCall(ctx, token, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return "", err
	}
	var f tgFile
	if json.Unmarshal(raw2, &f) != nil || f.Result.FilePath == "" {
		return "", nil
	}
	return telegramAPIBase() + "/file/bot" + token + "/" + f.Result.FilePath, nil
}

// fetchSlackPhotoURL asks Slack for userID's current profile image (the
// 192px size, already a plain URL — no further resolution needed).
func fetchSlackPhotoURL(ctx context.Context, token, userID string) (string, error) {
	raw, err := slackCall(ctx, token, "users.info", map[string]any{"user": userID})
	if err != nil {
		return "", err
	}
	var r struct {
		User struct {
			Profile struct {
				Image192 string `json:"image_192"`
			} `json:"profile"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return "", nil
	}
	return r.User.Profile.Image192, nil
}

// fetchDiscordPhotoURL asks Discord for userID's current avatar hash and
// turns it into a CDN URL. "" with a nil error means the user has no custom
// avatar (Discord's default), which this card treats the same as "no photo".
func fetchDiscordPhotoURL(ctx context.Context, token, userID string) (string, error) {
	raw, err := discordCall(ctx, token, http.MethodGet, "/users/"+userID, nil)
	if err != nil {
		return "", err
	}
	var r struct {
		Avatar string `json:"avatar"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Avatar == "" {
		return "", nil
	}
	ext := "png"
	if strings.HasPrefix(r.Avatar, "a_") {
		ext = "gif" // an animated avatar hash is prefixed "a_" by Discord
	}
	return discordCDNBase() + "/avatars/" + userID + "/" + r.Avatar + "." + ext + "?size=256", nil
}

// RefreshChannelPhoto fetches and caches one Telegram, Slack or Discord
// sender's profile photo onto kind ("contact" or "conversation") rowID — at
// most once every photocache.RefreshWindow, rate-limited per workspace
// (channelPhotoLimiter), and never louder than a cached failure on error.
// Callers MUST run this in a goroutine with a background context: exactly
// internal/whatsapp.RefreshContactPhoto's contract, generalised to three more
// platforms instead of duplicated per platform.
func RefreshChannelPhoto(ctx context.Context, a *app.App, workspaceID, channel, connID, kind, rowID, externalUserID string) {
	externalUserID = strings.TrimSpace(externalUserID)
	if rowID == "" || externalUserID == "" || connID == "" {
		return
	}
	table := "contacts"
	if kind == "conversation" {
		table = "conversations"
	}

	var fetchedAt sql.NullTime
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT photo_fetched_at FROM `+table+` WHERE workspace_id = $1 AND id = $2`,
		workspaceID, rowID).Scan(&fetchedAt); err != nil {
		return
	}
	if !photocache.IsStale(fetchedAt, photocache.RefreshWindow) {
		return
	}
	if !channelPhotoLimiter.Allow(workspaceID) {
		return
	}

	var photoURL string
	var err error
	switch channel {
	case "telegram":
		if strings.HasPrefix(externalUserID, "-") {
			return // a group/channel chat id, not a private chat with a person
		}
		var token string
		if token, err = telegramToken(ctx, a.SQLDB, workspaceID, connID); err == nil {
			photoURL, err = fetchTelegramPhotoURL(ctx, token, externalUserID)
		}
	case "slack":
		var token string
		if token, err = slackToken(ctx, a, workspaceID, connID); err == nil {
			photoURL, err = fetchSlackPhotoURL(ctx, token, externalUserID)
		}
	case "discord":
		if !isSnowflake(externalUserID) {
			return
		}
		var token string
		if _, token, err = discordConnAndToken(ctx, a, workspaceID, connID); err == nil {
			photoURL, err = fetchDiscordPhotoURL(ctx, token, externalUserID)
		}
	default:
		return
	}

	var dataURI string
	if err == nil && photoURL != "" {
		var raw []byte
		var ct string
		raw, ct, err = photocache.Download(ctx, photoURL, nil)
		if err == nil {
			dataURI, err = photocache.EncodeDataURI(raw, ct)
		}
	}
	_ = photocache.UpdatePhoto(ctx, a.SQLDB, table, workspaceID, rowID, channel, dataURI, err)
}
