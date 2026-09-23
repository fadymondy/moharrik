package automation

import "testing"

func TestCompareVersionsIsNumericNotTextual(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.2", "0.2.0", -1},
		{"0.10.0", "0.9.3", 1}, // a string comparison says the opposite
		{"0.2.0", "0.2.0", 0},
		{"0.2", "0.2.0", 0},
		{"1.0.0", "0.99.99", 1},
		{"garbage", "0.0.1", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestOutdatedAgainstTheOfferedBuild(t *testing.T) {
	latest := &ExtensionBuild{Version: "0.2.0"}
	if !Outdated("0.1.2", latest) || Outdated("0.2.0", latest) || Outdated("0.3.0", latest) {
		t.Error("older is outdated, same and newer are not")
	}
	if !Outdated("", latest) {
		t.Error("a browser that never reported a version is not on the current build")
	}
	if Outdated("0.0.1", nil) {
		t.Error("with nothing packaged there is nothing to update to")
	}
}
