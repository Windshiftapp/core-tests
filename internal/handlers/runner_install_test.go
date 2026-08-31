package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// WI-313: the hosted install script must come back with every placeholder
// replaced by server-known values — public /api URL and version-matched
// image tags — so the operator only supplies the registration token.
func TestRunnerInstallScriptSubstitutesServerValues(t *testing.T) {
	h := NewRunnerInstallHandler("https://windshift.example.com/")

	rec := httptest.NewRecorder()
	h.ServeScript(rec, httptest.NewRequest("GET", "/api/runner-install.sh", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "__WS_API_URL__") || strings.Contains(body, "__AGENT_IMAGE__") ||
		strings.Contains(body, "__RUNNER_IMAGE__") || strings.Contains(body, "__SCRIPT_URL__") {
		t.Fatalf("unreplaced placeholder in script:\n%s", body[:min(len(body), 2000)])
	}
	for _, want := range []string{
		`WS_API_URL="https://windshift.example.com/api"`,
		// Dev builds (version.Version == "dev") must reference :main — the
		// branch tag that always exists — never :latest (WI-314 trap).
		`AGENT_IMAGE="ghcr.io/windshiftapp/windshift-agent:main"`,
		`RUNNER_IMAGE="ghcr.io/windshiftapp/windshift-runner:main"`,
		// The script must refresh BOTH images: a cached-but-stale agent image is
		// never re-pulled by the runner otherwise (WI-511).
		`docker pull "$RUNNER_IMAGE"`,
		`docker pull "$AGENT_IMAGE"`,
		"curl -fsSL https://windshift.example.com/api/runner-install.sh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/x-shellscript") {
		t.Errorf("Content-Type = %q, want text/x-shellscript", ct)
	}
}

// Without a configured base URL the handler falls back to the request host
// over https — any real deployment terminates TLS in front of the server.
func TestRunnerInstallScriptFallsBackToRequestHost(t *testing.T) {
	h := NewRunnerInstallHandler("")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/runner-install.sh", nil)
	req.Host = "ws.internal:8443"
	h.ServeScript(rec, req)

	if want := `WS_API_URL="https://ws.internal:8443/api"`; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("script missing %q", want)
	}
}
