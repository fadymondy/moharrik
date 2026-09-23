// Package paging is the keyset paging every list endpoint shares (MH-467).
//
// MH-408 fixed the contacts list, which stopped at 1,000 rows without saying
// so. The same shape — an opaque cursor naming the last row a page returned, an
// accurate total for the filter, and a page size that is refused rather than
// quietly clamped when it is too big — is what campaigns, deals, companies and
// timelines use. This package is the cursor; each list keeps its own SQL.
package paging

import (
	"encoding/base64"
	"errors"
	"strings"
)

// ErrBadCursor is a cursor this server did not issue (or one mangled in transit).
var ErrBadCursor = errors.New("that page cursor is not valid")

// sep cannot occur in a UUID, a number or a timestamp, and is escaped anywhere
// else (a lower-cased company name is a cursor part).
const sep = "\x1f"

// Encode makes a cursor of a row's sort key, in the order the list sorts by.
func Encode(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, sep)))
}

// Decode reads a cursor back into exactly n parts. An empty cursor is the first
// page: nil, nil.
func Decode(s string, n int) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > 2048 {
		return nil, ErrBadCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrBadCursor
	}
	parts := strings.Split(string(raw), sep)
	if len(parts) != n {
		return nil, ErrBadCursor
	}
	for _, p := range parts[n-1:] {
		if p == "" { // the last part is always a row id
			return nil, ErrBadCursor
		}
	}
	return parts, nil
}

// Limit is the page size to use: def when none was asked for. Over max is not
// clamped here — the endpoints declare maximum:"…" on the parameter, so an
// oversize request is a 422 before it reaches this — but a caller that skips
// the schema (an MCP tool) gets the same refusal.
func Limit(n, def, max int) (int, error) {
	switch {
	case n < 0:
		return 0, errors.New("limit cannot be negative")
	case n == 0:
		return def, nil
	case n > max:
		return 0, &TooLargeError{Max: max}
	}
	return n, nil
}

// TooLargeError is a page size over the endpoint's maximum.
type TooLargeError struct{ Max int }

func (e *TooLargeError) Error() string {
	return "limit is at most " + itoa(e.Max) + "; follow next_cursor for the rest"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
