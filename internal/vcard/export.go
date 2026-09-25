package vcard

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// exportRow is what one contact needs to render as a vCard 3.0 block.
type exportRow struct {
	name, givenName, familyName, middleName, prefix, suffix, nickname string
	email, phone, title, jobRole, biography, photoURL, companyName    string
	birthday, anniversary                                             string
	tags                                                              []string
	addressesJSON, urlsJSON                                           string
	extraPhones, extraEmails                                          []string
}

// ExportVCard renders ids (or, if ids is empty, every contact in the
// workspace) as one vCard 3.0 text blob — the version every reader (Google,
// iPhone, Outlook, Android) accepts, and the same version PreviewImport's
// own fixtures round-trip against.
func ExportVCard(ctx context.Context, db *sql.DB, wsID string, ids []string) (string, error) {
	query := `
		SELECT c.id, c.name, c.given_name, c.family_name, c.middle_name, c.name_prefix, c.name_suffix,
		       c.nickname, c.email, c.phone, c.title, c.job_role, c.biography, c.photo_url,
		       COALESCE(co.name, ''), COALESCE(c.birthday::text, ''), COALESCE(c.anniversary::text, ''),
		       c.tags, c.addresses::text, c.urls::text
		FROM contacts c
		LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id
		WHERE c.workspace_id = $1`
	args := []any{wsID}
	if len(ids) > 0 {
		query += ` AND c.id = ANY($2::text[])`
		args = append(args, ids)
	}
	query += ` ORDER BY c.name, c.id`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var out strings.Builder
	for rows.Next() {
		var id string
		var r exportRow
		if err := rows.Scan(&id, &r.name, &r.givenName, &r.familyName, &r.middleName, &r.prefix, &r.suffix,
			&r.nickname, &r.email, &r.phone, &r.title, &r.jobRole, &r.biography, &r.photoURL,
			&r.companyName, &r.birthday, &r.anniversary, arrayScanner{&r.tags}, &r.addressesJSON, &r.urlsJSON); err != nil {
			return "", err
		}
		extraPhones, extraEmails, err := extraIdentities(ctx, db, wsID, id, r.phone, r.email)
		if err != nil {
			return "", err
		}
		r.extraPhones, r.extraEmails = extraPhones, extraEmails
		writeCard(&out, r)
	}
	return out.String(), rows.Err()
}

// arrayScanner adapts a *[]string to sql.Scanner for pgx's database/sql text[] decoding.
type arrayScanner struct{ dst *[]string }

func (a arrayScanner) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a.dst = nil
	case []byte:
		*a.dst = parsePGTextArray(string(v))
	case string:
		*a.dst = parsePGTextArray(v)
	}
	return nil
}

// parsePGTextArray parses Postgres's {a,b,"c d"} text[] wire format. Good
// enough for tag values, which never contain a literal comma (CATEGORIES
// splits on comma on the way in).
func parsePGTextArray(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(p, `"`))
	}
	return out
}

func extraIdentities(ctx context.Context, db *sql.DB, wsID, contactID, primaryPhone, primaryEmail string) ([]string, []string, error) {
	rows, err := db.QueryContext(ctx, `SELECT channel, external_id FROM contact_identities WHERE workspace_id=$1 AND contact_id=$2 AND channel IN ('phone','email','whatsapp')`, wsID, contactID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var phones, emails []string
	for rows.Next() {
		var ch, ext string
		if err := rows.Scan(&ch, &ext); err != nil {
			return nil, nil, err
		}
		switch ch {
		case "phone", "whatsapp":
			if ext != primaryPhone {
				phones = append(phones, ext)
			}
		case "email":
			if !strings.EqualFold(ext, primaryEmail) {
				emails = append(emails, ext)
			}
		}
	}
	return phones, emails, rows.Err()
}

func esc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `,`, `\,`, `;`, `\;`, "\n", `\n`)
	return r.Replace(s)
}

