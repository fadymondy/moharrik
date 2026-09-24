package inbox

import (
	"fmt"
	"net"
	"net/smtp"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/client"
)

/*
Two mailboxes, a real IMAP server, real wire protocol (MH-555): a local
GreenMail container (greenmail/standalone) stands in for two mail accounts —
never a real one, per the card's own instruction — seeded over its SMTP port
and polled through the actual Poll()/dial()/Login()/Search()/Fetch() path,
the one thing mailbox_db_test.go's File()-level tests cannot reach because
they never open a socket.

GreenMail's dynamic-user mode (-Dgreenmail.auth.disabled) accepts SMTP mail
for any local address without it existing first, and IMAP LOGIN with any
password for that same address — which is what lets one container stand in
for two independent mailboxes (support@test.local, sales@test.local) with no
setup step of its own.

	docker run --rm -p 3143 -p 3025 greenmail/standalone:2.1.0

Skips only if the docker CLI itself is not on PATH — everything else here is
a hard failure, not a skip: this environment is expected to have Docker (the
card asks for it explicitly), so a missing container or a protocol mismatch
is a real result, not "unavailable, move on".
*/

type greenmail struct {
	imapAddr, smtpAddr string
	name               string
}

func startGreenMail(t *testing.T) *greenmail {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is not on PATH — this test needs it (MH-555 asks for a real IMAP server in docker, never a real account)")
	}
	name := "gm-test-" + idSuffix()
	run := exec.Command("docker", "run", "-d", "--rm", "--name", name,
		"-p", "127.0.0.1::3143", "-p", "127.0.0.1::3025",
		"-e", "GREENMAIL_OPTS=-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.auth.disabled",
		"greenmail/standalone:2.1.0")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("docker run greenmail: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "stop", name).Run()
	})

	imapAddr := dockerPort(t, name, "3143/tcp")
	smtpAddr := dockerPort(t, name, "3025/tcp")

	// SMTP: a TCP connect is enough — sending mail needs nothing more than
	// the socket being open.
	waitForTCP(t, name, smtpAddr)

	// IMAP: a TCP connect is NOT enough. The port opens (and this container's
	// own SMTP send above can succeed) well before GreenMail's internal user
	// manager is ready to answer LOGIN — a bare readiness check on the socket
	// raced this and got "Invalid login/password" on a server that a moment
	// later logged in fine. So this retries an actual LOGIN with a throwaway
	// account until IT succeeds, which is the only signal that means "ready".
	deadline := time.Now().Add(30 * time.Second)
	for {
		c, err := client.Dial(imapAddr)
		if err == nil {
			err = c.Login("readiness-probe@test.local", "anything")
			_ = c.Logout()
		}
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("greenmail's IMAP LOGIN never succeeded: %v\ncontainer logs:\n%s", err, out)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return &greenmail{imapAddr: imapAddr, smtpAddr: smtpAddr, name: name}
}

