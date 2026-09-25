// Package photourl is the pure, dependency-free half of MH-602/MH-588's
// signed photo URLs: given a workspace, a row kind/id and what that row
// currently has (an uploaded avatar id, or a cached provider photo), it
// builds the SAME signed /api/contact-avatars/ or /api/provider-photos/ link
// internal/crm.RegisterAvatars/RegisterProviderPhotos serve.
//
// It exists as its own leaf package (no DB, no app.App) so that BOTH
// internal/crm (which owns the download endpoints and the DB-backed upload
// flow) and internal/marketing (the contacts list, MH-588's "avatars on the
// contacts list" requirement) can call it without an import cycle —
// internal/crm already imports internal/marketing for its own contact
// helpers, so internal/marketing cannot import internal/crm back.
// internal/crm.AvatarURLLite/AvatarURL/ProviderPhotoURL/ContactAvatarURL
// remain as thin re-exports of these so every existing caller
// (internal/inbox, internal/alerts) keeps working unchanged.
package photourl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const avatarURLTTL = 6 * time.Hour

// ProviderPhotoKinds names the two rows a provider-cached photo can live on: a
// linked contact, or an unlinked conversation. The value is the table the
// signed provider-photo endpoint re-reads photo_url from.
var ProviderPhotoKinds = map[string]string{
	"contact":      "contacts",
	"conversation": "conversations",
}

// AvatarURLLite is the list-safe form (MH-602 defect 2): it takes a
// hasProviderPhoto flag instead of the actual photo_url, so a list query
// never has to select the data: URI (30-80KB apiece) just to decide whether
// to link to it. kind and id say which row (contact or conversation) the
// signed endpoint should re-read photo_url from.
func AvatarURLLite(workspaceID, kind, id, avatarID string, hasProviderPhoto bool, externalPhotoURL string) string {
	if avatarID != "" {
		return ContactAvatarURL(workspaceID, avatarID)
	}
	if hasProviderPhoto {
		return ProviderPhotoURL(workspaceID, kind, id)
	}
	return externalPhotoURL
}

// AvatarURL is AvatarURLLite for a caller that already has photoURL in hand
// (a single-row read, not a list) — it still never lets a data: URI itself
// out as JSON.
func AvatarURL(workspaceID, kind, id, avatarID, photoURL string) string {
	return AvatarURLLite(workspaceID, kind, id, avatarID, strings.HasPrefix(photoURL, "data:"), photoURL)
}

// SignAvatar is the raw signature (no URL wrapper) behind ContactAvatarURL —
// exported so internal/crm's tests can mint an arbitrary-exp signature
// directly, the same way its own pre-refactor signAvatar did.
func SignAvatar(workspaceID, avatarID string, exp int64) (string, error) {
	return signAvatar(workspaceID, avatarID, exp)
}

func signAvatar(workspaceID, avatarID string, exp int64) (string, error) {
	secret := os.Getenv("AUTH_SECRET")
	if len(secret) < 16 {
		return "", errors.New("AUTH_SECRET is not set; cannot sign an avatar URL")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-avatar:%s:%s:%d", workspaceID, avatarID, exp)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// ContactAvatarURL builds one uploaded avatar's signed download URL.
func ContactAvatarURL(workspaceID, avatarID string) string {
	exp := time.Now().Add(avatarURLTTL).Unix()
	sig, err := signAvatar(workspaceID, avatarID, exp)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("/api/contact-avatars/%s?ws=%s&exp=%d&sig=%s", avatarID, workspaceID, exp, sig)
}

// VerifyAvatarURL checks a contact-avatar signature (used by
// internal/crm.RegisterAvatars).
func VerifyAvatarURL(workspaceID, avatarID, expStr, sig string) error {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("bad expiry")
	}
	if time.Now().Unix() > exp {
		return errors.New("this link has expired")
	}
	want, err := signAvatar(workspaceID, avatarID, exp)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return errors.New("bad signature")
	}
	return nil
}

// SignProviderPhoto is the raw signature behind ProviderPhotoURL — exported
// so internal/crm's tests can mint an arbitrary-exp signature directly, the
// same way its own pre-refactor signProviderPhoto did.
func SignProviderPhoto(kind, workspaceID, id string, exp int64) (string, error) {
	return signProviderPhoto(kind, workspaceID, id, exp)
}

func signProviderPhoto(kind, workspaceID, id string, exp int64) (string, error) {
	secret := os.Getenv("AUTH_SECRET")
	if len(secret) < 16 {
		return "", errors.New("AUTH_SECRET is not set; cannot sign a provider-photo URL")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	// A distinct domain string from signAvatar's "contact-avatar:...": the two
	// endpoints read different tables by id, so a contact-avatar signature
	// must never be replayable here, or vice versa (MH-602 defect 2).
	fmt.Fprintf(mac, "provider-photo:%s:%s:%s:%d", kind, workspaceID, id, exp)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// ProviderPhotoURL builds a provider-cached photo's signed download URL —
// AvatarURLLite's answer whenever a row's photo_url is a data: URI, so the
// bytes never round-trip through a JSON response again (MH-602 defect 2).
func ProviderPhotoURL(workspaceID, kind, id string) string {
	if _, ok := ProviderPhotoKinds[kind]; !ok || id == "" {
		return ""
	}
	exp := time.Now().Add(avatarURLTTL).Unix()
	sig, err := signProviderPhoto(kind, workspaceID, id, exp)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("/api/provider-photos/%s/%s?ws=%s&exp=%d&sig=%s", kind, id, workspaceID, exp, sig)
}

// VerifyProviderPhotoURL checks a provider-photo signature (used by
// internal/crm.RegisterProviderPhotos).
func VerifyProviderPhotoURL(kind, workspaceID, id, expStr, sig string) error {
	if _, ok := ProviderPhotoKinds[kind]; !ok {
		return errors.New("unknown provider-photo kind")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("bad expiry")
	}
	if time.Now().Unix() > exp {
		return errors.New("this link has expired")
	}
	want, err := signProviderPhoto(kind, workspaceID, id, exp)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return errors.New("bad signature")
	}
	return nil
}

// ParseDataURI splits a data: URI (internal/whatsapp/avatar.go's cache
// format, always base64) into its content type and decoded bytes.
func ParseDataURI(s string) (string, []byte, error) {
	if !strings.HasPrefix(s, "data:") {
		return "", nil, errors.New("not a data URI")
	}
	rest := s[len("data:"):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return "", nil, errors.New("malformed data URI")
	}
	meta, payload := rest[:comma], rest[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, errors.New("only base64 data URIs are supported")
	}
	contentType := strings.TrimSuffix(meta, ";base64")
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, errors.New("bad base64 in a data URI")
	}
	return contentType, data, nil
}
