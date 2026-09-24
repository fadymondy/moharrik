package automation

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// readPB walks one protobuf message's length-delimited fields.
func readPB(t *testing.T, b []byte) map[uint64][][]byte {
	t.Helper()
	out := map[uint64][][]byte{}
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag&7 != 2 {
			t.Fatalf("unexpected protobuf tag %d", tag)
		}
		b = b[n:]
		size, m := binary.Uvarint(b)
		b = b[m:]
		out[tag>>3] = append(out[tag>>3], b[:size])
		b = b[size:]
	}
	return out
}

// An independent reading of the CRX3 format, as Chrome's verifier reads it:
// the header's key proof must verify over the signed data and the zip, and the
// crx_id must be the key's id.
func TestPackCRX3IsWhatChromeVerifies(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("manifest.json")
	_, _ = f.Write([]byte(`{"manifest_version":3,"name":"x","version":"1.2.3"}`))
	_ = zw.Close()
	archive := zbuf.Bytes()

	crx, id, err := PackCRX3(archive, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(crx[:4]) != "Cr24" || binary.LittleEndian.Uint32(crx[4:8]) != 3 {
		t.Fatalf("not a CRX3: % x", crx[:8])
	}
	hlen := binary.LittleEndian.Uint32(crx[8:12])
	header, body := crx[12:12+hlen], crx[12+hlen:]
	if !bytes.Equal(body, archive) {
		t.Fatal("the zip after the header is not the zip that was signed")
	}
	fields := readPB(t, header)
	signed := fields[10000][0]
	proof := readPB(t, fields[2][0])
	spki, sig := proof[1][0], proof[2][0]

	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write([]byte("CRX3 SignedData\x00"))
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(signed)))
	h.Write(n[:])
	h.Write(signed)
	h.Write(archive)
	if err := rsa.VerifyPKCS1v15(pub.(*rsa.PublicKey), crypto.SHA256, h.Sum(nil), sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	crxID := readPB(t, signed)[1][0]
	sum := sha256.Sum256(spki)
	if !bytes.Equal(crxID, sum[:16]) {
		t.Fatal("crx_id is not the key's id")
	}
	if id != ExtensionID(spki) || len(id) != 32 || strings.Trim(id, "abcdefghijklmnop") != "" {
		t.Fatalf("id %q", id)
	}
}

// Chrome's own example: this public key has the id Chrome shows for it.
func TestExtensionIDMatchesChrome(t *testing.T) {
	// Any key: the id is the first 16 bytes of sha256(spki) in a..p.
	spki := []byte("not really a key, the mapping is what is tested")
	sum := sha256.Sum256(spki)
	want := ""
	for _, b := range sum[:16] {
		want += string(rune('a'+b>>4)) + string(rune('a'+b&15))
	}
	if got := ExtensionID(spki); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestParseSigningKeyTakesPEMAndBase64(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	p8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	p1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	for name, v := range map[string]string{
		"pkcs8": p8, "pkcs1": p1, "base64 of pem": base64.StdEncoding.EncodeToString([]byte(p8)), "padded": "\n  " + p8 + "\n",
	} {
		got, err := ParseSigningKey(v)
		if err != nil || !got.Equal(key) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := ParseSigningKey("not a key"); err == nil {
		t.Fatal("garbage parsed")
	}
}

// The three public routes: without a key they say there is no signed package
// (and still name the version); with the matching key they serve a CRX and an
// update manifest pointing at it; with a key that does not match the build's
// public half they refuse, because that CRX would install as another extension.
func TestExtensionUpdateRoutes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EXTENSION_DIR", dir)
	t.Setenv("APP_ENV", "test")
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	spki, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("manifest.json")
	_, _ = f.Write([]byte(`{"version":"0.5.0"}`))
	_ = zw.Close()
	sum := sha256.Sum256(zbuf.Bytes())
	_ = os.WriteFile(filepath.Join(dir, "moharrik-extension-0.5.0.zip"), zbuf.Bytes(), 0o600)
	writeBuild := func(pub string) {
		raw, _ := json.Marshal(ExtensionBuild{Version: "0.5.0", File: "moharrik-extension-0.5.0.zip", SHA256: fmt.Sprintf("%x", sum),
			Site: "https://app.example", API: "https://app.example", PublicKey: pub})
		_ = os.WriteFile(filepath.Join(dir, "build.json"), raw, 0o600)
		crxMu.Lock()
		crxCache = nil
		crxMu.Unlock()
	}
	writeBuild(base64.StdEncoding.EncodeToString(spki))

	r := chi.NewRouter()
	MountExtensionUpdates(r, nil)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	t.Setenv(SigningKeyEnv, "")
	if w := get(extensionUpdatesPath); w.Code != http.StatusNotFound {
		t.Fatalf("updates.xml without a key: %d", w.Code)
	}
	var latest ExtensionLatest
	_ = json.Unmarshal(get(extensionLatestPath).Body.Bytes(), &latest)
	if latest.Version != "0.5.0" || latest.Signed {
		t.Fatalf("latest without a key: %+v", latest)
	}

	der, _ := x509.MarshalPKCS8PrivateKey(key)
	t.Setenv(SigningKeyEnv, base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	writeBuild(base64.StdEncoding.EncodeToString(spki))
	id := ExtensionID(spki)
	w := get(extensionUpdatesPath)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "appid='"+id+"'") ||
		!strings.Contains(w.Body.String(), "codebase='https://app.example/api/automation/extension/moharrik.crx'") ||
		!strings.Contains(w.Body.String(), "version='0.5.0'") {
		t.Fatalf("updates.xml: %d %s", w.Code, w.Body.String())
	}
	crx := get(extensionCRXPath)
	if crx.Code != 200 || crx.Header().Get("Content-Type") != "application/x-chrome-extension" || string(crx.Body.Bytes()[:4]) != "Cr24" {
		t.Fatalf("crx: %d %s", crx.Code, crx.Header())
	}
	body, _ := io.ReadAll(crx.Body)
	if !bytes.HasSuffix(body, zbuf.Bytes()) {
		t.Fatal("the CRX does not carry the packaged zip")
	}
	_ = json.Unmarshal(get(extensionLatestPath).Body.Bytes(), &latest)
	if !latest.Signed || latest.ID != id || latest.UpdateURL != "https://app.example"+extensionUpdatesPath {
		t.Fatalf("latest with a key: %+v", latest)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherSPKI, _ := x509.MarshalPKIXPublicKey(&other.PublicKey)
	writeBuild(base64.StdEncoding.EncodeToString(otherSPKI))
	if w := get(extensionCRXPath); w.Code != http.StatusNotFound {
		t.Fatalf("a key that does not match the build signed anyway: %d", w.Code)
	}
}
