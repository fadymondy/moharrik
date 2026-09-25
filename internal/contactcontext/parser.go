// Package contactcontext is MH-586: a per-contact context built from a
// WhatsApp chat export, notes and files added by hand or over MCP, and the
// contact's existing conversations/deals/meta fields — searchable (Postgres
// full-text, 'simple' config, so Arabic is indexed correctly) and summarised
// into a report.
//
// No vector database and no new AI SDK are introduced here: internal/ai
// already gives this codebase a pluggable, provider-agnostic generation
// client with an honest ErrNoProvider path (see report.go), so that is what
// ask_about_contact and generate_contact_report call. Retrieval itself is
// plain tsvector search (retrieval.go) — this repo has no embedding/pgvector
// infrastructure to reuse, and the card is explicit that one should not be
// added for this feature.
package contactcontext

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Message is one parsed line of a WhatsApp export, before it is filed.
type Message struct {
	Time       time.Time
	Sender     string // "" for a system line
	Text       string
	System     bool
	MediaFile  string // set when the line references an attached/omitted file
	MediaOnly  bool   // the whole message is just the media placeholder
	lineNumber int
}

// ParsedExport is one export file (txt or the txt found inside a zip), fully
// parsed but not yet written anywhere.
type ParsedExport struct {
	Filename string
	Messages []Message
	// Media holds every non-txt file found in a zip, keyed by its exact name
	// as it appears in <attached: name> lines.
	Media map[string][]byte
	// DayFirst records which date convention this file was parsed with, for
	// display/debugging.
	DayFirst bool
}

// stripMarks removes the Unicode control/direction marks WhatsApp exports
// litter iOS text with (U+200E, U+200F) and the bidi override range
// (U+202A-U+202E), which otherwise corrupt Arabic text and break the line
// regexes below.
func stripMarks(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '‎' || r == '‏':
			return -1
		case r >= '‪' && r <= '‮':
			return -1
		default:
			return r
		}
	}, s)
}

// androidLine matches "dd/mm/yy, HH:MM[:SS][ AM/PM] - rest".
var androidLine = regexp.MustCompile(`^(\d{1,2}/\d{1,2}/\d{2,4}),\s*(\d{1,2}:\d{2}(?::\d{2})?(?:\s?[AaPp][Mm])?)\s*-\s*(.*)$`)

// iosLine matches "[dd/mm/yy, HH:MM:SS AM/PM] rest".
var iosLine = regexp.MustCompile(`^\[(\d{1,2}/\d{1,2}/\d{2,4}),\s*(\d{1,2}:\d{2}(?::\d{2})?(?:\s?[AaPp][Mm])?)\]\s*(.*)$`)

// senderLine splits "Name: message" from a plain system line. Names in real
// exports are short (a saved contact name, a phone number, a business name);
// capping the match keeps a colon inside the message body (very common:
// "Note: call back tomorrow") from being mistaken for a sender.
var senderLine = regexp.MustCompile(`^([^:\n]{1,60}): ([\s\S]*)$`)

var mediaOmitted = regexp.MustCompile(`(?i)^(<media omitted>|image omitted|video omitted|audio omitted|gif omitted|sticker omitted|document omitted)$`)
var mediaAttached = regexp.MustCompile(`(?i)^<attached:\s*(.+?)>$`)

