package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

// connect_discord_channel created connections with has_token:false: its
// arguments were an untagged struct, so "bot_token" never matched BotToken
// (encoding/json ignores case but not underscores) and only "name" bound.
// Every property a connect tool publishes must land in a field of its args.
func TestChannelConnectToolsBindEveryArgument(t *testing.T) {
	args := map[string]any{
		"connect_telegram_channel": &telegramConnectArgs{},
		"connect_slack_channel":    &slackConnectArgs{},
		"connect_discord_channel":  &discordConnectArgs{},
		"connect_workflow_channel": &workflowConnectArgs{},
	}
	tools := map[string]Tool{}
	for _, tl := range ChannelTools() {
		tools[tl.Name] = tl
	}
	for name, into := range args {
		tl, ok := tools[name]
		if !ok {
			t.Fatalf("%s is not published", name)
		}
		props, _ := tl.InputSchema["properties"].(map[string]any)
		payload := map[string]any{}
		for k, p := range props {
			if typ, _ := p.(map[string]any)["type"].(string); typ == "boolean" {
				payload[k] = true
			} else {
				payload[k] = "v-" + k
			}
		}
		raw, _ := json.Marshal(payload)
		if err := bind(raw, into); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		v := reflect.ValueOf(into).Elem()
		if v.NumField() != len(props) {
			t.Errorf("%s: %d args fields for %d published properties", name, v.NumField(), len(props))
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).IsZero() {
				t.Errorf("%s: %s was not bound from %s", name, v.Type().Field(i).Name, raw)
			}
		}
	}
}

func TestDiscordConnectBindsTokenAndAllowLists(t *testing.T) {
	var in discordConnectArgs
	raw := `{"name":"Fady","bot_token":"tok","application_id":"123","allowed_channel_ids":"1,2"}`
	if err := bind(json.RawMessage(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.BotToken != "tok" || in.ApplicationID != "123" || in.AllowedChannelIDs != "1,2" {
		t.Fatalf("got %+v", in)
	}
}