func waitForTCP(t *testing.T, name, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			return
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("greenmail never opened %s: %v\ncontainer logs:\n%s", addr, err, out)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func dockerPort(t *testing.T, name, port string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := exec.Command("docker", "port", name, port).CombinedOutput()
		addr := strings.TrimSpace(string(out))
		if err == nil && addr != "" {
			// "docker port" answers "0.0.0.0:54321\n[::]:54321" on some
			// setups; the first line's host part is always usable from this
			// process.
			return "127.0.0.1:" + addr[strings.LastIndex(addr, ":")+1:]
		}
		if time.Now().After(deadline) {
			t.Fatalf("docker port %s %s: %v\n%s", name, port, err, out)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// seedSMTP injects one RFC 5322 message into the container, addressed to
// `to` — GreenMail's dynamic-user mode files it for that address with no
// account having to exist first, the same way a real inbound delivery would.
func (g *greenmail) seedSMTP(t *testing.T, from, to, msg string) {
	t.Helper()
	c, err := smtp.Dial(g.smtpAddr)
	if err != nil {
		t.Fatalf("dial greenmail smtp: %v", err)
	}
	defer c.Close()
	if err := c.Mail(from); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(to); err != nil {
		t.Fatal(err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Quit(); err != nil {
		t.Fatal(err)
	}
}

func rawMessage(from, to, subject, msgID, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-Id: <%s>\r\nDate: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from, to, subject, msgID, time.Now().Format(time.RFC1123Z), body)
}

// TestPollAgainstRealIMAPServer is the MH-555 docker/GreenMail test: two
// mailboxes on one container, a poll of each over the real wire protocol,
// cross-mailbox dedupe, and the "the mailbox itself is the queue" idempotency
// (a second poll finds nothing new because the first one marked \Seen).
func TestPollAgainstRealIMAPServer(t *testing.T) {
	gm := startGreenMail(t)
	host, portStr, err := net.SplitHostPort(gm.imapAddr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatal(err)
	}

	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	supportID := f.mailbox(t, "Support", true, "")
	salesID := f.mailbox(t, "Sales", false, "")

	supportMB, err := loadMailboxByID(f.ctx, a, f.wsID, supportID)
	if err != nil {
		t.Fatal(err)
	}
	supportMB.Host, supportMB.Port, supportMB.TLSMode, supportMB.Username, supportMB.Password = host, port, "none", "support@test.local", "anything"
	salesMB, err := loadMailboxByID(f.ctx, a, f.wsID, salesID)
	if err != nil {
		t.Fatal(err)
	}
	salesMB.Host, salesMB.Port, salesMB.TLSMode, salesMB.Username, salesMB.Password = host, port, "none", "sales@test.local", "anything"

	// Connection test (MH-555's own bullet), against the real server, before
	// anything is seeded.
	if err := TestConnection(f.ctx, *supportMB); err != nil {
		t.Fatalf("TestConnection against a real, reachable, empty mailbox failed: %v", err)
	}

	from := "customer-" + idSuffix() + "@buyer.test"
	gm.seedSMTP(t, from, "support@test.local", rawMessage(from, "support@test.local", "Order #99", "poll-1-"+idSuffix()+"@buyer.test", "First message, over real IMAP"))
	// A second, unrelated-looking message to the OTHER mailbox, same sender,
	// same normalised subject — the cross-mailbox thread/dedupe case.
	gm.seedSMTP(t, from, "sales@test.local", rawMessage(from, "sales@test.local", "Re: Order #99", "poll-2-"+idSuffix()+"@buyer.test", "Second message, over real IMAP, different mailbox"))

	filed1, err := Poll(f.ctx, a, *supportMB)
	if err != nil {
		t.Fatalf("Poll(support): %v", err)
	}
	if filed1 != 1 {
		t.Fatalf("Poll(support) filed %d, want 1", filed1)
	}
	filed2, err := Poll(f.ctx, a, *salesMB)
	if err != nil {
		t.Fatalf("Poll(sales): %v", err)
	}
	if filed2 != 1 {
		t.Fatalf("Poll(sales) filed %d, want 1", filed2)
	}

	var convCount, msgCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND customer_email = $2`, f.wsID, from).Scan(&convCount); err != nil {
		t.Fatal(err)
	}
	if convCount != 1 {
		t.Fatalf("polling two mailboxes for the same customer made %d conversations, want 1", convCount)
	}
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id)
		FROM conversations c WHERE workspace_id = $1 AND customer_email = $2`, f.wsID, from).Scan(&msgCount); err != nil {
		t.Fatal(err)
	}
	if msgCount != 2 {
		t.Fatalf("messages in the merged thread = %d, want 2", msgCount)
	}

	// Idempotency: GreenMail marked both messages \Seen when Poll fetched
	// them (Poll's own STORE call), so polling again must find nothing new —
	// this is "the mailbox is the queue" (imap.go's doc) actually holding
	// against a real server, not just the ON CONFLICT DO NOTHING in File().
	filed1b, err := Poll(f.ctx, a, *supportMB)
	if err != nil {
		t.Fatalf("second Poll(support): %v", err)
	}
	if filed1b != 0 {
		t.Fatalf("a second poll of an already-collected mailbox filed %d, want 0 (messages were not marked \\Seen)", filed1b)
	}
}
