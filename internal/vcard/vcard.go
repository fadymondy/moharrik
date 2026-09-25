// Package vcard parses and writes vCard 2.1/3.0/4.0 (MH-584): the format
// every phone/desktop address book exports as (Android, iPhone, Google
// Contacts, Outlook). It knows nothing about this app's database — Card is a
// plain, fully-decoded value type — so it can be unit tested with fixture
// text files and no database at all. import.go is the thin layer on top that
// turns a parsed Card into an internal/contacts.Candidate and writes it.
package vcard

import (
	"strconv"
	"strings"
)

// Phone is one TEL.
type Phone struct {
	Value     string   // raw, as written in the file (unescaped, not yet normalised)
	Types     []string // "CELL", "HOME", "WORK", "VOICE", ... upper-cased
	Preferred bool
	Label     string // from an X-ABLabel item group, if any
}

// Email is one EMAIL.
type Email struct {
	Value     string
	Types     []string
	Preferred bool
	Label     string
}

// Address is one ADR.
type Address struct {
	Types                                                      []string
	POBox, Extended, Street, Locality, Region, Postal, Country string
	Label                                                      string // LABEL param/property or an X-ABLabel item group
}

// IMPP is one instant-messaging/SIP URI (IMPP, or a well-known X-* IM property).
type IMPP struct {
	URI   string // e.g. "sip:fady@example.test" or "skype:fady.mondy"
	Types []string
	Label string
}

// Name is the parsed N property (family;given;additional;prefix;suffix).
type Name struct {
	Family, Given, Additional, Prefix, Suffix string
}

// Card is one fully-decoded VCARD…END:VCARD block.
type Card struct {
	Version string // "2.1", "3.0" or "4.0"

	FN       string
	N        Name
	Nickname string

	// PhoneticGiven/PhoneticFamily and Meta exist for importers that have no
	// vCard property to decode them from (MH-585's Google People/CSV
	// importers set them directly; ParseCards never does — vCard 4.0's own
	// phonetic params are rare enough in the wild that decoding them was out
	// of scope for MH-584 and stays that way here). Meta is fill-blank
	// key/value data merged into contacts.meta (Google's userDefined
	// fields, plus organizations/relations/events this app has no dedicated
	// repeatable columns for — see contacts.go's metaJSON for the encoding).
	PhoneticGiven  string
	PhoneticFamily string
	Meta           map[string]string

	Phones     []Phone
	Emails     []Email
	Addresses  []Address
	IMPPs      []IMPP
	URLs       []string
	Categories []string

	Org      string // ORG's first component (company/organisation name)
	Title    string
	Role     string
	BDay     string // YYYY-MM-DD when it could be parsed, else the raw value
	Anniv    string // ANNIVERSARY, same rule as BDay
	Note     string
	UID      string
	PhotoURL string // an http(s) PHOTO value, or a data: URI for an embedded (BASE64) one

	// ImportSource (MH-585) overrides the "vcard_import" Candidate.Source and
	// the "vcard" external_ids/meta key classify()/applyExtraFields default
	// to, so a Card built from Google People or a Google CSV row is
	// attributed and keyed correctly through the exact same commit path.
	// Empty means "a real vCard file" (unchanged MH-584 behaviour). When
	// set, UID is expected already namespaced as "<ImportSource>:<id>"
	// (e.g. "google:people/c123") rather than a bare vCard UID.
	ImportSource string

	// Unknown X-* properties this package does not otherwise understand, so
	// nothing an import sees is silently dropped. Keyed by the lower-cased
	// property name without its "X-" prefix, e.g. "x-skype" -> "skype".
	XProps map[string]string

	// Warnings collected while decoding this card (e.g. a TEL that could not
	// be normalised) — never fatal to the card, always surfaced in a preview.
	Warnings []string
}

// property is one decoded, but not yet interpreted, vCard line.
type property struct {
	group  string // "" or e.g. "item1"
	name   string // upper-cased, e.g. "TEL", "X-SKYPE"
	params map[string][]string
	value  string // raw value, NOT unescaped, NOT charset/QP/base64-decoded
}

