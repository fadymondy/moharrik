package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/googlecontacts"
	"github.com/fadymondy/moharrik/internal/vcard"
)

/*
VcardTools (MH-584, extended MH-585): import_contacts_preview / import_contacts_commit / export_contacts_vcard.

No import_contacts tool existed before this — MH-562's inbox_import family (see
user_tools_imports.go) imports from a live connection (WhatsApp, email, ...),
never from a pasted/uploaded file, so these are new tools rather than an
extension of that family. They share internal/vcard with the REST routes
(internal/vcard/routes.go) — one parser, one merge engine, one rate limit — so
an agent's import behaves exactly like a member's upload in the web UI.

MH-585 adds a `source` argument to both tools: "vcard" (default, unchanged),
"google_csv" (a Google Contacts CSV export, parsed by internal/vcard's
ParseGoogleCSV, then fed through the exact same PreviewCards/CommitCards as
a vCard file), and "google" (a connected Google account, fetched live via
internal/googlecontacts — preview classifies without writing, commit runs a
full sync). Every source ends at the same classify/match/merge engine.
*/

type importSource string

const (
	sourceVcard     importSource = "vcard"
	sourceGoogleCSV importSource = "google_csv"
	sourceGoogle    importSource = "google"
)

const (
	descVcardText     = "Raw vCard 2.1/3.0/4.0 text (one BEGIN:VCARD…END:VCARD block per contact, any number of them). Required when source is \"vcard\" (the default)."
	descVcardCountry  = "ISO 3166-1 alpha-2 country (e.g. \"EG\") to resolve phone numbers with no country code against; omit to use the workspace's default"
	descVcardIDs      = "Contact ids to export; omit to export every contact in the workspace"
	descContactSource = "Where the cards come from: \"vcard\" (default, pasted vCard text), \"google_csv\" (a Google Contacts CSV export, no OAuth), " +
		"or \"google\" (a connected Google account — fetches live via the People API; commit here runs a full sync, same as google_contacts_sync)."
	descGoogleCSV    = "Raw Google Contacts CSV export text (the \"Google CSV\" option under contacts.google.com → Export). Required when source is \"google_csv\"."
	descGoogleConnID = "A google_connections id from w-google-connections, with Contacts access granted. Required when source is \"google\"."
	// descFullResync (MH-646) is shared by google_contacts_sync and
	// import_contacts_commit{source:"google"} — the exact same "full"
	// argument on the exact same underlying Sync call — so one Arabic
	// translation covers both (see docs_vcard.go/argsARVcard).
	descFullResync = "Ignore the stored sync token and re-import every contact (MH-646: fills in phones a newly-set default country can now resolve). Never duplicates or overwrites a hand-set phone."
)