func fold(out *strings.Builder, line string) {
	// RFC 6350 folding at 75 octets is a nicety, not a requirement most
	// readers enforce; every fixture and every real address book this
	// package has seen accepts an unfolded line, so lines are written as-is
	// for simplicity and to keep export trivially round-trip safe with
	// ParseCards (which only unfolds space/tab continuations — it would
	// never see one we did not create).
	out.WriteString(line)
	out.WriteString("\r\n")
}

func writeCard(out *strings.Builder, r exportRow) {
	fold(out, "BEGIN:VCARD")
	fold(out, "VERSION:3.0")
	if r.name != "" {
		fold(out, "FN:"+esc(r.name))
	} else {
		fold(out, "FN:"+esc(strings.TrimSpace(r.givenName+" "+r.familyName)))
	}
	fold(out, "N:"+esc(r.familyName)+";"+esc(r.givenName)+";"+esc(r.middleName)+";"+esc(r.prefix)+";"+esc(r.suffix))
	if r.nickname != "" {
		fold(out, "NICKNAME:"+esc(r.nickname))
	}
	if r.phone != "" {
		fold(out, "TEL;TYPE=CELL,PREF:"+esc(r.phone))
	}
	for _, p := range r.extraPhones {
		fold(out, "TEL;TYPE=HOME:"+esc(p))
	}
	if r.email != "" {
		fold(out, "EMAIL;TYPE=INTERNET,PREF:"+esc(r.email))
	}
	for _, e := range r.extraEmails {
		fold(out, "EMAIL;TYPE=INTERNET:"+esc(e))
	}
	if r.companyName != "" {
		fold(out, "ORG:"+esc(r.companyName))
	}
	if r.title != "" {
		fold(out, "TITLE:"+esc(r.title))
	}
	if r.jobRole != "" {
		fold(out, "ROLE:"+esc(r.jobRole))
	}
	if r.birthday != "" {
		fold(out, "BDAY:"+r.birthday)
	}
	if r.anniversary != "" {
		fold(out, "ANNIVERSARY:"+r.anniversary)
	}
	if r.biography != "" {
		fold(out, "NOTE:"+esc(r.biography))
	}
	if r.photoURL != "" {
		if strings.HasPrefix(r.photoURL, "data:") {
			writeDataURIPhoto(out, r.photoURL)
		} else {
			fold(out, "PHOTO;VALUE=uri:"+r.photoURL)
		}
	}
	for _, t := range r.tags {
		if t != "" {
			fold(out, "CATEGORIES:"+esc(t))
		}
	}
	for _, a := range decodeAddressesJSON(r.addressesJSON) {
		fold(out, "ADR"+adrTypeParam(a.Type)+":;;"+esc(a.Street)+";"+esc(a.Locality)+";"+esc(a.Region)+";"+esc(a.Postal)+";"+esc(a.Country))
	}
	for _, u := range decodeStringArrayJSON(r.urlsJSON) {
		fold(out, "URL:"+u)
	}
	fold(out, "END:VCARD")
}

func adrTypeParam(t string) string {
	if t == "" {
		return ""
	}
	return ";TYPE=" + strings.ToUpper(t)
}

func writeDataURIPhoto(out *strings.Builder, dataURI string) {
	i := strings.Index(dataURI, ",")
	if i < 0 {
		return
	}
	meta, b64 := dataURI[:i], dataURI[i+1:]
	typ := "JPEG"
	switch {
	case strings.Contains(meta, "png"):
		typ = "PNG"
	case strings.Contains(meta, "gif"):
		typ = "GIF"
	}
	fold(out, "PHOTO;ENCODING=b;TYPE="+typ+":"+b64)
}

type jsonAddr struct {
	Type     string `json:"type"`
	Street   string `json:"street"`
	Locality string `json:"locality"`
	Region   string `json:"region"`
	Postal   string `json:"postal_code"`
	Country  string `json:"country"`
	Label    string `json:"label"`
}

func decodeAddressesJSON(s string) []jsonAddr {
	if s == "" {
		return nil
	}
	var out []jsonAddr
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func decodeStringArrayJSON(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}
