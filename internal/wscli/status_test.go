package wscli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusListScopesTwoWorkspaces(t *testing.T) {
	workspaces := []Workspace{
		{ID: 11, Key: "ALPHA", Name: "Alpha"},
		{ID: 22, Key: "BETA", Name: "Beta"},
	}
	workspaceStatuses := map[int][]Status{
		11: {{ID: 101, Name: "Alpha Review"}},
		22: {{ID: 202, Name: "Beta Done", IsCompleted: true}},
	}
	systemStatuses := []Status{
		{ID: 101, Name: "Alpha Review"},
		{ID: 202, Name: "Beta Done", IsCompleted: true},
		{ID: 303, Name: "System Only"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/v1/workspaces":
			_ = json.NewEncoder(w).Encode(PaginatedResponse[Workspace]{Data: workspaces})
		case "/rest/api/v1/workspaces/11":
			_ = json.NewEncoder(w).Encode(workspaces[0])
		case "/rest/api/v1/workspaces/22":
			_ = json.NewEncoder(w).Encode(workspaces[1])
		case "/rest/api/v1/workspaces/11/statuses":
			_ = json.NewEncoder(w).Encode(workspaceStatuses[11])
		case "/rest/api/v1/workspaces/22/statuses":
			_ = json.NewEncoder(w).Encode(workspaceStatuses[22])
		case "/rest/api/v1/statuses":
			_ = json.NewEncoder(w).Encode(systemStatuses)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	run := func(t *testing.T, args ...string) StatusListResult {
		t.Helper()
		var out, errOut bytes.Buffer
		code := Run(context.Background(), args, nil, &out, &errOut, map[string]string{
			"WS_URL":   server.URL,
			"WS_TOKEN": "test-token",
		})
		if code != 0 {
			t.Fatalf("ws %s returned %d: %s", strings.Join(args, " "), code, errOut.String())
		}
		var result StatusListResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("decode output: %v\nraw=%s", err, out.String())
		}
		return result
	}

	alpha := run(t, "status", "ls", "-w", "ALPHA", "-o", "json")
	if alpha.Scope != "workspace" || alpha.Workspace == nil || alpha.Workspace.Key != "ALPHA" || alpha.Workspace.Name != "Alpha" {
		t.Fatalf("alpha scope metadata = %+v", alpha)
	}
	if len(alpha.Statuses) != 1 || alpha.Statuses[0].Name != "Alpha Review" {
		t.Fatalf("alpha statuses = %+v", alpha.Statuses)
	}

	beta := run(t, "status", "ls", "-w", "BETA", "-o", "json")
	if beta.Scope != "workspace" || beta.Workspace == nil || beta.Workspace.Key != "BETA" || beta.Workspace.Name != "Beta" {
		t.Fatalf("beta scope metadata = %+v", beta)
	}
	if len(beta.Statuses) != 1 || beta.Statuses[0].Name != "Beta Done" {
		t.Fatalf("beta statuses = %+v", beta.Statuses)
	}

	system := run(t, "status", "ls", "--system", "-o", "json")
	if system.Scope != "system" || system.Workspace != nil || len(system.Statuses) != 3 {
		t.Fatalf("system output = %+v", system)
	}
}

func TestStatusListOutputLabelsScope(t *testing.T) {
	result := &StatusListResult{
		Scope: "workspace",
		Workspace: &StatusListWorkspace{
			ID: 11, Key: "ALPHA", Name: "Alpha",
		},
		Statuses: []Status{{ID: 101, Name: "Review", CategoryName: "In Progress"}},
	}

	for _, format := range []string{"table", "csv"} {
		t.Run(format, func(t *testing.T) {
			out := captureStdout(t, func() { (&Output{format: format}).Print(result) })
			for _, want := range []string{"workspace", "ALPHA", "Alpha", "Review"} {
				if !strings.Contains(out, want) {
					t.Fatalf("%s output missing %q:\n%s", format, want, out)
				}
			}
		})
	}
}

func TestStatusListRejectsSystemWithExplicitWorkspace(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"status", "ls", "--system", "-w", "ALPHA"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   "http://127.0.0.1:1",
		"WS_TOKEN": "test-token",
	})
	if code == 0 || !strings.Contains(errOut.String(), "--system cannot be combined with --workspace") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}

func TestStatusListHelpDistinguishesWorkspaceAndSystem(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"status", "ls", "--help"}, nil, &out, &errOut, nil)
	if code != 0 {
		t.Fatalf("help returned %d: %s", code, errOut.String())
	}
	for _, want := range []string{"workspace", "system statuses", "--system", "move targets"} {
		if !strings.Contains(strings.ToLower(out.String()), strings.ToLower(want)) {
			t.Fatalf("help missing %q:\n%s", want, out.String())
		}
	}
}
