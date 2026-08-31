package todoist

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestClient returns a Client whose Sync API base points at the given test
// server. baseURL is unexported, so this white-box test sets it directly.
func newTestClient(serverURL string) *Client {
	c := NewClient("test-token")
	c.baseURL = serverURL
	return c
}

func TestSyncSendsTokenAndParsesResponse(t *testing.T) {
	var gotAuth, gotToken, gotResourceTypes, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotToken = form.Get("sync_token")
		gotResourceTypes = form.Get("resource_types")
		_, _ = io.WriteString(w, `{
			"sync_token": "next-token",
			"full_sync": false,
			"items": [
				{"id":"1","project_id":"p1","content":"Buy milk","priority":1,"checked":false},
				{"id":"2","project_id":"p1","content":"Done thing","checked":true}
			],
			"projects": [{"id":"p1","name":"Inbox","inbox_project":true}]
		}`)
	}))
	defer srv.Close()

	resp, err := newTestClient(srv.URL).Sync("prev-token", []string{"items", "projects"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotToken != "prev-token" {
		t.Errorf("sync_token = %q, want prev-token", gotToken)
	}
	if gotResourceTypes != `["items","projects"]` {
		t.Errorf("resource_types = %q", gotResourceTypes)
	}
	if resp.SyncToken != "next-token" {
		t.Errorf("SyncToken = %q", resp.SyncToken)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].Content != "Buy milk" || resp.Items[0].Checked {
		t.Errorf("item 0 = %+v", resp.Items[0])
	}
	if !resp.Items[1].Checked {
		t.Errorf("item 1 should be checked: %+v", resp.Items[1])
	}
	if len(resp.Projects) != 1 || !resp.Projects[0].IsInbox {
		t.Errorf("projects = %+v", resp.Projects)
	}
}

func TestSyncEmptyTokenSendsStar(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotToken = form.Get("sync_token")
		_, _ = io.WriteString(w, `{"sync_token":"t"}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(srv.URL).Sync("", []string{"items"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if gotToken != "*" {
		t.Errorf("sync_token = %q, want *", gotToken)
	}
}

func TestExecuteCommandsParsesMappingAndStatus(t *testing.T) {
	var sentCommands string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		sentCommands = form.Get("commands")
		_, _ = io.WriteString(w, `{
			"sync_token": "after-write",
			"temp_id_mapping": {"temp-abc": "999"},
			"sync_status": {"cmd-ok": "ok", "cmd-bad": {"error_code": 15, "error": "Invalid argument"}}
		}`)
	}))
	defer srv.Close()

	cmd, tempID := NewAddItemCommand(AddItemArgs{Content: "New task", DueDate: "2026-07-01"})
	cmd.UUID = "cmd-ok"
	cmd.TempID = "temp-abc"

	resp, err := newTestClient(srv.URL).ExecuteCommands([]Command{cmd})
	if err != nil {
		t.Fatalf("ExecuteCommands: %v", err)
	}

	// The add command should have serialized a due object derived from DueDate.
	var decoded []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(sentCommands), &decoded); err != nil {
		t.Fatalf("commands not valid json: %v", err)
	}
	if !strings.Contains(sentCommands, `"due":{"date":"2026-07-01"}`) {
		t.Errorf("expected due derived from DueDate, got %s", sentCommands)
	}
	if tempID == "" {
		t.Error("NewAddItemCommand returned empty temp id")
	}

	if got := resp.TempIDMapping["temp-abc"]; got != "999" {
		t.Errorf("temp id mapping = %q, want 999", got)
	}
	if err := resp.CommandError("cmd-ok"); err != nil {
		t.Errorf("CommandError(cmd-ok) = %v, want nil", err)
	}
	if err := resp.CommandError("cmd-bad"); err == nil {
		t.Error("CommandError(cmd-bad) = nil, want error")
	} else if !strings.Contains(err.Error(), "Invalid argument") {
		t.Errorf("CommandError(cmd-bad) = %v", err)
	}
	if err := resp.CommandError("absent"); err != nil {
		t.Errorf("CommandError(absent) = %v, want nil", err)
	}
}

func TestListProjectsFiltersDeleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
			"sync_token": "t",
			"projects": [
				{"id":"p1","name":"Inbox","inbox_project":true},
				{"id":"p2","name":"Old","is_deleted":true},
				{"id":"p3","name":"Work"}
			]
		}`)
	}))
	defer srv.Close()

	projects, err := newTestClient(srv.URL).ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2 (deleted filtered)", len(projects))
	}
	for _, p := range projects {
		if p.IsDeleted {
			t.Errorf("deleted project leaked: %+v", p)
		}
	}
}

func TestSyncHTTPErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"AUTH_INVALID_TOKEN"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).Sync("t", []string{"items"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention status: %v", err)
	}
}
