package vcard

import (
	"context"
	"database/sql"
	"encoding/json"
)

/*
MH-646: a phone kept raw in meta (phone_raw/phones_raw, see classify in
import.go) because no country was resolvable at import/sync time is not
lost — once the workspace has a default_country (set explicitly or inferred
from other contacts), ReparseRawPhones re-attempts ToE164 for every contact
still carrying a raw phone and fills it in. It never overwrites a phone a
member already set (contacts.phone must still be blank) and never touches a
contact with no raw phone to begin with, so it is cheap to run after every
country change: one SELECT plus one UPDATE per contact that now resolves.
*/

// ReparseRawPhones re-parses every raw phone kept on wsID's contacts
// (contacts.meta->>'phone_raw' / 'phones_raw') against country, filling
// contacts.phone (only while still blank) and adding any further numbers as
// phone contact_identities. Returns how many contacts got their phone filled
// in. country empty is a no-op (nothing to resolve against yet).
func ReparseRawPhones(ctx context.Context, db *sql.DB, wsID, country string) (resolved int, err error) {
	if country == "" {
		return 0, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, meta->>'phone_raw', coalesce(meta->>'phones_raw', '')
		FROM contacts
		WHERE workspace_id = $1 AND phone = '' AND meta ? 'phone_raw'`, wsID)
	if err != nil {
		return 0, err
	}
	type candidate struct{ id, raw, rawList string }
	var list []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.raw, &c.rawList); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, c := range list {
		e164, _, cerr := ToE164(c.raw, country)
		if cerr != nil || e164 == "" {
			continue
		}
		res, uerr := db.ExecContext(ctx, `
			UPDATE contacts SET phone = $3, meta = meta - 'phone_raw', updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND phone = ''`, wsID, c.id, e164)
		if uerr != nil {
			return resolved, uerr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			resolved++
		}

		if c.rawList == "" {
			continue
		}
		var extras []string
		if json.Unmarshal([]byte(c.rawList), &extras) != nil {
			continue
		}
		for _, raw := range extras {
			extraE164, _, xerr := ToE164(raw, country)
			if xerr != nil || extraE164 == "" {
				continue
			}
			if _, uerr := db.ExecContext(ctx, `
				INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id)
				VALUES ($1, $2, 'phone', $3)
				ON CONFLICT (workspace_id, channel, external_id) DO NOTHING`, wsID, c.id, extraE164); uerr != nil {
				return resolved, uerr
			}
		}
		if _, uerr := db.ExecContext(ctx, `UPDATE contacts SET meta = meta - 'phones_raw', updated_at = now() WHERE workspace_id = $1 AND id = $2`, wsID, c.id); uerr != nil {
			return resolved, uerr
		}
	}
	return resolved, nil
}
