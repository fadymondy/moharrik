package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// fixtureSecret is the AUTH_SECRET the fixtures below were sealed under.
const fixtureSecret = "compat-fixture-secret-0123456789abcdef"

// Values sealed by the OLD per-package copies (internal/{inbox,whatsapp,
// marketing,notifications}/secret.go as of c9324c5), captured by running their
// seal() before they were deleted (MH-439). Production holds values sealed by
// exactly that code: if any of these stops opening, stored SMTP passwords,
// mailbox passwords, WhatsApp tokens and webhook secrets stop decrypting.
var oldCiphertexts = []struct {
	domain, plain, sealed string
}{
	{DomainMailboxPassword, "plain-inbox-secret", "eG6asWL52Jp8aXq2h8OiumMjjHIQ7+/qYV8rfHiMrdLPL4rssUz9tm9hY9ySuA=="},
	{DomainMailboxPassword, "ümlaut ✓ عربي inbox", "vfgDHqK5GFZ9B6kkR4wGo67RE5usq2m7HNsUmoQw7onyx9/S16AzztBDpyFq1KQZRXQiGu4/"},
	{DomainWhatsAppToken, "plain-whatsapp-secret", "UP3EwzFkukKkl7+DcRh9Ql020QguTfEDeQZaehhISQ6lXtMQZdh/oDZxWhpP3Ekocg=="},
	{DomainWhatsAppToken, "ümlaut ✓ عربي whatsapp", "qGflljrE0llUUcMaW+pRn50Ns7A7gJH60B7a0UoTFTir5kafjHSYoqzZEr9u171ju+CO8i72pJhc"},
	{DomainSMTP, "plain-marketing-secret", "Hfrh30JZfVn8+kwOMIkccSe3dUuim83loD1eNwL9135pf7MUwy3Bkz8j9tr5DcJ/iXE="},
	{DomainSMTP, "ümlaut ✓ عربي marketing", "/CFhyusk4PwL5eF3X+6oiHo20RwwK2cwTXReoEHJUspR6od8ZSsn6KX7qc5dTVhTrEfy6BCYfAZ/cw=="},
	{DomainWebhookSecret, "plain-notifications-secret", "4LhsOxFrv+ZSA9zbujere/1J/QsuXEMW574p/v3uahoflRXVrSf5RwpDxbiPRBIYxw86ApFH"},
	{DomainWebhookSecret, "ümlaut ✓ عربي notifications", "X82w6AKMkXubZc5RIz8qnFyL9viD4MctmCgeWjkpGpFlT7TYxts6D6kdL0uSR9nfqIHVvE87YzYjQ2/0lvU="},
}

func TestOpensValuesSealedByTheOldCopies(t *testing.T) {
	t.Setenv("AUTH_SECRET", fixtureSecret)
	for _, c := range oldCiphertexts {
		got, err := New(c.domain, "the value", "retry").Open(c.sealed)
		if err != nil {
			t.Errorf("%s: a value sealed by the old code no longer opens: %v", c.domain, err)
			continue
		}
		if got != c.plain {
			t.Errorf("%s: opened %q, want %q", c.domain, got, c.plain)
		}
	}
}

// The old copies can still open what the new package seals: a rollback to the
// previous release must not strand values written by this one.
func TestOldCodeOpensNewCiphertext(t *testing.T) {
	t.Setenv("AUTH_SECRET", fixtureSecret)
	for _, d := range []string{DomainSMTP, DomainMailboxPassword, DomainWhatsAppToken, DomainWebhookSecret} {
		sealed, err := New(d, "x", "y").Seal("round trip " + d)
		if err != nil {
			t.Fatal(err)
		}
		if got := oldUnseal(t, d, sealed); got != "round trip "+d {
			t.Errorf("%s: the previous construction read %q", d, got)
		}
	}
}

// oldUnseal is the deleted copies' construction, verbatim in substance.
func oldUnseal(t *testing.T, domain, sealed string) string {
	t.Helper()
	k := sha256.Sum256([]byte(domain + ":" + fixtureSecret))
	block, _ := aes.NewCipher(k[:])
	gcm, _ := cipher.NewGCM(block)
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatal(err)
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		t.Fatalf("%s: %v", domain, err)
	}
	return string(out)
}

func TestDomainsAreNotInterchangeable(t *testing.T) {
	t.Setenv("AUTH_SECRET", fixtureSecret)
	g := New(DomainGoogleRefresh, "the Google grant", "reconnect")
	sealed, err := g.Seal("1//refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{DomainSMTP, DomainMailboxPassword, DomainWhatsAppToken, DomainWebhookSecret} {
		if _, err := New(d, "x", "y").Open(sealed); err == nil {
			t.Errorf("a %s value opened under %s", DomainGoogleRefresh, d)
		}
	}
	if back, err := g.Open(sealed); err != nil || back != "1//refresh-token" {
		t.Fatalf("round trip: %q %v", back, err)
	}
}

func TestEmptyAndMissingSecret(t *testing.T) {
	t.Setenv("AUTH_SECRET", "")
	b := New(DomainSMTP, "the SMTP password", "enter it again")
	if s, err := b.Seal(""); s != "" || err != nil {
		t.Fatalf("empty seals to empty: %q %v", s, err)
	}
	if _, err := b.Seal("x"); err == nil || !strings.Contains(err.Error(), "AUTH_SECRET") {
		t.Fatalf("no AUTH_SECRET must refuse to seal: %v", err)
	}
}

func TestErrorsNeverCarryThePlaintextOrKey(t *testing.T) {
	t.Setenv("AUTH_SECRET", fixtureSecret)
	b := New(DomainGoogleRefresh, "the Google grant", "reconnect")
	sealed, _ := b.Seal("super-secret-refresh")
	t.Setenv("AUTH_SECRET", "a-different-secret-0123456789abcdef")
	_, err := b.Open(sealed)
	if err == nil {
		t.Fatal("a changed AUTH_SECRET must not open")
	}
	for _, leak := range []string{"super-secret-refresh", fixtureSecret, sealed} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error carries a secret: %v", err)
		}
	}
	if _, err := b.Open("!!not base64!!"); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("garbage must read as corrupt: %v", err)
	}
}
