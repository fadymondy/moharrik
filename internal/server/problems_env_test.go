package server

import "testing"

// MH-479: only production files its 5xx and panics to the board.
func TestProblemReportsOnlyInProduction(t *testing.T) {
	for env, want := range map[string]bool{
		"production": true, " Production ": true,
		"development": false, "": false, "staging": false, "test": false,
	} {
		if got := problemReportsEnabled(env); got != want {
			t.Errorf("problemReportsEnabled(%q) = %v, want %v", env, got, want)
		}
	}
}