func VcardTools() []Tool {
	return []Tool{
		{
			Name: "import_contacts_preview",
			Description: "Classify each contact card as new, merging into an existing contact (matched on any phone/email/UID), " +
				"or invalid with a reason — without writing anything. Source defaults to pasted vCard text; set source to \"google_csv\" " +
				"or \"google\" to preview a Google export/connection through the exact same classification instead. Re-run after " +
				"adjusting default_country to see how many phones would otherwise be guessed from the workspace default.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"source":          prop("string", descContactSource),
				"vcard":           prop("string", descVcardText),
				"csv":             prop("string", descGoogleCSV),
				"connection_id":   prop("string", descGoogleConnID),
				"default_country": prop("string", descVcardCountry),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				switch importSource(str(m, "source")) {
				case sourceGoogle:
					pv, err := googlecontacts.Preview(ctx, a, s.WorkspaceID, str(m, "connection_id"))
					if err != nil {
						return "", err
					}
					return jsonResult(pv)
				case sourceGoogleCSV:
					if !vcard.CheckRateLimit(s.WorkspaceID) {
						return "", errTooManyVcardImports
					}
					cards, perrs, hash, err := parseGoogleCSVArg(m)
					if err != nil {
						return "", err
					}
					country, err := vcard.ResolveCountry(ctx, a.SQLDB, s.WorkspaceID, str(m, "default_country"))
					if err != nil {
						return "", err
					}
					pv, err := vcard.PreviewCards(ctx, a.SQLDB, s.WorkspaceID, cards, perrs, hash, country)
					if err != nil {
						return "", err
					}
					return jsonResult(pv)
				default:
					if !vcard.CheckRateLimit(s.WorkspaceID) {
						return "", errTooManyVcardImports
					}
					text, err := required("vcard", str(m, "vcard"))
					if err != nil {
						return "", err
					}
					if len(text) > vcard.MaxFileSize {
						return "", vcard.ErrTooLarge
					}
					country, err := vcard.ResolveCountry(ctx, a.SQLDB, s.WorkspaceID, str(m, "default_country"))
					if err != nil {
						return "", err
					}
					pv, err := vcard.PreviewImport(ctx, a.SQLDB, s.WorkspaceID, []byte(text), country)
					if err != nil {
						return "", err
					}
					return jsonResult(pv)
				}
			},
		},
		{
			Name: "import_contacts_commit",
			Description: "Import contact cards: create a contact for each new card, merge the rest into their matched contact filling only " +
				"blank fields and appending new phones/emails (never overwriting), and record nothing for an invalid card. Source defaults " +
				"to pasted vCard text; \"google_csv\" commits a parsed Google export, and \"google\" runs a full People API sync for that " +
				"connection (same as google_contacts_sync) through this same flow. Re-committing the exact same vCard/CSV text creates " +
				"nothing new — match it against import_contacts_preview first.",
			Scope: "contacts:write",
			InputSchema: schema(nil, map[string]any{
				"source":          prop("string", descContactSource),
				"vcard":           prop("string", descVcardText),
				"csv":             prop("string", descGoogleCSV),
				"connection_id":   prop("string", descGoogleConnID),
				"default_country": prop("string", descVcardCountry),
				"full": prop("boolean", descFullResync),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				switch importSource(str(m, "source")) {
				case sourceGoogle:
					full, _ := m["full"].(bool)
					res, err := googlecontacts.Sync(ctx, a, s.WorkspaceID, str(m, "connection_id"), s.UserID, full)
					if err != nil {
						return "", err
					}
					return jsonResult(res)
				case sourceGoogleCSV:
					if !vcard.CheckRateLimit(s.WorkspaceID) {
						return "", errTooManyVcardImports
					}
					cards, perrs, hash, err := parseGoogleCSVArg(m)
					if err != nil {
						return "", err
					}
					country, err := vcard.ResolveCountry(ctx, a.SQLDB, s.WorkspaceID, str(m, "default_country"))
					if err != nil {
						return "", err
					}
					res, err := vcard.CommitCards(ctx, a.SQLDB, s.WorkspaceID, s.UserID, cards, perrs, hash, country)
					if err != nil {
						return "", err
					}
					return jsonResult(res)
				default:
					if !vcard.CheckRateLimit(s.WorkspaceID) {
						return "", errTooManyVcardImports
					}
					text, err := required("vcard", str(m, "vcard"))
					if err != nil {
						return "", err
					}
					if len(text) > vcard.MaxFileSize {
						return "", vcard.ErrTooLarge
					}
					country, err := vcard.ResolveCountry(ctx, a.SQLDB, s.WorkspaceID, str(m, "default_country"))
					if err != nil {
						return "", err
					}
					res, err := vcard.CommitImport(ctx, a.SQLDB, s.WorkspaceID, s.UserID, []byte(text), country)
					if err != nil {
						return "", err
					}
					return jsonResult(res)
				}
			},
		},
		{
			Name:        "export_contacts_vcard",
			Description: "Export one contact, a selection, or every contact in the workspace as vCard 3.0 text, ready to hand to any address book.",
			Scope:       "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": descVcardIDs},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					IDs []string `json:"ids"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				text, err := vcard.ExportVCard(ctx, a.SQLDB, s.WorkspaceID, in.IDs)
				if err != nil {
					return "", err
				}
				return text, nil
			},
		},
	}
}

var errTooManyVcardImports = &toolError{"too many vCard imports for this workspace in the last minute — try again shortly"}
var errMissingGoogleCSV = &toolError{`"csv" is required when source is "google_csv"`}

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

// parseGoogleCSVArg reads the "csv" argument and parses it with
// vcard.ParseGoogleCSV, returning a run identifier (not a real file hash —
// PreviewCards/CommitCards only use it for vcard_imports bookkeeping) that
// stays stable for the same bytes, same as the vCard path's fileHash.
func parseGoogleCSVArg(m map[string]any) (cards []vcard.Card, perrs []vcard.ParseError, hash string, err error) {
	text := str(m, "csv")
	if text == "" {
		return nil, nil, "", errMissingGoogleCSV
	}
	if len(text) > vcard.MaxFileSize {
		return nil, nil, "", vcard.ErrTooLarge
	}
	cards, perrs = vcard.ParseGoogleCSV([]byte(text))
	sum := sha256.Sum256([]byte(text))
	return cards, perrs, hex.EncodeToString(sum[:]), nil
}
