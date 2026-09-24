package automation

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
Updating the extension by itself (MH-483).

A browser that loaded the zip by hand stays on that version until someone
notices. Chrome can update an extension by itself when it was installed from a
signed package (a CRX) whose manifest names an update_url: it fetches that URL,
reads the version offered, and installs the newer CRX. Three public routes make
that work:

	GET /api/automation/extension/updates.xml  the update manifest Chrome reads
	GET /api/automation/extension/moharrik.crx the signed package
	GET /api/automation/extension/latest       the version on offer, for the
	                                           extension's own "update
	                                           available" check

Public, because Chrome's updater and an extension that has not connected yet
carry no session. Nothing in them is secret — the zip inside is the same file
the signed-in download serves.

THE SIGNING KEY never touches the repository or the image. The image build packs
the zip (extension/build.mjs) with the PUBLIC half baked into the manifest, so
every install has the same extension id; the API signs the CRX in memory at the
first request, with the private half from EXTENSION_SIGNING_KEY. A key that does
not match the public half the build carries is refused: a CRX signed with the
wrong key has a different id, and Chrome would install it as a second
extension rather than an update.

Without the key there is no CRX and no updates.xml — said, not faked — and the
extension's own check still tells people a new version is out.

Chrome installs a CRX from outside the Web Store only where policy allows it
(ExtensionInstallForcelist / ExtensionInstallSources, i.e. a managed browser) or
on Linux. Everyone else gets the in-extension notice and a one-click download;
the Web Store listing (MH-484) is the path that updates everyone.
*/

// SigningKeyEnv holds the extension's private signing key: PEM (PKCS#8 or
// PKCS#1), or that PEM base64-encoded onto one line.
const SigningKeyEnv = "EXTENSION_SIGNING_KEY"

const (
	extensionUpdatesPath = "/api/automation/extension/updates.xml"
	extensionCRXPath     = "/api/automation/extension/moharrik.crx"
	extensionLatestPath  = "/api/automation/extension/latest"
)

// ExtensionID is Chrome's id for a public key: the first 16 bytes of the
// SHA-256 of its SubjectPublicKeyInfo, each hex digit written as a..p.
func ExtensionID(spki []byte) string {
	sum := sha256.Sum256(spki)
	var b strings.Builder
	for _, c := range hex.EncodeToString(sum[:16]) {
		v, _ := strconv.ParseUint(string(c), 16, 8)
		b.WriteByte(byte('a' + v))
	}
	return b.String()
}

// ParseSigningKey reads EXTENSION_SIGNING_KEY's value.
func ParseSigningKey(raw string) (*rsa.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("no key")
	}
	data := []byte(raw)
	if !strings.HasPrefix(raw, "-----BEGIN") {
		dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
		if err != nil {
			return nil, errors.New("the key is neither PEM nor base64")
		}
		data = dec
	}
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	}
	if k, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("the key is not an RSA key")
		}
		return rk, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(data); err == nil {
		return k, nil
	}
	return nil, errors.New("the key could not be parsed")
}

// PackCRX3 signs a zip as a CRX3 package: "Cr24", version 3, the header
// length, a CrxFileHeader protobuf, then the zip. The signature covers
// "CRX3 SignedData\x00", the signed header's length and bytes, and the zip.
func PackCRX3(zip []byte, key *rsa.PrivateKey) ([]byte, string, error) {
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	idSum := sha256.Sum256(spki)
	signedData := pbBytes(1, idSum[:16]) // SignedData{crx_id}

	h := sha256.New()
	h.Write([]byte("CRX3 SignedData\x00"))
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(signedData)))
	h.Write(n[:])
	h.Write(signedData)
	h.Write(zip)
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, h.Sum(nil))
	if err != nil {
		return nil, "", err
	}

	proof := append(pbBytes(1, spki), pbBytes(2, sig)...) // AsymmetricKeyProof
	header := append(pbBytes(2, proof), pbBytes(10000, signedData)...)

	var out bytes.Buffer
	out.WriteString("Cr24")
	binary.LittleEndian.PutUint32(n[:], 3)
	out.Write(n[:])
	binary.LittleEndian.PutUint32(n[:], uint32(len(header)))
	out.Write(n[:])
	out.Write(header)
	out.Write(zip)
	return out.Bytes(), ExtensionID(spki), nil
}

// pbBytes is one length-delimited protobuf field.
func pbBytes(field int, v []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(field)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(v)))
	return append(out, v...)
}

// signedBuild is the CRX for one packaged build, made once.
type signedBuild struct {
	sha string // the zip it was made from
	crx []byte
	id  string
	err error
}

var (
	crxMu    sync.Mutex
	crxCache *signedBuild
)

