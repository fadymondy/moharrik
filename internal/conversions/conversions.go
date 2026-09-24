/*
Package conversions sends a workspace's funnel moments to its own GA4 property
through the Measurement Protocol, and gives its embed snippets the GA4 tag with
Consent Mode v2 (MH-449). fadymondy.com does the same for itself
(internal/rest/ga4_events.go); here every workspace brings its own property.

What is sent, and when:

	a lead is created (a website inquiry)     generate_lead
	a lead is marked qualified                qualify_lead
	a lead is converted (contact + deal)      close_convert_lead   (+ value, currency of its deal)
	a deal enters a "won" stage               deal_won             (+ value, currency)

GA4's recommended lead-generation names where one exists, so GA4's own
lead-generation reports pick them up; deal_won is Moharrik's. The owner marks
the ones that count as key events in GA4 (Admin → Events).

How: triggers on leads and deals (internal/db/post/130_ga4_conversions.sql)
write one outbox row per moment — whichever code path made the change, the
Leads page, MCP, GraphQL or a drag on the board — and only while the workspace
has events switched on. The sender here drains the outbox: it is the only
place that talks to Google, it retries what failed for a reason that may pass,
and it records what happened to every event so the settings page can show it.

Who: the visitor's GA client id, read from the _ga cookie by the inquiry
snippet, so the lead lands on the journey that brought them. Without one (no
GA on the site, or analytics consent refused) the event is still counted,
under a pseudonymous id derived from the lead, and marked unattributed.
Nothing personal is ever sent: no name, email, phone, company or message.

Consent Mode: the snippet reads the page's own consent state (the gtag
'consent' commands in its dataLayer) for ad_user_data and ad_personalization,
and the event carries exactly that. Unknown is DENIED.

The API secret is sealed at rest (internal/secret), never returned by the API,
never logged, and kept out of every error: it travels in the request's query
string, and Go's own transport errors quote the URL, so those are rewritten
before anything records them.
*/
package conversions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/secret"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// The events, in funnel order.
const (
	EventGenerateLead = "generate_lead"
	EventQualifyLead  = "qualify_lead"
	EventConvertLead  = "close_convert_lead"
	EventDealWon      = "deal_won"
	// EventTest is what "Send a test event" sends. Never recorded in the outbox.
	EventTest = "moharrik_test"
)

// ConversionEventInfo is one event as the settings page explains it.
type ConversionEventInfo struct {
	Name string `json:"name"`
	When string `json:"when" enum:"lead_created,lead_qualified,lead_converted,deal_won"`
}

// Events lists what is sent, in funnel order.
var Events = []ConversionEventInfo{
	{EventGenerateLead, "lead_created"},
	{EventQualifyLead, "lead_qualified"},
	{EventConvertLead, "lead_converted"},
	{EventDealWon, "deal_won"},
}

var box = secret.New(secret.DomainGA4APISecret, "the GA4 Measurement Protocol API secret", "enter it again")

var (
	measurementID = regexp.MustCompile(`^G-[A-Z0-9]{4,20}$`)
	gaClientID    = regexp.MustCompile(`^\d{1,20}\.\d{1,20}$`)
)

// ValidMeasurementID reports whether s is a GA4 web stream's measurement id.
func ValidMeasurementID(s string) bool { return measurementID.MatchString(s) }

// ValidClientID reports whether s looks like a GA client id ("1234567890.1712345678").
func ValidClientID(s string) bool { return gaClientID.MatchString(s) }

// ConsentValue normalises what the snippet reported for one consent type:
// "granted", "denied", or "" (not known).
func ConsentValue(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "granted":
		return "granted"
	case "denied":
		return "denied"
	}
	return ""
}

