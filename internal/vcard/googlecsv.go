package vcard

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

/*
Google CSV import (MH-585): the "Google CSV" export from
contacts.google.com -> Export -> Google CSV, no OAuth needed. ParseGoogleCSV
turns each data row into a Card, the exact type vCard import and the Google
People sync (internal/google/contacts.go) both build — so a CSV row previews
and commits through vcard.PreviewCards/CommitCards exactly like either of
those.

Column shape: one header row, then scalar columns (Name Prefix, First Name,
Middle Name, Last Name, Name Suffix, Phonetic First Name, Phonetic Last Name,
Nickname, Birthday, Notes, Labels, Organization 1 - Name/Title/Department)
plus repeatable groups numbered from 1 (E-mail N, Phone N, Address N,
Website N, Relation N, Event N, Custom Field N), each "<Group> N - <Suffix>"
— <Suffix> is "Label" or "Type" depending on the export (Google has used
both spellings across versions; both are accepted here). A cell that holds
several values (Google folds e.g. two "Home" phone numbers into one Phone 1
cell some exports produce) is " ::: "-joined; ParseGoogleCSV splits on that
literal separator, which never appears in an ordinary value.
*/

const multiSep = " ::: "

// ParseGoogleCSV reads a Google Contacts CSV export (UTF-8, with or without
// a BOM) into Cards, one per data row. A row entirely blank is skipped
// (Google's export sometimes has a trailing blank line); a row whose CSV
// itself cannot be parsed becomes a ParseError instead of aborting the file,
// the same contract ParseCards has.
func ParseGoogleCSV(data []byte) (cards []Card, errs []ParseError) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // Google's own export is ragged on short trailing groups
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, []ParseError{{Index: 0, Reason: "unreadable CSV header: " + err.Error()}}
	}
	cols := indexColumns(header)

	idx := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, ParseError{Index: idx, Reason: "unreadable CSV row: " + err.Error()})
			idx++
			continue
		}
		if rowBlank(rec) {
			continue
		}
		cards = append(cards, rowToCard(rec, cols))
		idx++
	}
	return cards, errs
}

