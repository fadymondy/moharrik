/*
Package secret seals credentials at rest: AES-256-GCM under a key derived from
AUTH_SECRET and a per-use DOMAIN string, so a database dump alone hands out
nothing and two features' ciphertexts are never interchangeable.

It replaces five copies of the same construction (inbox, whatsapp, marketing,
notifications and the MCP webhook tool, MH-439). The construction is frozen,
because values sealed by those copies are stored in production:

	key        = sha256(domain + ":" + AUTH_SECRET)
	ciphertext = base64std(nonce(12) || GCM-seal(plaintext))
	""         seals to "" and opens to ""

secret_test.go opens values sealed by the old copies, one per domain. If that
test fails, stored SMTP, mailbox, WhatsApp and webhook secrets stop decrypting.

A Box never logs and never puts the plaintext in an error.
*/
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// The domains in use. Changing any of these strings makes every value stored
// under it unreadable.
const (
	DomainSMTP            = "moharrik/smtp/v1"
	DomainMailboxPassword = "moharrik/mailbox-password/v1"
	DomainWhatsAppToken   = "moharrik/whatsapp-token/v1"
	DomainWebhookSecret   = "moharrik/webhook-secret/v1"
	DomainGoogleRefresh   = "moharrik/google-refresh/v1"
	// The values a schedule keeps for its workflow's SECRET parameters (MH-403).
	DomainScheduleSecret = "moharrik/schedule-secret/v1"
)

// minSecret is the shortest AUTH_SECRET a key is derived from.
const minSecret = 16

// Box seals and opens values for one domain.
type Box struct {
	domain string
	// what names the value in errors ("the SMTP password"); remedy says what
	// the person should do when it can no longer be opened.
	what, remedy string
}

// New returns a Box for domain. what names the value in error messages
// ("the SMTP password"); remedy is the advice when it cannot be opened
// ("enter it again").
func New(domain, what, remedy string) Box {
	return Box{domain: domain, what: what, remedy: remedy}
}

// Domain is the Box's domain string.
func (b Box) Domain() string { return b.domain }

func (b Box) aead() (cipher.AEAD, error) {
	s := os.Getenv("AUTH_SECRET")
	if len(s) < minSecret {
		return nil, fmt.Errorf("AUTH_SECRET is not set; cannot seal %s", b.what)
	}
	k := sha256.Sum256([]byte(b.domain + ":" + s))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plain. The empty string seals to the empty string, so "none
// stored" survives a round trip without a special case at every caller.
func (b Box) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	gcm, err := b.aead()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts what Seal produced under the same domain and AUTH_SECRET.
func (b Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	gcm, err := b.aead()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("the stored %s is corrupt", trimThe(b.what))
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("%s cannot be unsealed (AUTH_SECRET changed?) — %s", b.what, b.remedy)
	}
	return string(out), nil
}

func trimThe(s string) string {
	if len(s) > 4 && s[:4] == "the " {
		return s[4:]
	}
	return s
}

// ErrRandom is returned when the system's random source fails.
var ErrRandom = errors.New("the random source failed")

// RandomHex returns n random bytes, hex-encoded (2n characters).
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", ErrRandom
	}
	return hex.EncodeToString(b), nil
}
