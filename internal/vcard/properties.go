package vcard

import (
	"mime/quotedprintable"
	"regexp"
	"strings"
)

// decodeQP decodes a QUOTED-PRINTABLE value. vCard 2.1 QP values use CRLF
// soft breaks (already gone by the time we get here — unfold() only handles
// RFC 6350 space/tab folding, and 2.1 QP soft-breaks end a line with a bare
// "=" which mime/quotedprintable already understands) so this is a thin
// wrapper that falls back to the raw value if decoding fails outright rather
// than losing the property.
func decodeQP(v string) string {
	// mime/quotedprintable expects CRLF for soft line breaks; vCard values
	// arrive as a single unfolded line, so "=\r\n"/"=\n" soft breaks (if any
	// survived unfolding, e.g. a value that itself contains one) decode fine
	// as-is, and a plain run of "=XX" hex escapes needs no line handling at
	// all.
	r := quotedprintable.NewReader(strings.NewReader(v))
	buf := make([]byte, 0, len(v))
	tmp := make([]byte, 256)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	if len(buf) == 0 && v != "" {
		return v
	}
	return string(buf)
}

var dateDigits = regexp.MustCompile(`^(\d{4})-?(\d{2})-?(\d{2})`)

// normalizeDate turns a vCard BDAY/ANNIVERSARY value into YYYY-MM-DD when it
// looks like a date (both "19850304" and "1985-03-04" appear in the wild,
// plus a 4.0 "date-and-or-time" with a trailing time this app has no column
// for and simply drops). Anything else — e.g. Apple's "--03-04" (no year,
// vCard 4.0) — is returned unchanged so it is never silently lost, just not
// interpreted as a full date.
func normalizeDate(v string) string {
	v = strings.TrimSpace(v)
	if m := dateDigits.FindStringSubmatch(v); m != nil {
		return m[1] + "-" + m[2] + "-" + m[3]
	}
	return v
}

// applyProperty decodes one property's value and folds it into c. labelFor
// is the X-ABLabel text for this property's item group, if any.
func applyProperty(c *Card, p property, labelFor string) {
	name := p.name
	if p.name == "" {
		return
	}
	// The label carrier property itself contributes nothing beyond what the
	// two callers above already used it for.
	if name == "X-ABLABEL" {
		return
	}

	switch name {
	case "VERSION":
		v, _ := decodedValue(p)
		if v != "" {
			c.Version = v
		}
	case "FN":
		v, _ := decodedValue(p)
		c.FN = unescape(v)
	case "N":
		v, _ := decodedValue(p)
		parts := splitEscaped(v, ';')
		get := func(i int) string {
			if i < len(parts) {
				return unescape(parts[i])
			}
			return ""
		}
		c.N = Name{Family: get(0), Given: get(1), Additional: get(2), Prefix: get(3), Suffix: get(4)}
	case "NICKNAME":
		v, _ := decodedValue(p)
		names := splitEscaped(v, ',')
		if len(names) > 0 {
			c.Nickname = unescape(names[0])
		}
	case "TEL":
		v, _ := decodedValue(p)
		c.Phones = append(c.Phones, Phone{Value: unescape(v), Types: typesOf(p), Preferred: isPreferred(p), Label: labelFor})
	case "EMAIL":
		v, _ := decodedValue(p)
		c.Emails = append(c.Emails, Email{Value: unescape(v), Types: typesOf(p), Preferred: isPreferred(p), Label: labelFor})
	case "ADR":
		v, _ := decodedValue(p)
		parts := splitEscaped(v, ';')
		get := func(i int) string {
			if i < len(parts) {
				return unescape(parts[i])
			}
			return ""
		}
		c.Addresses = append(c.Addresses, Address{
			Types: typesOf(p), Label: labelFor,
			POBox: get(0), Extended: get(1), Street: get(2),
			Locality: get(3), Region: get(4), Postal: get(5), Country: get(6),
		})
	case "LABEL":
		// A free-text mailing label with no structured ADR to attach to;
		// keep it as an address with only Label/Street set rather than
		// dropping it.
		v, _ := decodedValue(p)
		c.Addresses = append(c.Addresses, Address{Types: typesOf(p), Street: unescape(v)})
	case "ORG":
		v, _ := decodedValue(p)
		parts := splitEscaped(v, ';')
		if len(parts) > 0 {
			c.Org = unescape(parts[0])
		}
	case "TITLE":
		v, _ := decodedValue(p)
		c.Title = unescape(v)
	case "ROLE":
		v, _ := decodedValue(p)
		c.Role = unescape(v)
	case "BDAY":
		v, _ := decodedValue(p)
		c.BDay = normalizeDate(v)
	case "ANNIVERSARY":
		v, _ := decodedValue(p)
		c.Anniv = normalizeDate(v)
	case "URL":
		v, _ := decodedValue(p)
		if v != "" {
			c.URLs = append(c.URLs, unescape(v))
		}
	case "CATEGORIES":
		v, _ := decodedValue(p)
		for _, cat := range splitEscaped(v, ',') {
			cat = strings.TrimSpace(unescape(cat))
			if cat != "" {
				c.Categories = append(c.Categories, cat)
			}
		}
	case "NOTE":
		v, _ := decodedValue(p)
		v = unescape(v)
		if c.Note != "" {
			c.Note += "\n" + v
		} else {
			c.Note = v
		}
	case "UID":
		v, _ := decodedValue(p)
		c.UID = unescape(v)
	case "PHOTO":
		v, isB64 := decodedValue(p)
		if isB64 && v != "" {
			mime := "image/jpeg"
			for _, t := range p.params["TYPE"] {
				switch strings.ToUpper(t) {
				case "PNG":
					mime = "image/png"
				case "GIF":
					mime = "image/gif"
				case "JPEG", "JPG":
					mime = "image/jpeg"
				}
			}
			c.PhotoURL = "data:" + mime + ";base64," + v
		} else if v != "" {
			c.PhotoURL = unescape(v)
		}
	case "IMPP":
		v, _ := decodedValue(p)
		if v != "" {
			c.IMPPs = append(c.IMPPs, IMPP{URI: unescape(v), Types: typesOf(p), Label: labelFor})
		}
	case "REV", "KIND", "PRODID", "SOURCE", "CLASS", "SORT-STRING", "GENDER":
		// Recognised but not modelled — nothing this app stores yet needs
		// them and they carry no contact-identity information.
	default:
		if strings.HasPrefix(name, "X-") {
			v, isB64 := decodedValue(p)
			if isB64 {
				// An unrecognised X- binary property: keep a note that it
				// existed rather than dumping raw bytes into text meta.
				v = "(binary)"
			}
			if v != "" {
				if !isB64 {
					v = unescape(v)
				}
				key := strings.ToLower(strings.TrimPrefix(name, "X-"))
				if _, exists := c.XProps[key]; !exists {
					c.XProps[key] = v
				}
			}
		}
	}
}
