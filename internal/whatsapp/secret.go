package whatsapp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
)

// A WhatsApp webhook token is sealed at rest with AES-256-GCM under a key
// derived from AUTH_SECRET — the same construction as the SMTP password, the
// mailbox password and a webhook signing secret, each with its OWN domain
// string so the ciphertexts are never interchangeable.
//
// This token is the whole authorization for a public endpoint, so it is never
// returned by any API: it goes from here to Evolution's webhook headers and
// nowhere else.

func key() ([]byte, error) {
	s := os.Getenv("AUTH_SECRET")
	if len(s) < 16 {
		return nil, errors.New("AUTH_SECRET is not set; cannot seal WhatsApp webhook tokens")
	}
	k := sha256.Sum256([]byte("moharrik/whatsapp-token/v1:" + s))
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
		return "", errors.New("the stored WhatsApp token is corrupt")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the WhatsApp token cannot be unsealed (AUTH_SECRET changed?) — link the number again")
	}
	return string(out), nil
}