// ClientIDFromCookie takes a _ga cookie value ("GA1.1.1234567890.1712345678") or
// a bare client id and returns the client id, or "" when it is neither.
func ClientIDFromCookie(s string) string {
	s = strings.TrimSpace(s)
	if ValidClientID(s) {
		return s
	}
	parts := strings.Split(s, ".")
	if len(parts) >= 4 && strings.HasPrefix(parts[0], "GA") {
		id := parts[len(parts)-2] + "." + parts[len(parts)-1]
		if ValidClientID(id) {
			return id
		}
	}
	return ""
}

// pseudonymousClientID is the client id of a lead or deal nobody knows the
// visitor's id for: stable per subject, so its later events join its first,
// and shaped like a real one. It identifies nobody.
func pseudonymousClientID(subject string) string {
	h := sha256.Sum256([]byte("moharrik/ga4-client/v1:" + subject))
	return fmt.Sprintf("%d.%d", binary.BigEndian.Uint32(h[0:4])%2_000_000_000+100_000_000, binary.BigEndian.Uint32(h[4:8])%2_000_000_000+100_000_000)
}

// ConversionSettings is a workspace's GA4 conversions setup, as the API shows it.
type ConversionSettings struct {
	MeasurementID string `json:"measurement_id" doc:"The GA4 web stream's measurement id (G-…). Empty = not set up."`
	// The secret itself is never returned.
	APISecretSet  bool       `json:"api_secret_set" doc:"Whether a Measurement Protocol API secret is stored. The secret is never returned."`
	EventsEnabled bool       `json:"events_enabled" doc:"Send the funnel events to GA4."`
	TagInEmbeds   bool       `json:"tag_in_embeds" doc:"The subscribe and inquiry embed snippets include the GA4 tag with Consent Mode v2."`
	DebugMode     bool       `json:"debug_mode" doc:"Mark events for GA4 DebugView while checking the setup."`
	UpdatedAt     *time.Time `json:"updated_at"`
}

// stored is the row, with the sealed secret.
type stored struct {
	ConversionSettings
	sealed string
}

// Load reads the workspace's settings on the app pool (ctx carries the
// workspace). A workspace that never saved any gets the zero ConversionSettings.
func Load(ctx context.Context, db *sql.DB, workspaceID string) (ConversionSettings, error) {
	s, err := load(ctx, db, workspaceID)
	return s.ConversionSettings, err
}

func load(ctx context.Context, db *sql.DB, workspaceID string) (stored, error) {
	var s stored
	var updated time.Time
	err := db.QueryRowContext(ctx, `
		SELECT measurement_id, api_secret_sealed, events_enabled, tag_in_embeds, debug_mode, updated_at
		FROM ga4_conversion_settings WHERE workspace_id = $1`, workspaceID,
	).Scan(&s.MeasurementID, &s.sealed, &s.EventsEnabled, &s.TagInEmbeds, &s.DebugMode, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return stored{}, nil
	}
	if err != nil {
		return stored{}, err
	}
	s.APISecretSet = s.sealed != ""
	s.UpdatedAt = &updated
	return s, nil
}

// Change is a settings update. A nil field is left as it is.
type Change struct {
	MeasurementID *string
	// APISecret: nil keeps the stored secret, "" removes it, anything else replaces it.
	APISecret     *string
	EventsEnabled *bool
	TagInEmbeds   *bool
	DebugMode     *bool
}

// Setup errors, in words the settings page shows as they are.
var (
	ErrMeasurementID = errors.New("the measurement id is a GA4 web stream's, like G-ABC123XYZ (Admin → Data streams)")
	ErrAPISecret     = errors.New("the API secret is the value GA4 shows under Measurement Protocol API secrets, without spaces")
	ErrNeedsBoth     = errors.New("sending events needs both the measurement id and the API secret")
	ErrNeedsID       = errors.New("the tag needs the measurement id")
)

