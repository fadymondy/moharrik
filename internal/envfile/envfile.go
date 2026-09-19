// Package envfile loads KEY=VALUE lines from .env into the process
// environment. togo's LoadConfig (togo@v0.21.0 config.go) only reads
// os.Getenv, so `go run ./cmd/api` would otherwise start without the
// database URLs. Variables already set in the environment win.
package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Load reads path if it exists. Missing file is not an error.
func Load(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		val = strings.TrimSpace(val)
		// Strip an inline comment on an unquoted value: FOO=bar   # note
		if !strings.HasPrefix(val, `"`) && !strings.HasPrefix(val, `'`) {
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		val = strings.Trim(val, `"'`)
		if _, set := os.LookupEnv(key); !set {
			_ = os.Setenv(key, val)
		}
	}
}
