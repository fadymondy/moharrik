package paging

import (
	"errors"
	"reflect"
	"testing"
)

func TestCursorRoundTrips(t *testing.T) {
	c := Encode("acme | co", "2026-09-23T10:00:00.123456Z", "b1c2")
	got, err := Decode(c, 3)
	if err != nil || !reflect.DeepEqual(got, []string{"acme | co", "2026-09-23T10:00:00.123456Z", "b1c2"}) {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := Decode("", 3); got != nil || err != nil {
		t.Fatalf("empty cursor: %q %v", got, err)
	}
	if _, err := Decode("!!!", 2); !errors.Is(err, ErrBadCursor) {
		t.Error("a cursor that is not base64 must be refused")
	}
	if _, err := Decode(Encode("a", "b"), 3); !errors.Is(err, ErrBadCursor) {
		t.Error("a cursor with the wrong number of parts must be refused")
	}
	if _, err := Decode(Encode("a", ""), 2); !errors.Is(err, ErrBadCursor) {
		t.Error("a cursor with no row id must be refused")
	}
}

func TestLimitRefusesRatherThanClamps(t *testing.T) {
	if n, err := Limit(0, 50, 200); n != 50 || err != nil {
		t.Errorf("default: %d %v", n, err)
	}
	if n, err := Limit(200, 50, 200); n != 200 || err != nil {
		t.Errorf("max: %d %v", n, err)
	}
	var tl *TooLargeError
	if _, err := Limit(201, 50, 200); !errors.As(err, &tl) || tl.Max != 200 {
		t.Errorf("over max must be refused, got %v", err)
	}
	if _, err := Limit(-1, 50, 200); err == nil {
		t.Error("negative must be refused")
	}
}