// dateRe pulls the three numeric fields out of a matched date string, used by
// detectDayFirst.
var dateRe = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{2,4})$`)

// ParseText parses one export .txt file's raw bytes (already extracted from
// a zip, or uploaded directly).
func ParseText(filename string, data []byte) (*ParsedExport, error) {
	text := stripMarks(string(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))))
	lines := strings.Split(text, "\n")

	dayFirst := detectDayFirst(lines)

	var msgs []Message
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := androidLine.FindStringSubmatch(line); m != nil {
			ts, err := parseTimestamp(m[1], m[2], dayFirst)
			if err == nil {
				msgs = append(msgs, buildMessage(ts, m[3], i))
				continue
			}
		}
		if m := iosLine.FindStringSubmatch(line); m != nil {
			ts, err := parseTimestamp(m[1], m[2], dayFirst)
			if err == nil {
				msgs = append(msgs, buildMessage(ts, m[3], i))
				continue
			}
		}
		// A continuation line (a multi-line message body) belongs to the
		// previous message, whatever it was.
		if len(msgs) > 0 {
			last := &msgs[len(msgs)-1]
			last.Text = last.Text + "\n" + line
		}
		// A line before any timestamped line at all (rare: a stray header) is
		// dropped rather than crashing the import.
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("contactcontext: no WhatsApp-export lines recognised in %q", filename)
	}
	return &ParsedExport{Filename: filename, Messages: msgs, DayFirst: dayFirst}, nil
}

func buildMessage(ts time.Time, rest string, lineNo int) Message {
	if m := senderLine.FindStringSubmatch(rest); m != nil {
		name := strings.TrimSpace(m[1])
		body := m[2]
		msg := Message{Time: ts, Sender: name, Text: body, lineNumber: lineNo}
		annotateMedia(&msg)
		return msg
	}
	// No "Name:" prefix: a system line (join notice, encryption notice, a
	// missed call, etc).
	msg := Message{Time: ts, System: true, Text: rest, lineNumber: lineNo}
	annotateMedia(&msg)
	return msg
}

func annotateMedia(msg *Message) {
	body := strings.TrimSpace(msg.Text)
	if mediaOmitted.MatchString(body) {
		msg.MediaOnly = true
		return
	}
	if m := mediaAttached.FindStringSubmatch(body); m != nil {
		msg.MediaFile = strings.TrimSpace(m[1])
		msg.MediaOnly = true
	}
}

// detectDayFirst scans every date field in the file. Per MH-586: if any
// date's first numeric field is greater than 12, the whole file is day-first
// (dd/mm/yy) — a month cannot be 13 or more. Absent that evidence the file is
// treated as month-first (m/d/yy), which is the iOS default and the safer of
// the two guesses when the export is otherwise ambiguous (every date <= 12/12).
func detectDayFirst(lines []string) bool {
	for _, line := range lines {
		var dateStr string
		if m := androidLine.FindStringSubmatch(line); m != nil {
			dateStr = m[1]
		} else if m := iosLine.FindStringSubmatch(line); m != nil {
			dateStr = m[1]
		} else {
			continue
		}
		parts := dateRe.FindStringSubmatch(dateStr)
		if parts == nil {
			continue
		}
		if n, err := strconv.Atoi(parts[1]); err == nil && n > 12 {
			return true
		}
	}
	return false
}

func parseTimestamp(dateStr, timeStr string, dayFirst bool) (time.Time, error) {
	parts := dateRe.FindStringSubmatch(dateStr)
	if parts == nil {
		return time.Time{}, fmt.Errorf("bad date %q", dateStr)
	}
	f1, err1 := strconv.Atoi(parts[1])
	f2, err2 := strconv.Atoi(parts[2])
	year, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, fmt.Errorf("bad date %q", dateStr)
	}
	var day, month int
	if dayFirst {
		day, month = f1, f2
	} else {
		month, day = f1, f2
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		// The scan-the-whole-file heuristic can still leave one line
		// swapped (e.g. "13/1/24" earlier forced day-first, but a later
		// "5/13/24" cannot be day-first). Fall back to swapping this one
		// line rather than failing the whole import over it.
		day, month = month, day
		if month < 1 || month > 12 || day < 1 || day > 31 {
			return time.Time{}, fmt.Errorf("bad date %q", dateStr)
		}
	}
	if year < 100 {
		year += 2000
	}

	timeStr = strings.TrimSpace(timeStr)
	ampm := ""
	up := strings.ToUpper(timeStr)
	if strings.HasSuffix(up, "AM") || strings.HasSuffix(up, "PM") {
		ampm = up[len(up)-2:]
		timeStr = strings.TrimSpace(timeStr[:len(timeStr)-2])
	}
	hms := strings.Split(timeStr, ":")
	if len(hms) < 2 {
		return time.Time{}, fmt.Errorf("bad time %q", timeStr)
	}
	hour, err := strconv.Atoi(hms[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time %q", timeStr)
	}
	minute, err := strconv.Atoi(hms[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time %q", timeStr)
	}
	second := 0
	if len(hms) > 2 {
		second, _ = strconv.Atoi(hms[2])
	}
	switch ampm {
	case "AM":
		if hour == 12 {
			hour = 0
		}
	case "PM":
		if hour != 12 {
			hour += 12
		}
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), nil
}

// ParseArchive extracts one .txt export plus its referenced media from a
// .zip. The txt is whichever entry has the largest size among *.txt members
// (WhatsApp's own zip has exactly one); everything else is kept, keyed by
// base filename, for the <attached: ...> lines to resolve against.
func ParseArchive(filename string, data []byte) (*ParsedExport, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("contactcontext: not a valid zip: %w", err)
	}
	var txtName string
	var txtBytes []byte
	media := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := baseName(f.Name)
		if strings.HasSuffix(strings.ToLower(base), ".txt") {
			if txtBytes == nil || f.UncompressedSize64 > uint64(len(txtBytes)) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				txtName, txtBytes = base, b
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		media[base] = b
	}
	if txtBytes == nil {
		return nil, fmt.Errorf("contactcontext: zip %q has no .txt export inside it", filename)
	}
	parsed, err := ParseText(txtName, txtBytes)
	if err != nil {
		return nil, err
	}
	parsed.Filename = filename
	parsed.Media = media
	return parsed, nil
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ParseUpload dispatches to ParseText or ParseArchive by filename/content.
func ParseUpload(filename string, data []byte) (*ParsedExport, error) {
	lower := strings.ToLower(filename)
	if strings.HasSuffix(lower, ".zip") || (len(data) >= 4 && bytes.Equal(data[:2], []byte("PK"))) {
		return ParseArchive(filename, data)
	}
	return ParseText(filename, data)
}

// Senders returns the distinct human senders in an export (system lines
// excluded), most-frequent first, for the preview step's sender picker.
func (p *ParsedExport) Senders() []string {
	counts := map[string]int{}
	for _, m := range p.Messages {
		if !m.System && m.Sender != "" {
			counts[m.Sender]++
		}
	}
	out := make([]string, 0, len(counts))
	for s := range counts {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return counts[out[i]] > counts[out[j]] })
	return out
}

// DateRange returns the first and last message timestamps, zero values if
// there are none.
func (p *ParsedExport) DateRange() (from, to time.Time) {
	for i, m := range p.Messages {
		if i == 0 || m.Time.Before(from) {
			from = m.Time
		}
		if i == 0 || m.Time.After(to) {
			to = m.Time
		}
	}
	return from, to
}

// IsGroupOrAmbiguous reports whether the preview step must ask which sender
// is the contact: more than two distinct senders (a group chat), or fewer
// than two (nothing to choose between / a broken parse).
func (p *ParsedExport) IsGroupOrAmbiguous() bool {
	return len(p.Senders()) != 2
}

// GuessContactSender returns the sender name that best matches a known
// contact display name, and whether the guess is confident enough to
// pre-select without asking. Exact case-insensitive match only — anything
// looser risks silently mis-assigning which side of the conversation is the
// customer.
func (p *ParsedExport) GuessContactSender(contactName string) (string, bool) {
	senders := p.Senders()
	if len(senders) != 2 || strings.TrimSpace(contactName) == "" {
		return "", false
	}
	cn := strings.ToLower(strings.TrimSpace(contactName))
	for _, s := range senders {
		if strings.ToLower(s) == cn {
			return s, true
		}
	}
	return "", false
}

// normalizeForDedupe folds a message body down to the shape the dedupe key
// hashes: trimmed, whitespace-collapsed, case-folded. It intentionally does
// NOT strip punctuation — two different messages that happen to share words
// should not collide.
func normalizeForDedupe(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
