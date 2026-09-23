package server

/*
TestExtensionDownload — the extension install path (MH-399), through the real
stack: the guard's signed-in tier, the build info, the download, and the
browsers list flagging a browser whose extension is older than the build.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ExtensionDownload -v
*/

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func writeBuild(t *testing.T, dir, version string, zip []byte, recordedSum string) {
	t.Helper()
	name := "moharrik-extension-" + version + ".zip"
	if err := os.WriteFile(filepath.Join(dir, name), zip, 0o644); err != nil {
		t.Fatal(err)
	}
	if recordedSum == "" {
		s := sha256.Sum256(zip)
		recordedSum = hex.EncodeToString(s[:])
	}
	info, _ := json.Marshal(map[string]any{
		"version": version, "file": name, "bytes": len(zip), "sha256": recordedSum,
		"site": "https://app.moharrik.com", "dev": false,
	})
	if err := os.WriteFile(filepath.Join(dir, "build.json"), info, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionDownload(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "extension-test-secret-extension-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	dir := t.TempDir()
	t.Setenv("EXTENSION_DIR", dir)

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "ext-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	anon := newClient(t, srv.URL)

	// Nothing packaged: said, not a 404 behind a button.
	info := alice.must("GET", "/api/automation/extension", nil, 200)
	if info["available"] != false || info["reason"] == "" {
		t.Fatalf("with no build: %v", info)
	}
	if code, _ := alice.do("GET", "/api/automation/extension/download", nil); code != 404 {
		t.Fatalf("download with no build: %d, want 404", code)
	}

	zip := []byte("PK\x03\x04 not really a zip, but these exact bytes " + rnd())
	sum := sha256.Sum256(zip)
	want := hex.EncodeToString(sum[:])
	writeBuild(t, dir, "0.2.0", zip, "")

	// Signed in, no workspace needed.
	info = alice.must("GET", "/api/automation/extension", nil, 200)
	if info["available"] != true || info["version"] != "0.2.0" || info["sha256"] != want || info["download_url"] != "/api/automation/extension/download" {
		t.Fatalf("build info: %v", info)
	}
	// Anonymous: the guard's signed-in tier.
	for _, p := range []string{"/api/automation/extension", "/api/automation/extension/download"} {
		if code, _ := anon.do("GET", p, nil); code != 401 {
			t.Errorf("anonymous %s: %d, want 401", p, code)
		}
	}

	// The file served is the file whose checksum the page shows.
	req, _ := http.NewRequest("GET", srv.URL+"/api/automation/extension/download", nil)
	res, err := alice.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	gotSum := sha256.Sum256(got)
	if res.StatusCode != 200 || hex.EncodeToString(gotSum[:]) != want || res.Header.Get("X-Checksum-Sha256") != want {
		t.Fatalf("download: %d sum %x header %q, want %s", res.StatusCode, gotSum, res.Header.Get("X-Checksum-Sha256"), want)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="moharrik-extension-0.2.0.zip"` {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// A file that no longer matches its recorded checksum is refused, not served.
	writeBuild(t, dir, "0.2.0", zip, "00"+want[2:])
	if code, _ := alice.do("GET", "/api/automation/extension/download", nil); code != 409 {
		t.Fatalf("mismatched checksum: %d, want 409", code)
	}
	writeBuild(t, dir, "0.2.0", zip, "")

	// The browsers list flags a browser on an older extension, and not one on
	// the current build.
	ws := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Ext " + rnd()}, 201)["workspace"].(map[string]any)
	wsID := ws["id"].(string)
	for _, v := range []string{"0.1.2", "0.2.0", "0.10.0"} {
		if _, err := a.SysDB.Exec(`
			INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, extension_version)
			VALUES ($1, $2, $3, '', $2)`, wsID, v, "ext-"+v+"-"+rnd()); err != nil {
			t.Fatal(err)
		}
	}
	alice.ws = wsID
	list := alice.must("GET", "/api/w/browsers", nil, 200)
	if list["latest_extension_version"] != "0.2.0" {
		t.Fatalf("latest_extension_version = %v", list["latest_extension_version"])
	}
	flags := map[string]bool{}
	for _, b := range list["browsers"].([]any) {
		m := b.(map[string]any)
		flags[m["name"].(string)] = m["update_available"].(bool)
	}
	if !flags["0.1.2"] || flags["0.2.0"] || flags["0.10.0"] {
		t.Fatalf("update_available by version: %v (want only 0.1.2 flagged; 0.10.0 is newer, not older)", flags)
	}
}
