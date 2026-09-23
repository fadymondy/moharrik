package alerts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
)

// A push subscription's keys (p256dh + auth) are sealed at rest with
// AES-256-GCM under a key derived from AUTH_SECRET — the construction
// internal/notifications and internal/marketing use for their secrets, with
// its own domain string so no two ciphertexts are ever interchangeable.
//
// Why seal them at all: with the keys, anyone holding this server's VAPID key
// could write a notification this member's browser would display as Moharrik.
// A database dump alone should not be enough.

func key() ([]byte, error) {
	s := os.Getenv("AUTH_SECRET")
	if len(s) < 16 {
		return nil, errors.New("AUTH_SECRET is not set; cannot seal push keys")
	}
	k := sha256.Sum256([]byte("moharrik/push-keys/v1:" + s))
	return k[:], nil
}

func seal(plain string) (string, error) {
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
		return "", errors.New("sealed push keys are corrupt")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the push keys cannot be unsealed (AUTH_SECRET changed?) — enable push on the device again")
	}
	return string(out), nil
}
