package notifications

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
)

// A webhook source's signing secret is sealed at rest with AES-256-GCM under a
// key derived from AUTH_SECRET — the same construction as internal/marketing's
// SMTP password (internal/marketing/secret.go), with its own domain string so
// the two ciphertexts are never interchangeable. It is shown once, at creation
// or rotation, and never returned by the API again.

func key() ([]byte, error) {
	s := os.Getenv("AUTH_SECRET")
	if len(s) < 16 {
		return nil, errors.New("AUTH_SECRET is not set; cannot seal webhook secrets")
	}
	k := sha256.Sum256([]byte("moharrik/webhook-secret/v1:" + s))
	return k[:], nil
}

func seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	k, err := key()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func unseal(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	k, err := key()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("sealed webhook secret is corrupt")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the webhook secret cannot be unsealed (AUTH_SECRET changed?) — rotate it")
	}
	return string(out), nil
}

// newSecret makes a 32-byte random signing secret, hex-encoded (64 chars) —
// long enough that a curl example fits on one line and unambiguous in a URL
// or a shell without quoting tricks.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
