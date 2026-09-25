package channels

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// MH-593: a gateway close that only a change on Discord's side can fix says
// what to change, and is retried slowly; anything else keeps its own text.
func TestGatewayErrorReason(t *testing.T) {
	msg, fatal := gatewayErrorReason(fmt.Errorf("read: %w", &websocket.CloseError{Code: 4014, Text: "Disallowed intent(s)."}))
	if !fatal || !strings.Contains(msg, "Message Content Intent") || !strings.Contains(msg, "Privileged Gateway Intents") || len(msg) > 300 {
		t.Fatalf("4014: fatal=%v %q (%d chars)", fatal, msg, len(msg))
	}
	if msg, fatal = gatewayErrorReason(&websocket.CloseError{Code: 4004, Text: "Authentication failed."}); !fatal || !strings.Contains(msg, "token") {
		t.Fatalf("4004: fatal=%v %q", fatal, msg)
	}
	if msg, fatal = gatewayErrorReason(&websocket.CloseError{Code: 1006}); fatal || !strings.Contains(msg, "1006") {
		t.Fatalf("1006: fatal=%v %q", fatal, msg)
	}
	if msg, fatal = gatewayErrorReason(errors.New("no HELLO from the gateway: EOF")); fatal || msg != "no HELLO from the gateway: EOF" {
		t.Fatalf("plain: fatal=%v %q", fatal, msg)
	}
}
