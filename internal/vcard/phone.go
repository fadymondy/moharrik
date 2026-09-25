package vcard

import (
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
