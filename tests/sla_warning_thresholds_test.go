//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

func TestSLAWarningThresholdConfiguration(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	bearer := server.BearerToken
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Threshold Workspace", shortKey("STW"))

	// Internal API creates a threshold.
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/warning-thresholds", workspaceID), map[string]any{"percent": 75, "label": "Three quarters"})
	AssertStatusCode(t, resp, http.StatusCreated)
	var threshold models.SLAWarningThreshold
	DecodeJSON(t, resp, &threshold)
	_ = resp.Body.Close()
	if threshold.ID == 0 || threshold.Percent != 75 {
		t.Fatalf("created threshold = %#v", threshold)
	}

	// Invalid percentages are rejected.
	for _, percent := range []int{0, 100, 150} {
		resp = MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/warning-thresholds", workspaceID), map[string]any{"percent": percent})
		AssertStatusCode(t, resp, http.StatusBadRequest)
		_ = resp.Body.Close()
	}

	// v2 lists and updates the same threshold.
	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds", workspaceID), nil)
	AssertStatusCode(t, resp, http.StatusOK)
	var listed []models.SLAWarningThreshold
	DecodeJSON(t, resp, &listed)
	_ = resp.Body.Close()
	if len(listed) != 1 || listed[0].ID != threshold.ID {
		t.Fatalf("v2 listed thresholds = %#v", listed)
	}

	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodPut, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds/%d", workspaceID, threshold.ID), map[string]any{"percent": 50, "label": "Halfway"})
	AssertStatusCode(t, resp, http.StatusOK)
	var updated models.SLAWarningThreshold
	DecodeJSON(t, resp, &updated)
	_ = resp.Body.Close()
	if updated.Percent != 50 || updated.Label != "Halfway" {
		t.Fatalf("updated threshold = %#v", updated)
	}

	resp = MakeBearerRequestWithToken(t, server, bearer, http.MethodDelete, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds/%d", workspaceID, threshold.ID), nil)
	AssertStatusCode(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()
}

func TestSLAWarningThresholdRequiresAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Threshold Denial", shortKey("STD"))
	viewerID, username, password := CreateTestUserWithCredentials(t, server, "sla_threshold_viewer", "sla_threshold_viewer@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	_, viewerBearer := CreateAuthCredentialsForUser(t, server, username, password)

	resp := MakeBearerRequestWithToken(t, server, viewerBearer, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds", workspaceID), map[string]any{"percent": 75})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer threshold create = %d, want denied (403/404)", resp.StatusCode)
	}
}

// TestSLAWarningThresholdReadableByItemViewers proves the warning-threshold
// read is available to plain item viewers (agents see warning badges without
// workspace administration) while writes stay admin-only, and a
// non-member's read stays denied (WI-1578).
func TestSLAWarningThresholdReadableByItemViewers(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminBearer := CreateBearerToken(t, server)
	server.BearerToken = adminBearer
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Threshold Read", shortKey("STR"))

	create := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/warning-thresholds", workspaceID), map[string]any{"percent": 75, "label": "Warning"})
	AssertStatusCode(t, create, http.StatusCreated)
	_ = create.Body.Close()

	// A workspace Viewer (item.view, no admin) can list thresholds.
	viewerID, viewerUsername, viewerPassword := CreateTestUserWithCredentials(t, server, "sla_threshold_read", "sla_threshold_read@test.com")
	AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
	_, viewerToken := CreateAuthCredentialsForUser(t, server, viewerUsername, viewerPassword)

	list := MakeBearerRequestWithToken(t, server, viewerToken, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds", workspaceID), nil)
	AssertStatusCode(t, list, http.StatusOK)
	var listed []models.SLAWarningThreshold
	DecodeJSON(t, list, &listed)
	_ = list.Body.Close()
	if len(listed) != 1 || listed[0].Percent != 75 {
		t.Fatalf("viewer threshold list = %#v", listed)
	}
	// Writes stay admin-only for the same viewer; the admin-gate denial
	// surfaces as not-found so outsiders cannot probe the workspace.
	denied := MakeBearerRequestWithToken(t, server, viewerToken, http.MethodPost, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds", workspaceID), map[string]any{"percent": 75})
	AssertStatusCode(t, denied, http.StatusNotFound)
	_ = denied.Body.Close()

	// A regularized user with no grant in this workspace cannot read.
	otherID, otherName, otherPassword := CreateTestUserWithCredentials(t, server, "sla_threshold_outsider", "sla_threshold_outsider@test.com")
	otherWorkspace, _ := CreateTestWorkspace(t, server, "SLA Threshold Other", shortKey("STO"))
	AssignWorkspaceRole(t, server, otherID, otherWorkspace, "Viewer")
	_, otherToken := CreateAuthCredentialsForUser(t, server, otherName, otherPassword)
	outside := MakeBearerRequestWithToken(t, server, otherToken, http.MethodGet, fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/warning-thresholds", workspaceID), nil)
	AssertStatusCode(t, outside, http.StatusNotFound)
	_ = outside.Body.Close()
}