// SignedExtension is the CRX of the packaged build, signed with the key in
// EXTENSION_SIGNING_KEY — or why there is none.
func SignedExtension() (*ExtensionBuild, []byte, string, error) {
	b := LatestExtension()
	if b == nil {
		return nil, nil, "", errors.New("no extension has been packaged on this server")
	}
	crxMu.Lock()
	defer crxMu.Unlock()
	if crxCache != nil && crxCache.sha == b.SHA256 {
		return b, crxCache.crx, crxCache.id, crxCache.err
	}
	crx, id, err := signBuild(b)
	crxCache = &signedBuild{sha: b.SHA256, crx: crx, id: id, err: err}
	return b, crx, id, err
}

func signBuild(b *ExtensionBuild) ([]byte, string, error) {
	raw := os.Getenv(SigningKeyEnv)
	if strings.TrimSpace(raw) == "" {
		return nil, "", fmt.Errorf("%s is not set, so there is no signed package", SigningKeyEnv)
	}
	key, err := ParseSigningKey(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", SigningKeyEnv, err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	if b.PublicKey != "" && b.PublicKey != base64.StdEncoding.EncodeToString(spki) {
		return nil, "", fmt.Errorf("%s does not match the public key this build was packed with", SigningKeyEnv)
	}
	zip, err := readBuildZip(b)
	if err != nil {
		return nil, "", err
	}
	return PackCRX3(zip, key)
}

// readBuildZip reads the packaged zip, confined to the build directory and
// checked against build.json's checksum — the same rule as the download.
func readBuildZip(b *ExtensionBuild) ([]byte, error) {
	dir, _ := filepath.Abs(ExtensionDir())
	full, err := filepath.Abs(filepath.Join(dir, filepath.Base(b.File)))
	if err != nil || !strings.HasPrefix(full, dir+string(os.PathSeparator)) {
		return nil, errors.New("bad build file")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, errors.New("the packaged file is missing")
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), b.SHA256) {
		return nil, errors.New("the packaged file does not match its recorded checksum")
	}
	return data, nil
}

// updateOrigin is where Chrome fetches the CRX from: the origin the build
// talks to, which is this server.
func updateOrigin(b *ExtensionBuild) string {
	o := strings.TrimRight(b.API, "/")
	if o == "" {
		o = strings.TrimRight(b.Site, "/")
	}
	return o
}

// ExtensionLatest is what the extension's own update check reads.
type ExtensionLatest struct {
	Version   string `json:"version,omitempty"`
	Signed    bool   `json:"signed" doc:"A signed package and update manifest are served, so a policy-installed browser updates by itself."`
	ID        string `json:"extension_id,omitempty"`
	UpdateURL string `json:"update_url,omitempty"`
}

// MountExtensionUpdates serves the update manifest, the CRX and the latest
// version. All three are public (internal/server/guard.go).
func MountExtensionUpdates(router chi.Router, a *app.App) {
	router.Get(extensionLatestPath, func(w http.ResponseWriter, r *http.Request) {
		b, _, id, err := SignedExtension()
		out := ExtensionLatest{}
		if b != nil {
			out.Version = b.Version
			if err == nil {
				out.Signed, out.ID, out.UpdateURL = true, id, updateOrigin(b)+extensionUpdatesPath
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	router.Get(extensionUpdatesPath, func(w http.ResponseWriter, r *http.Request) {
		b, _, id, err := SignedExtension()
		if err != nil {
			logCRX(a, err)
			writePlain(w, http.StatusNotFound, "no signed extension is offered here")
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `<?xml version='1.0' encoding='UTF-8'?>
<gupdate xmlns='http://www.google.com/update2/response' protocol='2.0'>
  <app appid='%s'>
    <updatecheck codebase='%s' version='%s' />
  </app>
</gupdate>
`, html.EscapeString(id), html.EscapeString(updateOrigin(b)+extensionCRXPath), html.EscapeString(b.Version))
	})

	router.Get(extensionCRXPath, func(w http.ResponseWriter, r *http.Request) {
		b, crx, _, err := SignedExtension()
		if err != nil {
			logCRX(a, err)
			writePlain(w, http.StatusNotFound, "no signed extension is offered here")
			return
		}
		w.Header().Set("Content-Type", "application/x-chrome-extension")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="moharrik-%s.crx"`, b.Version))
		w.Header().Set("Content-Length", strconv.Itoa(len(crx)))
		w.Header().Set("X-Extension-Version", b.Version)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(crx)
	})
}

func logCRX(a *app.App, err error) {
	if a != nil && a.Log != nil {
		a.Log.Warn("signed extension unavailable", "err", err)
	}
}
