package workspaces

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Acme Studio":        "acme-studio",
		"  3x1 -- Projects ": "3x1-projects",
		"Élan":               "lan",
		"مشاريع":             "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidSlug(t *testing.T) {
	for _, ok := range []string{"acme", "acme-studio", "a1b"} {
		if !ValidSlug(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "ab", "-acme", "acme-", "Acme", "platform", "api", "a_b"} {
		if ValidSlug(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
