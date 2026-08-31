package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils/factory"
)

type customerSubmissionResponse struct {
	ID                  int    `json:"id"`
	WorkspaceID         int    `json:"workspace_id"`
	WorkspaceItemNumber int    `json:"workspace_item_number"`
	WorkspaceName       string `json:"workspace_name"`
	WorkspaceKey        string `json:"workspace_key"`
	CanView             bool   `json:"can_view"`
	Title               string `json:"title"`
}

func TestPortalCustomers_GetCustomerSubmissions_RespectsWorkspaceVisibility(t *testing.T) {
	const (
		userID     = 1
		customerID = 500
	)

	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	permService := newNegativeTestPermissionService(t, db)
	timePermService := services.NewTimePermissionService(db, permService)
	handler := NewPortalCustomersHandler(db, permService, services.NewCustomerOrganisationPermissionService(db, permService, timePermService))

	if _, err := db.Exec(`INSERT INTO portal_customers (id, name, email) VALUES (?, 'Customer', 'customer@example.com')`, customerID); err != nil {
		t.Fatalf("seed portal customer: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, key, active, is_personal)
		VALUES (10, 'Visible workspace', 'VIS', TRUE, FALSE),
		       (20, 'Hidden workspace', 'HID', TRUE, FALSE)
	`); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}

	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles ORDER BY id LIMIT 1`).Scan(&roleID); err != nil {
		t.Fatalf("locate workspace role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (999, 20, ?)`, roleID); err != nil {
		t.Fatalf("restrict hidden workspace: %v", err)
	}

	portalCustomerID := customerID
	f := factory.NewTestFactory(db)
	visibleID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID:             10,
		Title:                   "Visible submission",
		CreatorPortalCustomerID: &portalCustomerID,
	})
	if err != nil {
		t.Fatalf("seed visible submission: %v", err)
	}
	var visibleItemNumber int
	if err := db.QueryRow(`SELECT workspace_item_number FROM items WHERE id = ?`, visibleID).Scan(&visibleItemNumber); err != nil {
		t.Fatalf("load visible workspace item number: %v", err)
	}
	if _, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID:             20,
		Title:                   "Hidden workspace submission",
		CreatorPortalCustomerID: &portalCustomerID,
	}); err != nil {
		t.Fatalf("seed hidden submission: %v", err)
	}

	req := authedRequest(http.MethodGet, "/api/portal-customers/"+strconv.Itoa(customerID)+"/submissions", userID, nil)
	req.SetPathValue("id", strconv.Itoa(customerID))
	rr := httptest.NewRecorder()
	handler.GetCustomerSubmissions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []customerSubmissionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 submissions, got %d: %s", len(got), rr.Body.String())
	}

	byTitle := make(map[string]customerSubmissionResponse, len(got))
	for _, submission := range got {
		byTitle[submission.Title] = submission
	}
	visibleGot := byTitle["Visible submission"]
	if visibleGot.WorkspaceKey != "VIS" || visibleGot.WorkspaceName != "Visible workspace" {
		t.Errorf("visible workspace metadata = %q/%q, want VIS/Visible workspace", visibleGot.WorkspaceKey, visibleGot.WorkspaceName)
	}
	if visibleGot.WorkspaceItemNumber != visibleItemNumber {
		t.Errorf("visible workspace item number = %d, want %d", visibleGot.WorkspaceItemNumber, visibleItemNumber)
	}
	if !visibleGot.CanView {
		t.Error("visible submission can_view = false, want true")
	}

	hiddenGot := byTitle["Hidden workspace submission"]
	if hiddenGot.WorkspaceName != "" || hiddenGot.WorkspaceKey != "" || hiddenGot.WorkspaceItemNumber != 0 {
		t.Errorf("hidden workspace metadata was exposed: name=%q key=%q number=%d", hiddenGot.WorkspaceName, hiddenGot.WorkspaceKey, hiddenGot.WorkspaceItemNumber)
	}
	if hiddenGot.CanView {
		t.Error("hidden submission can_view = true, want false")
	}
}
