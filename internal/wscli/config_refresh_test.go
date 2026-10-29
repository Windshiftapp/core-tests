//go:build test

package wscli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Refreshing aliases must not copy the effective token into the project file.
// Regression: it wrote cfg.Server, which carries the global or environment
// token, into ./ws.toml.
func TestConfigRefreshDoesNotCopyGlobalTokenIntoProjectFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/v2/workspaces":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": 7, "key": "ENG", "name": "Engineering"}},
			})
		case "/rest/api/v2/workspaces/7/statuses":
			_ = json.NewEncoder(w).Encode(v2StatusDocument([]Status{{ID: 3, Name: "Done", IsCompleted: true}}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	home := t.TempDir()
	writeGlobalConfig(t, home, server.URL, "crw_global_secret")
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ws.toml"), "[defaults]\nworkspace_key = \"ENG\"\n")

	t.Setenv("HOME", home)
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"config", "refresh"}, strings.NewReader(""), &out, &errOut, emptyEnv())
	if code != 0 {
		t.Fatalf("config refresh returned %d: %s", code, errOut.String())
	}

	raw, err := os.ReadFile(filepath.Join(dir, "ws.toml"))
	if err != nil {
		t.Fatalf("read ws.toml: %v", err)
	}
	if strings.Contains(string(raw), "crw_global_secret") {
		t.Fatalf("config refresh wrote the global token into the project file:\n%s", raw)
	}
	if !strings.Contains(string(raw), "status_aliases") {
		t.Fatalf("config refresh did not write status aliases:\n%s", raw)
	}
}
