package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	Title               string `json:"title"`
	Description         string `json:"description"`
	StatusName          string `json:"status_name"`
	StatusColor         string `json:"status_color"`
}

func TestPortalCustomers_GetCustomerSubmissions_OmitsInaccessibleWorkspaces(t *testing.T) {
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
		Description:             "Visible body",
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
		Description:             "Hidden body",
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

	body := rr.Body.String()
	for _, leak := range []string{"Hidden workspace submission", "Hidden body", `"workspace_id":20`, "HID", "Hidden workspace"} {
		if strings.Contains(body, leak) {
			t.Fatalf("inaccessible submission leaked %q in response: %s", leak, body)
		}
	}

	var got []customerSubmissionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected only the visible submission, got %d: %s", len(got), body)
	}

	visibleGot := got[0]
	if visibleGot.Title != "Visible submission" || visibleGot.Description != "Visible body" {
		t.Errorf("visible submission content = %q/%q, want Visible submission/Visible body", visibleGot.Title, visibleGot.Description)
	}
	if visibleGot.WorkspaceID != 10 {
		t.Errorf("visible workspace id = %d, want 10", visibleGot.WorkspaceID)
	}
	if visibleGot.WorkspaceKey != "VIS" || visibleGot.WorkspaceName != "Visible workspace" {
		t.Errorf("visible workspace metadata = %q/%q, want VIS/Visible workspace", visibleGot.WorkspaceKey, visibleGot.WorkspaceName)
	}
	if visibleGot.WorkspaceItemNumber != visibleItemNumber {
		t.Errorf("visible workspace item number = %d, want %d", visibleGot.WorkspaceItemNumber, visibleItemNumber)
	}
}

// TestPortalCustomers_GetCustomerSubmissions_NoWorkspaceAccessReturnsNone pins
// the fail-closed contract: a caller with no viewable workspaces receives an
// empty list rather than submissions from workspaces it cannot see.
func TestPortalCustomers_GetCustomerSubmissions_NoWorkspaceAccessReturnsNone(t *testing.T) {
	const (
		userID     = 1
		customerID = 501
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
		VALUES (30, 'Restricted workspace', 'RES', TRUE, FALSE)
	`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	var roleID int
	if err := db.QueryRow(`SELECT id FROM workspace_roles ORDER BY id LIMIT 1`).Scan(&roleID); err != nil {
		t.Fatalf("locate workspace role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id) VALUES (999, 30, ?)`, roleID); err != nil {
		t.Fatalf("restrict workspace: %v", err)
	}

	portalCustomerID := customerID
	f := factory.NewTestFactory(db)
	if _, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID:             30,
		Title:                   "Restricted submission",
		Description:             "Restricted body",
		CreatorPortalCustomerID: &portalCustomerID,
	}); err != nil {
		t.Fatalf("seed restricted submission: %v", err)
	}

	req := authedRequest(http.MethodGet, "/api/portal-customers/"+strconv.Itoa(customerID)+"/submissions", userID, nil)
	req.SetPathValue("id", strconv.Itoa(customerID))
	rr := httptest.NewRecorder()
	handler.GetCustomerSubmissions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "[]" {
		t.Fatalf("expected empty submission list, got %s", body)
	}
}