// Save applies a change and returns the settings as stored.
func Save(ctx context.Context, db *sql.DB, workspaceID, userID string, c Change) (ConversionSettings, error) {
	cur, err := load(ctx, db, workspaceID)
	if err != nil {
		return ConversionSettings{}, err
	}
	next := cur
	if c.MeasurementID != nil {
		id := strings.ToUpper(strings.TrimSpace(*c.MeasurementID))
		if id != "" && !ValidMeasurementID(id) {
			return ConversionSettings{}, ErrMeasurementID
		}
		next.MeasurementID = id
	}
	if c.APISecret != nil {
		plain := strings.TrimSpace(*c.APISecret)
		if plain != "" && (len(plain) > 200 || strings.ContainsAny(plain, " \t\r\n")) {
			return ConversionSettings{}, ErrAPISecret
		}
		sealed, err := box.Seal(plain)
		if err != nil {
			return ConversionSettings{}, err
		}
		next.sealed = sealed
	}
	if c.EventsEnabled != nil {
		next.EventsEnabled = *c.EventsEnabled
	}
	if c.TagInEmbeds != nil {
		next.TagInEmbeds = *c.TagInEmbeds
	}
	if c.DebugMode != nil {
		next.DebugMode = *c.DebugMode
	}
	// Switched on only when it can work: a toggle that is on and does nothing
	// is exactly the dead control the settings page must not have.
	if next.EventsEnabled && (next.MeasurementID == "" || next.sealed == "") {
		return ConversionSettings{}, ErrNeedsBoth
	}
	if next.TagInEmbeds && next.MeasurementID == "" {
		return ConversionSettings{}, ErrNeedsID
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO ga4_conversion_settings (workspace_id, measurement_id, api_secret_sealed, events_enabled, tag_in_embeds, debug_mode, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id) DO UPDATE SET measurement_id = EXCLUDED.measurement_id,
			api_secret_sealed = EXCLUDED.api_secret_sealed, events_enabled = EXCLUDED.events_enabled,
			tag_in_embeds = EXCLUDED.tag_in_embeds, debug_mode = EXCLUDED.debug_mode,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		workspaceID, next.MeasurementID, next.sealed, next.EventsEnabled, next.TagInEmbeds, next.DebugMode, nullable(userID),
	); err != nil {
		return ConversionSettings{}, err
	}
	return Load(ctx, db, workspaceID)
}

// TagID is the measurement id the workspace's embed snippets carry the GA4 tag
// for, or "" when they carry none. Read on the app pool; ctx carries the
// workspace. A failure reads as "no tag": a snippet without the tag still works.
func TagID(ctx context.Context, a *app.App, workspaceID string) string {
	if a == nil || a.SQLDB == nil || workspaceID == "" {
		return ""
	}
	s, err := Load(tenant.WithWorkspace(ctx, workspaceID), a.SQLDB, workspaceID)
	if err != nil || !s.TagInEmbeds || !ValidMeasurementID(s.MeasurementID) {
		return ""
	}
	return s.MeasurementID
}

// TagSnippet is the GA4 tag with Consent Mode v2, for the top of an embed.
//
// Everything starts DENIED: nothing is stored in the visitor's browser and no
// ads signal is sent until the site's own cookie banner grants it with
// gtag('consent', 'update', …). A page that already runs gtag keeps its own
// consent defaults and loader, and only gains this property's config.
func TagSnippet(measurementID string) string {
	if !ValidMeasurementID(measurementID) {
		return ""
	}
	return `<!-- GA4 (` + measurementID + `) with Consent Mode v2: nothing is stored until your cookie banner calls
     gtag('consent', 'update', { analytics_storage: 'granted', ... }). -->
<script>
(function () {
  window.dataLayer = window.dataLayer || [];
  if (typeof window.gtag !== "function") {
    window.gtag = function () { window.dataLayer.push(arguments); };
    gtag("consent", "default", {
      ad_storage: "denied", ad_user_data: "denied", ad_personalization: "denied",
      analytics_storage: "denied", wait_for_update: 500
    });
    gtag("js", new Date());
    var s = document.createElement("script");
    s.async = true;
    s.src = "https://www.googletagmanager.com/gtag/js?id=` + measurementID + `";
    document.head.appendChild(s);
  }
  gtag("config", "` + measurementID + `");
})();
</script>
`
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
