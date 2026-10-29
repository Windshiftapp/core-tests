//go:build test

package wscli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// Non-interactive init advertises WS_URL/WS_TOKEN in its error messages, so it
// must accept them without any flags. Regression: it previously only inspected
// the flags and failed with "--url is required".
func TestConfigInitNonInteractiveUsesEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/v2/users/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":1,"full_name":"Test User","email":"test@example.com"}}`))
	}))
	defer server.Close()

	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(project)

	var out, errOut bytes.Buffer
	code := Run(context.Background(),
		[]string{"config", "init", "--non-interactive"},
		nil, &out, &errOut,
		map[string]string{
			"WS_URL":       server.URL,
			"WS_TOKEN":     "crw_env_token",
			"WS_WORKSPACE": "ENG",
		})
	if code != 0 {
		t.Fatalf("config init returned %d: %s", code, errOut.String())
	}

	var saved Config
	if _, err := toml.DecodeFile(filepath.Join(project, "ws.toml"), &saved); err != nil {
		t.Fatalf("decode ws.toml: %v", err)
	}
	if saved.Server.URL != server.URL {
		t.Fatalf("server.url = %q, want %q", saved.Server.URL, server.URL)
	}
	if saved.Server.Token != "crw_env_token" {
		t.Fatalf("server.token = %q, want crw_env_token", saved.Server.Token)
	}
	if saved.Defaults.WorkspaceKey != "ENG" {
		t.Fatalf("defaults.workspace_key = %q, want ENG", saved.Defaults.WorkspaceKey)
	}
}

// Without flags or environment, non-interactive init still fails instead of
// hanging on a prompt.
func TestConfigInitNonInteractiveWithoutCredentialsFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	var out, errOut bytes.Buffer
	code := Run(context.Background(),
		[]string{"config", "init", "--non-interactive"},
		nil, &out, &errOut,
		map[string]string{
			"WS_URL":   "",
			"WS_TOKEN": "",
		})
	if code == 0 {
		t.Fatalf("config init succeeded without credentials, stdout=%s", out.String())
	}
}
