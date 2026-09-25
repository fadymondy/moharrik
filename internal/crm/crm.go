// Package crm is Moharrik's CRM: companies, the contacts they employ (the
// `contacts` table the email-marketing module owns, extended with CRM columns),
// a per-workspace pipeline of stages, deals on it, and an activity timeline of
// notes, calls, meetings and tasks.
//
// Same rules as internal/marketing: every handler runs on the app (RLS) pool AND
// names workspace_id in every predicate; links between rows are composite
// (workspace_id, id) foreign keys, so a row can only ever point inside its own
// workspace (internal/db/schema/crm.sql).
//
// Companies, deals and activities are read/written under contacts:read and
// contacts:write (members); editing the pipeline's stages is pipeline:manage
// (managers and up).
package crm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/icp"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Activity kinds.
var activityKinds = map[string]bool{"note": true, "call": true, "meeting": true, "task": true}

// Company sizes (the CHECK in crm.sql).
var companySizes = map[string]bool{"": true, "1-10": true, "11-50": true, "51-200": true, "201-1000": true, "1000+": true}

// defaultStages seed a workspace's pipeline the first time it is read.
var defaultStages = []struct{ en, ar, kind string }{
	{"Lead", "عميل محتمل", "open"},
	{"Qualified", "مؤهَّل", "open"},
	{"Proposal", "عرض سعر", "open"},
	{"Won", "تم الفوز", "won"},
	{"Lost", "خسارة", "lost"},
}

// ---- wire types ----------------------------------------------------------

type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ContactRef struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

type CompanyOut struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Domain       string    `json:"domain"`
	Industry     string    `json:"industry"`
	Size         string    `json:"size"`
	Country      string    `json:"country" doc:"Where the company is; the ideal-customer profile's regions are matched against it."`
	Notes        string    `json:"notes"`
	OwnerUserID  *string   `json:"owner_user_id"`
	ContactCount int64     `json:"contact_count"`
	OpenDeals    int64     `json:"open_deals"`
	CreatedAt    time.Time `json:"created_at"`
	// The ideal-customer score (MH-395) and what it was made of.
	Score        *int         `json:"score" doc:"0-100 against the workspace's ideal-customer profile; null when the profile weighs nothing."`
	ScoreReasons []icp.Reason `json:"score_reasons"`
}

type StageOut struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Position   int64  `json:"position"`
	Kind       string `json:"kind"`
	DealCount  int64  `json:"deal_count"`
	ValueMinor int64  `json:"value_minor"`
	// Board view only: the stage has more deals than were sent; pass this as
	// cursor with stage_id for the next ones.
	NextCursor string `json:"next_cursor,omitempty"`
}

// ErrNoSuchStage is a stage_id this workspace's pipeline does not have.
var ErrNoSuchStage = errors.New("stage not found")

type DealOut struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	ValueMinor    int64       `json:"value_minor"`
	Currency      string      `json:"currency"`
	StageID       string      `json:"stage_id"`
	StageName     string      `json:"stage_name"`
	StageKind     string      `json:"stage_kind"`
	Position      float64     `json:"position"`
	Contact       *ContactRef `json:"contact"`
	Company       *Ref        `json:"company"`
	OwnerUserID   *string     `json:"owner_user_id"`
	ExpectedClose *string     `json:"expected_close"`
	ClosedAt      *time.Time  `json:"closed_at"`
	CreatedAt     time.Time   `json:"created_at"`
}

type ActivityOut struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"`
	Body      string      `json:"body"`
	DueAt     *time.Time  `json:"due_at"`
	DoneAt    *time.Time  `json:"done_at"`
	Contact   *ContactRef `json:"contact"`
	Company   *Ref        `json:"company"`
	Deal      *Ref        `json:"deal"`
	CreatedBy string      `json:"created_by"`
	CreatedAt time.Time   `json:"created_at"`
}

type ListSubscription struct {
	ID       string `json:"id"`
	ListID   string `json:"list_id"`
	ListName string `json:"list_name"`
	Status   string `json:"status"`
	// The same three the contacts list carries (marketing.SubscriptionOut,
	// MH-408), so one type serves both pages.
	ConfirmedAt    *time.Time `json:"confirmed_at"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at"`
	ByWorkspace    bool       `json:"unsubscribed_by_workspace"`
}