func rowBlank(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// columns indexes a Google CSV header: scalar column name -> position, and
// every repeatable group ("E-mail", "Phone", "Address", "Website",
// "Relation", "Event", "Custom Field") -> {N: {suffix: position}}.
type columns struct {
	scalar map[string]int
	groups map[string]map[int]map[string]int
}

var groupRe = regexp.MustCompile(`^(E-mail|Phone|Address|Website|Relation|Event|Organization|Custom Field) (\d+) - (.+)$`)

func indexColumns(header []string) columns {
	c := columns{scalar: map[string]int{}, groups: map[string]map[int]map[string]int{}}
	for i, h := range header {
		h = strings.TrimSpace(h)
		if m := groupRe.FindStringSubmatch(h); m != nil {
			group, n, suffix := m[1], m[2], m[3]
			num, _ := strconv.Atoi(n)
			if c.groups[group] == nil {
				c.groups[group] = map[int]map[string]int{}
			}
			if c.groups[group][num] == nil {
				c.groups[group][num] = map[string]int{}
			}
			c.groups[group][num][suffix] = i
			continue
		}
		c.scalar[h] = i
	}
	return c
}

func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func (c columns) get(rec []string, name string) string {
	i, ok := c.scalar[name]
	if !ok {
		return ""
	}
	return cell(rec, i)
}

// suffix reads a group cell trying a few spellings for the same concept
// (Google's exports are not consistent across versions/locales): e.g.
// "Value" falls back to "" (some exports put the value in the unsuffixed
// group column, rare) and "Label" falls back to "Type".
func groupCell(rec []string, sub map[string]int, names ...string) string {
	for _, n := range names {
		if i, ok := sub[n]; ok {
			if v := cell(rec, i); v != "" {
				return v
			}
		}
	}
	return ""
}

func splitMulti(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, multiSep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pseudoLabel is Google's own "* myContacts" marker in the Labels column,
// meaning only "this person is in My Contacts" — not a real tag the person
// created, so it is stripped rather than imported as one.
const pseudoLabel = "* myContacts"

func rowToCard(rec []string, cols columns) Card {
	c := Card{
		Version:      "3.0",
		ImportSource: "google_csv",
		XProps:       map[string]string{},
		Meta:         map[string]string{},
		N: Name{
			Prefix:     cols.get(rec, "Name Prefix"),
			Given:      cols.get(rec, "First Name"),
			Additional: cols.get(rec, "Middle Name"),
			Family:     cols.get(rec, "Last Name"),
			Suffix:     cols.get(rec, "Name Suffix"),
		},
		PhoneticGiven:  cols.get(rec, "Phonetic First Name"),
		PhoneticFamily: cols.get(rec, "Phonetic Last Name"),
		Nickname:       cols.get(rec, "Nickname"),
		BDay:           normalizeCSVDate(cols.get(rec, "Birthday")),
		Note:           cols.get(rec, "Notes"),
	}
	c.FN = strings.TrimSpace(strings.Join(nonEmptyStrings(c.N.Prefix, c.N.Given, c.N.Additional, c.N.Family, c.N.Suffix), " "))
	if c.FN == "" {
		c.FN = cols.get(rec, "File As")
	}

	for _, label := range splitMulti(cols.get(rec, "Labels")) {
		if label == "" || label == pseudoLabel {
			continue
		}
		c.Categories = append(c.Categories, label)
	}

	for n, sub := range cols.groups["E-mail"] {
		_ = n
		label := groupCell(rec, sub, "Label", "Type")
		for _, v := range splitMulti(groupCell(rec, sub, "Value")) {
			c.Emails = append(c.Emails, Email{Value: v, Label: label})
		}
	}
	for n, sub := range cols.groups["Phone"] {
		_ = n
		label := groupCell(rec, sub, "Label", "Type")
		for _, v := range splitMulti(groupCell(rec, sub, "Value")) {
			c.Phones = append(c.Phones, Phone{Value: v, Label: label})
		}
	}
	for n, sub := range cols.groups["Address"] {
		_ = n
		label := groupCell(rec, sub, "Label", "Type")
		addr := Address{
			Label:    label,
			Street:   groupCell(rec, sub, "Street", "Formatted"),
			Extended: groupCell(rec, sub, "Extended Address"),
			Locality: groupCell(rec, sub, "City"),
			Region:   groupCell(rec, sub, "Region"),
			Postal:   groupCell(rec, sub, "Postal Code"),
			Country:  groupCell(rec, sub, "Country"),
		}
		if addr.Street != "" || addr.Extended != "" || addr.Locality != "" || addr.Region != "" || addr.Postal != "" || addr.Country != "" {
			c.Addresses = append(c.Addresses, addr)
		}
	}
	for n, sub := range cols.groups["Website"] {
		_ = n
		for _, v := range splitMulti(groupCell(rec, sub, "Value")) {
			c.URLs = append(c.URLs, v)
		}
	}

	if orgs := cols.groups["Organization"]; len(orgs) > 0 {
		first := true
		for _, n := range sortedKeys(orgs) {
			sub := orgs[n]
			name := groupCell(rec, sub, "Name")
			title := groupCell(rec, sub, "Title")
			dept := groupCell(rec, sub, "Department")
			if name == "" && title == "" && dept == "" {
				continue
			}
			if first {
				c.Org, c.Title, c.Role = name, title, dept
				first = false
				continue
			}
			c.Meta[fmt.Sprintf("google_organization_%d", n)] = strings.TrimSpace(name + " / " + title)
		}
	}

	for n, sub := range cols.groups["Relation"] {
		typ := groupCell(rec, sub, "Label", "Type")
		val := groupCell(rec, sub, "Value")
		if val == "" {
			continue
		}
		c.Meta[fmt.Sprintf("google_relation_%d_%s", n, safeKey(typ))] = val
	}
	for n, sub := range cols.groups["Event"] {
		typ := groupCell(rec, sub, "Label", "Type")
		val := groupCell(rec, sub, "Value")
		if val == "" {
			continue
		}
		c.Meta[fmt.Sprintf("google_event_%d_%s", n, safeKey(typ))] = val
	}
	for _, sub := range cols.groups["Custom Field"] {
		key := groupCell(rec, sub, "Label", "Type")
		val := groupCell(rec, sub, "Value")
		if key == "" || val == "" {
			continue
		}
		if strings.HasPrefix(key, "google_") {
			key = "custom_" + key
		}
		c.Meta[key] = val
	}

	return c
}

func safeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "other"
	}
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return '_'
		}
		return r
	}, s)
}

func sortedKeys(m map[int]map[string]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// normalizeCSVDate turns Google's "YYYY-MM-DD" (or occasionally "--MM-DD"
// for a year-less birthday, which this app's date column cannot store) into
// what Card.BDay/safeDate() expects, dropping anything it cannot place.
func normalizeCSVDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) == 10 && v[4] == '-' && v[7] == '-' && v[0] != '-' {
		return v
	}
	return ""
}
