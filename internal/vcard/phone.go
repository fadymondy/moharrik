package vcard

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/fadymondy/moharrik/internal/marketing"
)

/*
MH-584: a vCard TEL rarely carries a country code the way WhatsApp/CRM numbers
already do here (internal/marketing.NormalizePhone, which only checks digit
count — it has no idea whether "01001234567" is missing its country code or
not). Android/iPhone/Outlook address books export local numbers as dialled,
country code and all quirks included, so this file adds exactly the one thing
NormalizePhone does not: turning a LOCAL number into E.164 using the
workspace's default country, then handing the result to NormalizePhone for
the validation every other phone in this app already goes through.
*/

// dialCodes is deliberately not exhaustive — it covers the workspaces this
// app is actually used by today plus their common trading partners. A
// country missing from this map simply cannot be used as a default_country;
// ToE164 returns an error naming it, rather than silently guessing.
var dialCodes = map[string]string{
	"EG": "20", "SA": "966", "AE": "971", "KW": "965", "QA": "974", "BH": "973", "OM": "968",
	"JO": "962", "LB": "961", "SY": "963", "IQ": "964", "YE": "967", "PS": "970", "LY": "218",
	"TN": "216", "DZ": "213", "MA": "212", "SD": "249",
	"US": "1", "CA": "1", "GB": "44", "IE": "353", "FR": "33", "DE": "49", "IT": "39", "ES": "34",
	"PT": "351", "NL": "31", "BE": "32", "CH": "41", "AT": "43", "SE": "46", "NO": "47", "DK": "45",
	"FI": "358", "PL": "48", "GR": "30", "TR": "90", "RU": "7", "UA": "380",
	"IN": "91", "PK": "92", "BD": "880", "CN": "86", "JP": "81", "KR": "82", "SG": "65", "MY": "60",
	"ID": "62", "PH": "63", "VN": "84", "TH": "66", "AU": "61", "NZ": "64",
	"ZA": "27", "NG": "234", "KE": "254", "GH": "233", "ET": "251",
	"BR": "55", "MX": "52", "AR": "54", "CO": "57", "CL": "56",
}

// DialCode returns the calling code for an ISO 3166-1 alpha-2 country, and
// whether it is known.
func DialCode(country string) (string, bool) {
	c, ok := dialCodes[strings.ToUpper(strings.TrimSpace(country))]
	return c, ok
}

// ToE164 normalises raw (a vCard TEL value, already unescaped) to E.164.
// defaultCountry is the workspace's default_country (ISO alpha-2), possibly
// empty. wasLocal reports whether raw had no country code of its own and
// defaultCountry had to be used to supply one — the preview surfaces that so
// an import from an unfamiliar workspace can be double-checked before commit.
func ToE164(raw, defaultCountry string) (e164 string, wasLocal bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	digits := marketing.OnlyDigits(raw)
	switch {
	case strings.HasPrefix(raw, "+"):
		// Already international.
		e164, err = marketing.NormalizePhone("+" + digits)
		return e164, false, err
	case strings.HasPrefix(digits, "00") && len(digits) > 2:
		// "00" is the international prefix outside the +-using world (and how
		// a lot of address books actually store an international number).
		e164, err = marketing.NormalizePhone("+" + digits[2:])
		return e164, false, err
	default:
		// A local number: strip one leading trunk "0" (the near-universal
		// domestic prefix) and prepend the workspace's default country.
		local := strings.TrimPrefix(digits, "0")
		if local == "" {
			return "", false, nil
		}
		code, ok := DialCode(defaultCountry)
		if !ok {
			return "", true, &LocalPhoneError{Raw: raw, Country: defaultCountry}
		}
		e164, err = marketing.NormalizePhone("+" + code + local)
		return e164, true, err
	}
}

// LocalPhoneError is returned by ToE164 when a number has no country code and
// the workspace has no usable default_country to supply one.
type LocalPhoneError struct {
	Raw     string
	Country string
}

func (e *LocalPhoneError) Error() string {
	if e.Country == "" {
		return "\"" + e.Raw + "\" has no country code and this workspace has no default country set"
	}
	return "\"" + e.Raw + "\" has no country code and \"" + e.Country + "\" is not a country this app knows the calling code for"
}

