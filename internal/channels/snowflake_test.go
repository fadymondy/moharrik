package channels

import "testing"

// Discord ids from the caller are spliced into the request path, so only a
// real snowflake may reach discordCall.
func TestIsSnowflake(t *testing.T) {
	for id, want := range map[string]bool{
		"1111111111": true, "": false, "../users/@me": false, "123?x=1": false,
		"123/messages": false, "123456789012345678901": false,
	} {
		if got := isSnowflake(id); got != want {
			t.Errorf("isSnowflake(%q) = %v, want %v", id, got, want)
		}
	}
}
