package research

import (
	"testing"
	"unicode/utf8"
)

// A page read as binary carried NUL bytes and invalid UTF-8, Postgres refused
// the INSERT, and the source vanished from a run that still counted it.
func TestCleanMakesTextStorable(t *testing.T) {
	for _, in := range []string{
		"plain text",
		"a\x00b\x00c",
		"bad \xff\xfe utf-8",
		"%PDF-1.7\x00\x00\x01\x02\xc3\x28 stream",
		"عربي\x00 mixed",
	} {
		out := clean(in)
		for i := 0; i < len(out); i++ {
			if out[i] == 0 {
				t.Fatalf("clean(%q) kept a NUL byte: %q", in, out)
			}
		}
		if !utf8.ValidString(out) {
			t.Fatalf("clean(%q) is not valid UTF-8: %q", in, out)
		}
	}
	if got := clean("a\x00b"); got != "ab" {
		t.Fatalf("clean dropped more than the NUL: %q", got)
	}
	if got := clean("عربي"); got != "عربي" {
		t.Fatalf("clean changed valid text: %q", got)
	}
}
