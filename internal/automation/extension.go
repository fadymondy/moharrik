package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
The extension download (MH-399).

Pairing a browser always worked; getting the extension INTO a browser did not.
There was no build and nowhere to fetch one, so the only people who could
automate anything were the ones who could load the source directory by hand.

The image build packages extension/ (extension/build.mjs): a versioned zip and
build.json beside it, carrying the zip's sha256. Two routes serve them:

	GET /api/automation/extension           what the install page shows
	GET /api/automation/extension/download  the zip itself

Both are in the guard's SIGNED-IN tier, not the workspace tier: the zip is the
same for every workspace, and a plain <a href> download cannot send the
X-Workspace-ID header the workspace tier needs. Nothing in it is secret — the
token that makes a browser useful is minted only by an admin approving a
pairing — so "any signed-in person" is the right gate for the file.

THE CHECKSUM ON THE PAGE IS THE CHECKSUM OF THE FILE. build.json is written
from the exact bytes the packager produced, and the download handler hashes
the file on disk again before serving it. If the two disagree — a hand-copied
zip, a half-finished deploy — it refuses rather than serve a file that does not
match the number the page promised.
*/

// ExtensionBuild is extension/dist/build.json.
type ExtensionBuild struct {
	Version string   `json:"version"`
	File    string   `json:"file"`
	Bytes   int64    `json:"bytes"`
	SHA256  string   `json:"sha256"`
	Site    string   `json:"site"`
	API     string   `json:"api"`
	Dev     bool     `json:"dev"`
	Commit  string   `json:"commit"`
	BuiltAt string   `json:"built_at"`
	Files   []string `json:"files"`
	// The public half of the signing key, base64 SubjectPublicKeyInfo, baked
	// into the manifest as "key" so every install has the same id (MH-483).
	PublicKey string `json:"public_key,omitempty"`
	ID        string `json:"extension_id,omitempty"`
}

// ExtensionDir is where the packaged build lives: extension/dist under the
// working directory (/app in the image), or EXTENSION_DIR.
func ExtensionDir() string {
	if dir := strings.TrimSpace(os.Getenv("EXTENSION_DIR")); dir != "" {
		return dir
	}
	return filepath.Join("extension", "dist")
}

// LatestExtension reads the packaged build, or nil when there is none — or
// when it is a development build on a production server, which would install
// fine and then never connect to anything.
func LatestExtension() *ExtensionBuild {
	raw, err := os.ReadFile(filepath.Join(ExtensionDir(), "build.json"))
	if err != nil {
		return nil
	}
	var b ExtensionBuild
	if json.Unmarshal(raw, &b) != nil || b.File == "" || b.Version == "" || b.SHA256 == "" {
		return nil
	}
	if b.Dev && os.Getenv("APP_ENV") == "production" {
		return nil
	}
	return &b
}

// CompareVersions compares two dotted versions numerically: -1, 0 or 1.
// "0.10.0" is newer than "0.9.3", which a string comparison gets wrong. A part
// that is not a number counts as 0, so a garbled version reads as old rather
// than as newer than everything.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(strings.TrimSpace(a), "."), strings.Split(strings.TrimSpace(b), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

// Outdated reports whether a browser's extension is older than the build this
// server offers. An unknown version (a browser paired before it reported one)
// is outdated: whatever it is, it is not the current build.
func Outdated(reported string, latest *ExtensionBuild) bool {
	if latest == nil {
		return false
	}
	if strings.TrimSpace(reported) == "" {
		return true
	}
	return CompareVersions(reported, latest.Version) < 0
}

// ExtensionInfo is what the install page reads.
type ExtensionInfo struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty" doc:"Why there is nothing to download, when there is not."`
	Version   string `json:"version,omitempty"`
	File      string `json:"file,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty" doc:"Of the exact file the download route serves."`
	Site      string `json:"site,omitempty" doc:"The site this build pairs with by default."`
	Dev       bool   `json:"dev,omitempty" doc:"A development build, pointed at a local stack."`
	BuiltAt   string `json:"built_at,omitempty"`
	Commit    string `json:"commit,omitempty"`
	URL       string `json:"download_url,omitempty"`
	// The signed package a policy-managed Chrome installs and keeps current
	// (MH-483); false when this server has no signing key.
	Signed      bool   `json:"signed"`
	ExtensionID string `json:"extension_id,omitempty"`
	UpdateURL   string `json:"update_url,omitempty"`
}

const extensionDownloadPath = "/api/automation/extension/download"

// RegisterExtension mounts the build info. The download is a plain route
// (MountExtensionDownload): it streams a file, not a JSON body.
func RegisterExtension(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "extension-build", Method: http.MethodGet, Path: "/api/automation/extension",
		Summary: "The browser extension this server offers for download", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*body[ExtensionInfo], error) {
		if id, ok := auth.IdentityFrom(ctx); !ok || id == nil {
			return nil, huma.Error401Unauthorized("sign in to download the extension")
		}
		b := LatestExtension()
		if b == nil {
			// Said, not hidden: the page shows why there is nothing to download
			// instead of a button that 404s.
			return out(ExtensionInfo{Reason: "No extension has been packaged on this server yet."}), nil
		}
		info := ExtensionInfo{
			Available: true, Version: b.Version, File: filepath.Base(b.File), Bytes: b.Bytes, SHA256: b.SHA256,
			Site: b.Site, Dev: b.Dev, BuiltAt: b.BuiltAt, Commit: b.Commit, URL: extensionDownloadPath,
		}
		if _, _, id, err := SignedExtension(); err == nil {
			info.Signed, info.ExtensionID, info.UpdateURL = true, id, updateOrigin(b)+extensionUpdatesPath
		}
		return out(info), nil
	})

}

// MountExtensionDownload serves the zip itself.
func MountExtensionDownload(router chi.Router, a *app.App) {
	router.Get(extensionDownloadPath, func(w http.ResponseWriter, r *http.Request) {
		// The guard put this route in the signed-in tier; this is the handler
		// saying so too, so a change to the guard cannot open it by accident.
		if id, ok := auth.IdentityFrom(r.Context()); !ok || id == nil {
			writePlain(w, http.StatusUnauthorized, "sign in to download the extension")
			return
		}
		b := LatestExtension()
		if b == nil {
			writePlain(w, http.StatusNotFound, "no extension has been packaged on this server")
			return
		}
		// The name comes from build.json; it is still confined to the build
		// directory before anything is opened.
		dir, _ := filepath.Abs(ExtensionDir())
		full, err := filepath.Abs(filepath.Join(dir, filepath.Base(b.File)))
		if err != nil || !strings.HasPrefix(full, dir+string(os.PathSeparator)) {
			writePlain(w, http.StatusBadRequest, "bad build file")
			return
		}
		data, err := os.ReadFile(full)
		if err != nil {
			writePlain(w, http.StatusNotFound, "the packaged file is missing")
			return
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if !strings.EqualFold(actual, b.SHA256) {
			if a != nil {
				a.Log.Error("extension checksum mismatch", "expected", b.SHA256, "actual", actual)
			}
			writePlain(w, http.StatusConflict, "the packaged file does not match its recorded checksum, so it is not served")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(b.File)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		// The checksum travels with the file, so a script can verify it without
		// reading the page.
		w.Header().Set("X-Checksum-Sha256", actual)
		w.Header().Set("X-Extension-Version", b.Version)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		_, _ = w.Write(data)
	})
}

func writePlain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	raw, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(raw)
}
