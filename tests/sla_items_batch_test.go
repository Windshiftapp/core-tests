//go:build test

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"windshift/internal/models"
)

// TestWorkspaceItemsSLABatch proves the batch item-SLA read returns the same
// per-item state as single-item reads in one request, ignores ids outside the
// workspace, and stays permission-gated (WI-1591).
func TestWorkspaceItemsSLABatch(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Batch Workspace", shortKey("SBW"))
	itemA := CreateTestItem(t, server, workspaceID, "Batch item A")
	itemB := CreateTestItem(t, server, workspaceID, "Batch item B")

	// A metric whose start condition never matches keeps every cycle count at
	// zero so the read is deterministic.
	CreateNeverMatchingSLAMetric(t, server, workspaceID, "Batch metric")

	// Unknown ids are ignored, not errors.
	batchPath := fmt.Sprintf("/workspaces/%d/sla/items?ids=%d,%d,999999999", workspaceID, itemA, itemB)
	resp := MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, batchPath, nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var batch map[string]any
	DecodeJSON(t, resp, &batch)
	_ = resp.Body.Close()
	if len(batch) != 2 {
		t.Fatalf("batch returned %d items, want 2 (unknown ids ignored): %#v", len(batch), batch)
	}

	// Batch state matches the single-item read exactly.
	singleResp := MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/items/%d/sla", itemA), nil)
	AssertStatusCode(t, singleResp, http.StatusOK)
	var single any
	DecodeJSON(t, singleResp, &single)
	_ = singleResp.Body.Close()
	batchState, ok := batch[fmt.Sprintf("%d", itemA)]
	if !ok {
		t.Fatalf("batch response is missing item %d: %#v", itemA, batch)
	}
	batchJSON, err := json.Marshal(batchState)
	if err != nil {
		t.Fatalf("encode batch state: %v", err)
	}
	singleJSON, err := json.Marshal(single)
	if err != nil {
		t.Fatalf("encode single state: %v", err)
	}
	var batchNorm, singleNorm any
	if err := json.Unmarshal(batchJSON, &batchNorm); err != nil {
		t.Fatalf("re-decode batch state: %v", err)
	}
	if err := json.Unmarshal(singleJSON, &singleNorm); err != nil {
		t.Fatalf("re-decode single state: %v", err)
	}
	if !reflect.DeepEqual(batchNorm, singleNorm) {
		t.Fatalf("batch state differs from single-item read:\nbatch:  %s\nsingle: %s", batchJSON, singleJSON)
	}

	// Missing ids parameter is a client error.
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/items", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()

	// More than the batch cap is a client error (dedup happens after the cap
	// check input is parsed, so use distinct ids).
	ids := "1"
	for i := 2; i <= 201; i++ {
		ids += fmt.Sprintf(",%d", i)
	}
	resp = MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/workspaces/%d/sla/items?ids=%s", workspaceID, ids), nil)
	AssertStatusCode(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

// TestWorkspaceItemsSLABatchDeniesNonMember proves a caller without the
// workspace item-view permission cannot read the batch (WI-1591).
func TestWorkspaceItemsSLABatchDeniesNonMember(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Batch Deny", shortKey("SBD"))
	itemID := CreateTestItem(t, server, workspaceID, "Deny batch item")

	// Assigning the built-in Viewer role flips a workspace into restricted
	// visibility, so membership alone stops granting view to every
	// authenticated user.
	restrictedUserID, _, _ := CreateTestUserWithCredentials(t, server, "sla_batch_restricter", "sla_batch_restricter@test.com")
	AssignWorkspaceRole(t, server, restrictedUserID, workspaceID, "Viewer")

	otherID, otherUsername, otherPassword := CreateTestUserWithCredentials(t, server, "sla_batch_other", "sla_batch_other@test.com")
	// A role assignment regularizes the account out of the bootstrap admin
	// set, so the caller holds no grant in the target workspace.
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "SLA Batch Other", shortKey("SBO"))
	AssignWorkspaceRole(t, server, otherID, otherWorkspaceID, "Viewer")
	otherToken := CreateBearerTokenForUser(t, server, otherUsername, otherPassword)

	resp := MakeAuthRequestWithToken(t, server, otherToken, http.MethodGet,
		fmt.Sprintf("/workspaces/%d/sla/items?ids=%d", workspaceID, itemID), nil)
	// Membership is probed through a 404 so outsiders cannot confirm the id.
	AssertStatusCode(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

// CreateNeverMatchingSLAMetric creates an active metric whose start condition
// can never match, so items open no SLA cycles and reads stay deterministic.
func CreateNeverMatchingSLAMetric(t *testing.T, server *TestServer, workspaceID int, name string) int {
	t.Helper()
	calendarPayload := map[string]any{
		"name":     "Batch always open",
		"timezone": "UTC",
		"weekly_intervals": map[string]any{
			"monday":    []map[string]string{{"start": "00:00", "end": "24:00"}},
			"tuesday":   []map[string]string{{"start": "00:00", "end": "24:00"}},
			"wednesday": []map[string]string{{"start": "00:00", "end": "24:00"}},
			"thursday":  []map[string]string{{"start": "00:00", "end": "24:00"}},
			"friday":    []map[string]string{{"start": "00:00", "end": "24:00"}},
			"saturday":  []map[string]string{{"start": "00:00", "end": "24:00"}},
			"sunday":    []map[string]string{{"start": "00:00", "end": "24:00"}},
		},
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), calendarPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, resp, &calendar)
	_ = resp.Body.Close()

	payload := map[string]any{
		"name":           name,
		"display_format": "time",
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "status_entered",
			"config": map[string]any{"status_ids": []int{999_999}},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": calendar.ID,
			}},
		}},
	}
	resp = MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), payload)
	AssertStatusCode(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
	return 0
}