// ParseError is one card in a file that could not be parsed at all (not the
// same as a card that parsed fine but has nothing useful in it — that is
// classified by the import preview, not here).
type ParseError struct {
	Index  int // 0-based position of the card in the file
	Reason string
}

func (e ParseError) Error() string { return e.Reason }

// ParseCards reads every VCARD block in data. A block that is malformed
// enough that it cannot be split into properties at all is reported in errs
// and skipped; everything else always returns a Card (possibly with
// Warnings), even one with no usable identity — the caller decides what to
// do with that.
func ParseCards(data []byte) (cards []Card, errs []ParseError) {
	text := normalizeNewlines(string(data))
	lines := unfold(text)

	var cur []string
	idx := 0
	inCard := false
	for _, ln := range lines {
		u := strings.ToUpper(strings.TrimSpace(ln))
		switch {
		case u == "BEGIN:VCARD":
			cur = nil
			inCard = true
		case u == "END:VCARD":
			if !inCard {
				continue
			}
			c, err := parseOneCard(cur)
			if err != "" {
				errs = append(errs, ParseError{Index: idx, Reason: err})
			} else {
				cards = append(cards, c)
			}
			idx++
			inCard = false
		default:
			if inCard && strings.TrimSpace(ln) != "" {
				cur = append(cur, ln)
			}
		}
	}
	return cards, errs
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// unfold joins RFC 6350 folded continuation lines: a line starting with a
// single space or tab is a continuation of the previous one, joined with
// that one leading character removed (not replaced by anything — vCard
// folding may split in the middle of a word or an escape sequence).
func unfold(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, ln := range raw {
		if (strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t")) && len(out) > 0 {
			out[len(out)-1] += ln[1:]
			continue
		}
		out = append(out, ln)
	}
	return out
}

func parseOneCard(lines []string) (Card, string) {
	if len(lines) == 0 {
		return Card{}, "empty vCard (no properties between BEGIN and END)"
	}
	props := make([]property, 0, len(lines))
	for _, ln := range lines {
		p, ok := parseLine(ln)
		if ok {
			props = append(props, p)
		}
	}

	c := Card{Version: "3.0", XProps: map[string]string{}}

	// X-ABLabel items (Apple's way of naming a TEL/EMAIL/URL/ADR with a
	// human label like "iPhone" or "Home Fax") are collected first so the
	// property loop below can attach them by group.
	labels := map[string]string{}
	for _, p := range props {
		if p.group != "" && p.name == "X-ABLABEL" {
			labels[p.group] = decodeABLabel(unescape(p.value))
		}
	}

	for _, p := range props {
		labelFor := ""
		if p.group != "" {
			labelFor = labels[p.group]
		}
		applyProperty(&c, p, labelFor)
	}
	if c.FN == "" {
		c.FN = strings.TrimSpace(strings.Join(nonEmptyStrings(c.N.Prefix, c.N.Given, c.N.Additional, c.N.Family, c.N.Suffix), " "))
	}
	return c, ""
}

func nonEmptyStrings(ss ...string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// decodeABLabel turns Apple's "_$!<Mobile>!$_" wrapper into "Mobile"; a label
// with no such wrapper (a custom one the user typed) is returned unchanged.
func decodeABLabel(v string) string {
	const prefix, suffix = "_$!<", ">!$_"
	if strings.HasPrefix(v, prefix) && strings.HasSuffix(v, suffix) {
		return v[len(prefix) : len(v)-len(suffix)]
	}
	return v
}

// parseLine splits one unfolded, non-empty property line into name, params
// and raw value. Returns ok=false for a line this package does not need
// (VERSION is read separately below via applyProperty; a line with no ':' at
// all is simply dropped rather than failing the whole card).
func parseLine(ln string) (property, bool) {
	meta, value, ok := splitTopLevel(ln, ':')
	if !ok {
		return property{}, false
	}
	tokens := splitQuoted(meta, ';')
	if len(tokens) == 0 || strings.TrimSpace(tokens[0]) == "" {
		return property{}, false
	}
	nameField := tokens[0]
	group, name := "", nameField
	if i := strings.Index(nameField, "."); i >= 0 {
		group, name = nameField[:i], nameField[i+1:]
	}
	p := property{group: group, name: strings.ToUpper(strings.TrimSpace(name)), params: map[string][]string{}, value: value}
	for _, tok := range tokens[1:] {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if i := strings.Index(tok, "="); i >= 0 {
			key := strings.ToUpper(strings.TrimSpace(tok[:i]))
			for _, v := range splitQuoted(tok[i+1:], ',') {
				v = strings.Trim(strings.TrimSpace(v), `"`)
				if v != "" {
					p.params[key] = append(p.params[key], v)
				}
			}
			continue
		}
		// vCard 2.1 bare parameter: either an encoding keyword or a TYPE value.
		up := strings.ToUpper(tok)
		switch up {
		case "QUOTED-PRINTABLE", "BASE64", "8BIT", "7BIT", "B":
			enc := up
			if enc == "B" {
				enc = "BASE64"
			}
			p.params["ENCODING"] = append(p.params["ENCODING"], enc)
		default:
			p.params["TYPE"] = append(p.params["TYPE"], up)
		}
	}
	return p, true
}

// splitTopLevel finds the first sep that is not inside a double-quoted
// section, so a quoted TYPE value or a URL value containing ':' does not
// break the split.
func splitTopLevel(s string, sep byte) (before, after string, ok bool) {
	inQuotes := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuotes = !inQuotes
		case sep:
			if !inQuotes {
				return s[:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

// splitQuoted splits on sep, ignoring sep characters inside double quotes.
func splitQuoted(s string, sep byte) []string {
	var out []string
	inQuotes := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuotes = !inQuotes
		case sep:
			if !inQuotes {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// splitEscaped splits a vCard value on an UN-escaped sep (one not preceded by
// an odd number of backslashes), leaving the escape sequences themselves
// intact for unescape() to resolve afterwards.
func splitEscaped(s string, sep byte) []string {
	var out []string
	var cur strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			cur.WriteByte(c)
			escaped = false
		case c == '\\':
			cur.WriteByte(c)
			escaped = true
		case c == sep:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

// unescape resolves \\, \;, \, and \n/\N per RFC 6350 §3.4.
func unescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case ';':
				b.WriteByte(';')
			case ',':
				b.WriteByte(',')
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// decodedValue applies ENCODING (QUOTED-PRINTABLE/BASE64) only — it
// deliberately does NOT resolve backslash escapes, because a structured
// property (N, ADR, ORG, NICKNAME, CATEGORIES) must split on an UNescaped
// separator first (splitEscaped) and only unescape() each resulting part;
// unescaping the whole value here first would turn a real "\," into a plain
// "," before splitEscaped ever saw it, destroying the very thing it needs to
// tell a separator from an escaped one. Every call site is responsible for
// its own single unescape() — on the whole value for a scalar property, or
// per-part after splitEscaped for a structured one. BASE64 values are
// returned as raw decoded-header base64 text unchanged (callers decode them
// themselves — unescape() must never touch photo bytes).
func decodedValue(p property) (string, bool /*isBase64*/) {
	enc := strings.ToUpper(strings.Join(p.params["ENCODING"], ""))
	switch enc {
	case "QUOTED-PRINTABLE":
		return decodeQP(p.value), false
	case "B", "BASE64":
		return strings.Join(strings.Fields(p.value), ""), true
	default:
		return p.value, false
	}
}

func hasType(p property, want string) bool {
	for _, t := range p.params["TYPE"] {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func isPreferred(p property) bool {
	if hasType(p, "PREF") {
		return true
	}
	if v, ok := p.params["PREF"]; ok && len(v) > 0 {
		n, err := strconv.Atoi(v[0])
		return err == nil && n <= 1
	}
	return false
}

func typesOf(p property) []string {
	out := make([]string, 0, len(p.params["TYPE"]))
	for _, t := range p.params["TYPE"] {
		if !strings.EqualFold(t, "PREF") {
			out = append(out, strings.ToUpper(t))
		}
	}
	return out
}
