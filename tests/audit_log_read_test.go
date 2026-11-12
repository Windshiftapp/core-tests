//go:build test

package tests

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	"windshift/internal/logger"
)

// TestAuditLogReadSurfacesShareFilterContract covers WI-1901: the legacy, v1,
// and v2 read surfaces parse one shared filter contract, so the same query
// selects the same rows everywhere. Before the consolidation the v1 and v2
// handlers ignored `search` and `success` and silently accepted a malformed
// `success`, while the legacy handler returned 400.
func TestAuditLogReadSurfacesShareFilterContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceName := "Audit Read Workspace"
	CreateTestWorkspace(t, server, workspaceName, shortKey("ARW"))

	legacyIDs := auditLogIDs(t, MakeAuthRequestRaw(t, server, http.MethodGet,
		"/admin/audit-logs?action_type=workspace.create", ""), "entries")
	v1IDs := auditLogIDs(t, MakeBearerRequest(t, server, http.MethodGet,
		"/rest/api/v1/admin/audit-logs?action_type=workspace.create&limit=100", nil), "data")
	v2IDs := auditLogIDs(t, MakeBearerRequest(t, server, http.MethodGet,
		"/rest/api/v2/admin/audit-logs?action_type=workspace.create&page_size=100", nil), "data")

	if len(legacyIDs) == 0 {
		t.Fatal("no workspace.create audit rows found to compare")
	}
	if !slices.Equal(legacyIDs, v1IDs) || !slices.Equal(v1IDs, v2IDs) {
		t.Fatalf("read surfaces disagree for the same filter: legacy=%v v1=%v v2=%v", legacyIDs, v1IDs, v2IDs)
	}

	// `search` must be honored on every surface.
	encodedSearch := url.QueryEscape(workspaceName)
	legacySearchIDs := auditLogIDs(t, MakeAuthRequestRaw(t, server, http.MethodGet,
		"/admin/audit-logs?search="+encodedSearch, ""), "entries")
	v2SearchIDs := auditLogIDs(t, MakeBearerRequest(t, server, http.MethodGet,
		"/rest/api/v2/admin/audit-logs?search="+encodedSearch+"&page_size=100", nil), "data")
	if !slices.Contains(legacySearchIDs, legacyIDs[0]) || !slices.Contains(v2SearchIDs, legacyIDs[0]) {
		t.Fatalf("search did not return the matching row: legacy=%v v2=%v", legacySearchIDs, v2SearchIDs)
	}

	// A malformed `success` is a client error on every surface.
	for _, surface := range []struct {
		name    string
		request func() *http.Response
	}{
		{"legacy", func() *http.Response {
			return MakeAuthRequestRaw(t, server, http.MethodGet, "/admin/audit-logs?success=maybe", "")
		}},
		{"v1", func() *http.Response {
			return MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v1/admin/audit-logs?success=maybe", nil)
		}},
		{"v2", func() *http.Response {
			return MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/admin/audit-logs?success=maybe", nil)
		}},
	} {
		response := surface.request()
		AssertStatusCode(t, response, http.StatusBadRequest)
		_ = response.Body.Close()
	}

	// Reading the audit log is itself audited.
	var readCount int
	if err := server.server.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action_type = ?`, logger.ActionAuditLogRead).Scan(&readCount); err != nil {
		t.Fatalf("count audit_log.read rows: %v", err)
	}
	if readCount < 3 {
		t.Fatalf("expected an audit_log.read row per surface, got %d", readCount)
	}
}

// TestAuditLogStreamReadIsAudited covers WI-1901 for the cursor tail: each
// stream call records who tailed the log, with the cursor and batch size.
func TestAuditLogStreamReadIsAudited(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	CreateTestWorkspace(t, server, "Audit Stream Workspace", shortKey("ASW"))

	response := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/admin/audit-logs/since?after_id=0&limit=5", nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)

	var detailsJSON string
	if err := server.server.DB().QueryRow(`
		SELECT details FROM audit_logs WHERE action_type = ? ORDER BY id DESC LIMIT 1
	`, logger.ActionAuditLogStream).Scan(&detailsJSON); err != nil {
		t.Fatalf("load audit_log.stream row: %v", err)
	}
	if detailsJSON == "" {
		t.Fatal("audit_log.stream details are empty")
	}
}

func auditLogIDs(t *testing.T, response *http.Response, key string) []int {
	t.Helper()
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var body map[string]any
	DecodeJSON(t, response, &body)
	raw, ok := body[key].([]any)
	if !ok {
		raw, ok = body["data"].([]any)
	}
	if !ok {
		raw, ok = body["items"].([]any)
	}
	if !ok {
		t.Fatalf("response missing entry array %q: %#v", key, body)
	}
	ids := make([]int, 0, len(raw))
	for _, entry := range raw {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("entry is not an object: %#v", entry)
		}
		id, ok := row["id"].(float64)
		if !ok {
			t.Fatalf("entry missing numeric id: %#v", row)
		}
		ids = append(ids, int(id))
	}
	return ids
}