type ContactDetail struct {
	ID            string             `json:"id"`
	Email         string             `json:"email"`
	Name          string             `json:"name"`
	Locale        string             `json:"locale"`
	Source        string             `json:"source"`
	Phone         string             `json:"phone"`
	Handle        string             `json:"handle"`
	Title         string             `json:"title"`
	Tags          []string           `json:"tags"`
	Company       *Ref               `json:"company"`
	OwnerUserID   *string            `json:"owner_user_id"`
	CreatedAt     time.Time          `json:"created_at"`
	Subscriptions []ListSubscription `json:"subscriptions"`
	// MH-395: where the contact is in the lifecycle, and its ideal-customer score.
	Lifecycle             string               `json:"lifecycle" enum:",subscriber,lead,qualified,customer" doc:"Empty until an event or a member sets it."`
	LifecycleAt           *time.Time           `json:"lifecycle_at"`
	LifecycleHistory      []icp.LifecycleEvent `json:"lifecycle_history" doc:"Newest first, at most 100; lifecycle_history_total says how many in all."`
	LifecycleHistoryTotal int64                `json:"lifecycle_history_total"`
	Score                 *int                 `json:"score"`
	ScoreReasons          []icp.Reason         `json:"score_reasons"`
	// MH-583: Google-People-parity fields. Meta is the free key/value list
	// Fady asked for by name ("add a meta value that i can add key and value
	// of each contact"); Identities is every linked account on every channel
	// (email, phone, whatsapp, telegram, ...), including the ones already
	// denormalised onto Email/Phone/Handle above.
	GivenName          string            `json:"given_name"`
	FamilyName         string            `json:"family_name"`
	MiddleName         string            `json:"middle_name"`
	NamePrefix         string            `json:"name_prefix"`
	NameSuffix         string            `json:"name_suffix"`
	PhoneticGivenName  string            `json:"phonetic_given_name"`
	PhoneticFamilyName string            `json:"phonetic_family_name"`
	Nickname           string            `json:"nickname"`
	Birthday           *string           `json:"birthday" doc:"YYYY-MM-DD"`
	Biography          string            `json:"biography"`
	PhotoURL           string            `json:"photo_url"`
	// AvatarURL is what the UI should actually render (MH-602): an uploaded
	// photo first, then a cached provider photo (PhotoURL, already a
	// downloaded data: URI or a signed local URL — never the two are the
	// same value by coincidence), then empty so the client falls back to
	// initials. See AvatarURL() in avatar.go for the priority.
	AvatarURL          string            `json:"avatar_url"`
	Meta               map[string]string `json:"meta" doc:"Free-form key/value pairs on this contact."`
	Identities         []ContactIdentity `json:"identities" doc:"Every linked account: email, phone, whatsapp, telegram, slack, discord, messenger, instagram, linkedin, x, widget, sip, other."`
}

// ContactIdentity is one linked account (internal/contacts.Identity on the wire).
type ContactIdentity struct {
	ID         string `json:"id"`
	Channel    string `json:"channel" enum:"email,phone,whatsapp,handle,telegram,slack,discord,messenger,instagram,linkedin,x,widget,sip,other"`
	ExternalID string `json:"external_id"`
	Label      string `json:"label"`
	IsPrimary  bool   `json:"is_primary"`
	Position   int    `json:"position"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// ---- helpers ------------------------------------------------------------

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// linkErr maps a composite-FK violation (a link to a row that is not in this
// workspace, or does not exist) to a 422 instead of a 500.
func linkErr(err error, fallback string) error {
	switch pgCode(err) {
	case "23503":
		return huma.Error422UnprocessableEntity("a linked contact, company, deal or stage was not found in this workspace")
	case "23505":
		return huma.Error409Conflict("that already exists in this workspace")
	case "23514":
		if constraintName(err) == "contacts_identity_chk" {
			return huma.Error422UnprocessableEntity("a contact needs at least an email, a phone number or a handle")
		}
		return huma.Error422UnprocessableEntity("a value is out of range")
	}
	return huma.Error500InternalServerError(fallback)
}

func constraintName(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.ConstraintName
	}
	return ""
}

func strp(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func timep(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

func datep(n sql.NullTime) *string {
	if !n.Valid {
		return nil
	}
	v := n.Time.Format("2006-01-02")
	return &v
}

// nullable turns "" into SQL NULL (the wire convention for "unlink").
func nullable(s string) any {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return s
}

// normDomain keeps the registrable host: no scheme, path, port or www.
// (fadymondy.com's radar dedupes companies on the same canonical domain.)
func normDomain(raw string) string {
	d := strings.ToLower(strings.TrimSpace(raw))
	if d == "" {
		return ""
	}
	if !strings.Contains(d, "://") {
		d = "http://" + d
	}
	if u, err := url.Parse(d); err == nil && u.Hostname() != "" {
		d = u.Hostname()
	}
	return strings.TrimPrefix(d, "www.")
}

func normTags(in []string) []string {
	seen := map[string]bool{}
	tags := []string{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || len([]rune(t)) > 40 || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		tags = append(tags, t)
		if len(tags) == 20 {
			break
		}
	}
	return tags
}

func tagsJSON(tags []string) string {
	b, _ := json.Marshal(tags)
	return string(b)
}

// isMember reports whether userID is an active member of the workspace — the
// only people a record may be assigned to.
func isMember(ctx context.Context, db *sql.DB, wsID, userID string) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspace_members
		WHERE workspace_id = $1 AND user_id = $2 AND status = 'active')`, wsID, userID).Scan(&ok)
	return ok, err
}

// checkOwner validates an owner field: nil = unchanged, "" = unassign.
func checkOwner(ctx context.Context, db *sql.DB, wsID string, owner *string) error {
	if owner == nil || strings.TrimSpace(*owner) == "" {
		return nil
	}
	ok, err := isMember(ctx, db, wsID, strings.TrimSpace(*owner))
	if err != nil {
		return huma.Error500InternalServerError("could not check the owner")
	}
	if !ok {
		return huma.Error422UnprocessableEntity("the owner must be a member of this workspace")
	}
	return nil
}

func parseDate(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("dates are YYYY-MM-DD")
	}
	return t.Format("2006-01-02"), nil
}

func parseTime(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return nil, huma.Error422UnprocessableEntity("times are RFC 3339 or YYYY-MM-DD")
}

// update collects SET clauses for a PATCH. Args start after the two fixed
// predicates ($1 = workspace_id, $2 = id).
type update struct {
	sets []string
	args []any
}

func (u *update) set(col string, v any) {
	u.args = append(u.args, v)
	u.sets = append(u.sets, col+" = $"+strconv.Itoa(len(u.args)+2))
}
