package marketing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
)

// The SMTP password is sealed at rest with AES-256-GCM under a key derived from
// AUTH_SECRET, so a database dump alone does not hand out a mailbox. It is never
// returned by the API.

func key() ([]byte, error) {
	s := os.Getenv("AUTH_SECRET")
	if len(s) < 16 {
		return nil, errors.New("AUTH_SECRET is not set; cannot seal SMTP credentials")
	}
	k := sha256.Sum256([]byte("moharrik/smtp/v1:" + s))
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
		return "", errors.New("sealed SMTP password is corrupt")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the SMTP password cannot be unsealed (AUTH_SECRET changed?) — enter it again")
	}
	return string(out), nil
}