/*
MH-646: 867 of a real workspace's Google-imported contacts had a name, but
only 112 kept a phone — every local-format number silently dropped because
default_country was empty. ResolveCountry/InferDefaultCountry fix the two
sides of that: a workspace can now set default_country explicitly (the REST
route in internal/workspaces), and when it hasn't, InferDefaultCountry makes
a deterministic guess from the phones the workspace already has, so a local
number stands a chance of resolving even before anyone visits settings.
*/

// allDialCodes is every distinct calling code in dialCodes, longest first
// (a 3-digit code must be tried before a 1-digit one that would otherwise
// match as a false-positive prefix of the same digits), then lexically for a
// deterministic order between codes of the same length.
var allDialCodes = func() []string {
	seen := map[string]bool{}
	var codes []string
	for _, code := range dialCodes {
		if !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	sort.Slice(codes, func(i, j int) bool {
		if len(codes[i]) != len(codes[j]) {
			return len(codes[i]) > len(codes[j])
		}
		return codes[i] < codes[j]
	})
	return codes
}()

// matchDialCode finds the longest known calling code that prefixes phone's
// digits (phone may be E.164, with or without the leading '+').
func matchDialCode(phone string) (string, bool) {
	digits := marketing.OnlyDigits(phone)
	for _, code := range allDialCodes {
		if strings.HasPrefix(digits, code) {
			return code, true
		}
	}
	return "", false
}

// canonicalCountry returns the alphabetically-first ISO country in dialCodes
// using code — deterministic when several countries share a calling code
// (e.g. "1" for both US and CA).
func canonicalCountry(code string) string {
	best := ""
	for country, c := range dialCodes {
		if c == code && (best == "" || country < best) {
			best = country
		}
	}
	return best
}

// InferDefaultCountry guesses a workspace's default_country from the calling
// codes already present in its contacts' E.164 phones (contacts.phone and
// contact_identities with channel='phone'), when nothing has been set
// explicitly. It picks the most common calling code, ties broken by the
// lowest code then the alphabetically-first country using it — simple and
// deterministic, not a geolocation service. Returns "" when the workspace has
// no E.164 phone to learn from yet.
func InferDefaultCountry(ctx context.Context, db *sql.DB, wsID string) (string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT phone FROM contacts WHERE workspace_id = $1 AND phone LIKE '+%'
		UNION ALL
		SELECT external_id FROM contact_identities WHERE workspace_id = $1 AND channel = 'phone' AND external_id LIKE '+%'`,
		wsID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var phone string
		if err := rows.Scan(&phone); err != nil {
			return "", err
		}
		if code, ok := matchDialCode(phone); ok {
			counts[code]++
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	bestCode, bestCount := "", -1
	for code, n := range counts {
		if n > bestCount || (n == bestCount && code < bestCode) {
			bestCode, bestCount = code, n
		}
	}
	if bestCode == "" {
		return "", nil
	}
	return canonicalCountry(bestCode), nil
}

// ResolveCountry is the one place every caller (vCard import, Google
// sync/CSV, the MCP import tools) resolves which country to use for local
// phone numbers: an explicit override first, then the workspace's stored
// default_country, then a deterministic guess from InferDefaultCountry.
// Errors reading the workspace row or inferring are swallowed (returns "",
// nil) — an unresolvable country is ToE164's problem to report per number,
// not this helper's to fail on.
func ResolveCountry(ctx context.Context, db *sql.DB, wsID, override string) (string, error) {
	if override != "" {
		return strings.ToUpper(strings.TrimSpace(override)), nil
	}
	var country string
	if err := db.QueryRowContext(ctx, `SELECT default_country FROM workspaces WHERE id = $1`, wsID).Scan(&country); err != nil {
		return "", nil //nolint:nilerr // no resolvable workspace row is not this helper's problem to report
	}
	if country != "" {
		return country, nil
	}
	inferred, err := InferDefaultCountry(ctx, db, wsID)
	if err != nil {
		return "", nil //nolint:nilerr // best-effort inference; ToE164 reports per-number errors either way
	}
	return inferred, nil
}
