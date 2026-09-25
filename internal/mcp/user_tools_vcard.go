package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/vcard"
)

/*
VcardTools (MH-584): import_contacts_preview / import_contacts_commit / export_contacts_vcard.

No import_contacts tool existed before this — MH-562's inbox_import family (see
user_tools_imports.go) imports from a live connection (WhatsApp, email, ...),
never from a pasted/uploaded file, so these are new tools rather than an
extension of that family. They share internal/vcard with the REST routes
(internal/vcard/routes.go) — one parser, one merge engine, one rate limit — so
an agent's import behaves exactly like a member's upload in the web UI.
*/

const (
	descVcardText    = "Raw vCard 2.1/3.0/4.0 text (one BEGIN:VCARD…END:VCARD block per contact, any number of them)"
	descVcardCountry = "ISO 3166-1 alpha-2 country (e.g. \"EG\") to resolve phone numbers with no country code against; omit to use the workspace's default"
	descVcardIDs     = "Contact ids to export; omit to export every contact in the workspace"
)

func VcardTools() []Tool {
	return []Tool{
		{
			Name: "import_contacts_preview",
			Description: "Parse vCard text and classify each card as new, merging into an existing contact (matched on any phone/email/UID), " +
				"or invalid with a reason — without writing anything. Re-run after adjusting default_country to see how many phones would " +
				"otherwise be guessed from the workspace default.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"vcard"}, map[string]any{
				"vcard":           prop("string", descVcardText),
				"default_country": prop("string", descVcardCountry),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !vcard.CheckRateLimit(s.WorkspaceID) {
					return "", errTooManyVcardImports
				}
				m := decodeArgs(raw)
				text, err := required("vcard", str(m, "vcard"))
				if err != nil {
					return "", err
				}
				if len(text) > vcard.MaxFileSize {
					return "", vcard.ErrTooLarge
				}
				country, err := vcardCountry(ctx, a, s.WorkspaceID, str(m, "default_country"))
				if err != nil {
					return "", err
				}
				pv, err := vcard.PreviewImport(ctx, a.SQLDB, s.WorkspaceID, []byte(text), country)
				if err != nil {
					return "", err
				}
				return jsonResult(pv)
			},
		},
		{
			Name: "import_contacts_commit",
			Description: "Import vCard text: create a contact for each new card, merge the rest into their matched contact filling only " +
				"blank fields and appending new phones/emails (never overwriting), and record nothing for an invalid card. Re-committing the " +
				"exact same text creates nothing new — match it against import_contacts_preview first.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"vcard"}, map[string]any{
				"vcard":           prop("string", descVcardText),
				"default_country": prop("string", descVcardCountry),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !vcard.CheckRateLimit(s.WorkspaceID) {
					return "", errTooManyVcardImports
				}
				m := decodeArgs(raw)
				text, err := required("vcard", str(m, "vcard"))
				if err != nil {
					return "", err
				}
				if len(text) > vcard.MaxFileSize {
					return "", vcard.ErrTooLarge
				}
				country, err := vcardCountry(ctx, a, s.WorkspaceID, str(m, "default_country"))
				if err != nil {
					return "", err
				}
				res, err := vcard.CommitImport(ctx, a.SQLDB, s.WorkspaceID, s.UserID, []byte(text), country)
				if err != nil {
					return "", err
				}
				return jsonResult(res)
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

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func vcardCountry(ctx context.Context, a *app.App, wsID, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	var country string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT default_country FROM workspaces WHERE id = $1`, wsID).Scan(&country)
	return country, nil
}
