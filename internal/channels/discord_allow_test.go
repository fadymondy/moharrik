package channels

import "testing"

// The allow-lists filter server (guild) traffic only: a DM to the bot always
// reaches the inbox, even when allowed_channel_ids is set.
func TestDiscordMessageAllowed(t *testing.T) {
	cases := []struct {
		name                    string
		guild, channel          string
		allowGuilds, allowChans string
		want                    bool
	}{
		{"dm, no lists", "", "DM1", "", "", true},
		{"dm, channel list set", "", "DM1", "", "C1,C2", true},
		{"dm, guild list set", "", "DM1", "G1", "C1", true},
		{"guild, no lists", "G1", "C9", "", "", true},
		{"guild, listed channel", "G1", "C2", "", "C1, C2", true},
		{"guild, unlisted channel", "G1", "C9", "", "C1,C2", false},
		{"guild, unlisted guild", "G2", "C1", "G1", "", false},
		{"guild, listed guild and channel", "G1", "C1", "G1", "C1", true},
	}
	for _, c := range cases {
		if got := discordMessageAllowed(c.guild, c.channel, c.allowGuilds, c.allowChans); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
