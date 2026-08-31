package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// Positive-path coverage for TimeProjectHandler.
//
// The negative regression suite next door (time_projects_negative_test.go)
// covers the permission-bypass cases that landed during the bughunt; this
// file covers ordinary Create / Get / GetAll / Update / Delete flows. Tests
// live in-tree rather than in the core-tests overlay because (a) the
// fixture helpers they need are already in-tree and (b) the overlay
// previously carried these tests against the now-retired legacy
// ProjectHandler — replicating them here removes the dependency on the
// overlay for live time-project coverage.

// grantGlobalPermission grants the named global permission to the given
// user by looking up the permission_id and inserting into
// user_global_permissions. Test seed data already declares the global
// permission set, so no schema setup is required.
func grantGlobalPermission(t *testing.T, db database.Database, userID int, permKey string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = ?
	`, userID, permKey); err != nil {
		t.Fatalf("grant %s to user %d: %v", permKey, userID, err)
	}
}

func TestTimeProjectHandler_Create_Success(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	if _, err := db.Exec(`INSERT INTO customer_organisations (id, name) VALUES (1, 'Acme')`); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	grantGlobalPermission(t, db, userID, models.PermissionProjectManage)

	handler := newTimeProjectHandler(t, db)

	customerID := 1
	body := models.TimeProject{
		Name:        "New Project",
		Description: "A new project",
		CustomerID:  &customerID,
		Status:      "Active",
	}
	req := authedRequest(http.MethodPost, "/api/time-projects", userID, body)
	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201. body=%s", rr.Code, rr.Body.String())
	}
	var resp models.TimeProject
	decodeJSONBody(t, rr, &resp)

	if resp.ID == 0 {
		t.Error("expected non-zero project ID in response")
	}
	if resp.Name != "New Project" {
		t.Errorf("got name %q, want %q", resp.Name, "New Project")
	}
	if resp.Status != "Active" {
		t.Errorf("got status %q, want %q", resp.Status, "Active")
	}
}

func TestTimeProjectHandler_Create_DefaultsStatusToActive(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	if _, err := db.Exec(`INSERT INTO customer_organisations (id, name) VALUES (1, 'Acme')`); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	grantGlobalPermission(t, db, userID, models.PermissionProjectManage)

	handler := newTimeProjectHandler(t, db)

	customerID := 1
	body := models.TimeProject{
		Name:       "Defaults",
		CustomerID: &customerID,
		// Status intentionally omitted - handler should default to "Active"
	}
	req := authedRequest(http.MethodPost, "/api/time-projects", userID, body)
	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201. body=%s", rr.Code, rr.Body.String())
	}
	var resp models.TimeProject
	decodeJSONBody(t, rr, &resp)

	if resp.Status != "Active" {
		t.Errorf("got default status %q, want %q", resp.Status, "Active")
	}
}

func TestTimeProjectHandler_Create_Forbidden_NoProjectManage(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	if _, err := db.Exec(`INSERT INTO customer_organisations (id, name) VALUES (1, 'Acme')`); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// Deliberately do NOT grant project.manage.

	handler := newTimeProjectHandler(t, db)

	customerID := 1
	body := models.TimeProject{Name: "Nope", CustomerID: &customerID}
	req := authedRequest(http.MethodPost, "/api/time-projects", userID, body)
	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403. body=%s", rr.Code, rr.Body.String())
	}
}

func TestTimeProjectHandler_Create_RejectsMissingCustomer(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	grantGlobalPermission(t, db, userID, models.PermissionProjectManage)

	handler := newTimeProjectHandler(t, db)

	body := models.TimeProject{Name: "Orphan", CustomerID: nil}
	req := authedRequest(http.MethodPost, "/api/time-projects", userID, body)
	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400. body=%s", rr.Code, rr.Body.String())
	}
}

func TestTimeProjectHandler_Create_RejectsUnknownCustomer(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	grantGlobalPermission(t, db, userID, models.PermissionProjectManage)

	handler := newTimeProjectHandler(t, db)

	bogusCustomerID := 9999
	body := models.TimeProject{Name: "Ghost", CustomerID: &bogusCustomerID}
	req := authedRequest(http.MethodPost, "/api/time-projects", userID, body)
	rr := httptest.NewRecorder()
	handler.Create(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400. body=%s", rr.Code, rr.Body.String())
	}
}

func TestTimeProjectHandler_Get_Success(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	openPID, _ := seedTwoTimeProjectsForUser(t, db)

	handler := newTimeProjectHandler(t, db)

	req := authedRequest(http.MethodGet, "/api/time-projects/1", userID, nil)
	req.SetPathValue("id", strconv.Itoa(openPID))
	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200. body=%s", rr.Code, rr.Body.String())
	}
	var resp models.TimeProject
	decodeJSONBody(t, rr, &resp)

	if resp.ID != openPID {
		t.Errorf("got ID %d, want %d", resp.ID, openPID)
	}
	if resp.Name != "Open" {
		t.Errorf("got name %q, want %q", resp.Name, "Open")
	}
}

func TestTimeProjectHandler_Get_NotFound(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	// No projects seeded - any ID is missing.
	grantGlobalPermission(t, db, userID, models.PermissionProjectManage)

	handler := newTimeProjectHandler(t, db)

	req := authedRequest(http.MethodGet, "/api/time-projects/99999", userID, nil)
	req.SetPathValue("id", "99999")
	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404. body=%s", rr.Code, rr.Body.String())
	}
}

func TestTimeProjectHandler_GetAll_ReturnsAccessibleProjects(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	openPID, _ := seedTwoTimeProjectsForUser(t, db)

	handler := newTimeProjectHandler(t, db)

	req := authedRequest(http.MethodGet, "/api/time-projects", userID, nil)
	rr := httptest.NewRecorder()
	handler.GetAll(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200. body=%s", rr.Code, rr.Body.String())
	}
	var resp []models.TimeProject
	decodeJSONBody(t, rr, &resp)

	if len(resp) != 1 {
		t.Fatalf("got %d projects, want 1 (only the open project). body=%s", len(resp), rr.Body.String())
	}
	if resp[0].ID != openPID {
		t.Errorf("returned project id=%d, want %d", resp[0].ID, openPID)
	}
}

func TestTimeProjectHandler_GetAll_ReturnsEmptyArray(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	// No projects seeded.

	handler := newTimeProjectHandler(t, db)

	req := authedRequest(http.MethodGet, "/api/time-projects", userID, nil)
	rr := httptest.NewRecorder()
	handler.GetAll(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200. body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "[]" && got != "[]\n" {
		t.Errorf("got body %q, want %q (empty array, never null)", got, "[]")
	}
}

func TestTimeProjectHandler_Update_Success(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	openPID, _ := seedTwoTimeProjectsForUser(t, db)

	handler := newTimeProjectHandler(t, db)

	customerID := 1
	body := models.TimeProject{
		Name:        "Renamed",
		Description: "updated description",
		CustomerID:  &customerID,
		Status:      "Inactive",
	}
	req := authedRequest(http.MethodPut, "/api/time-projects/1", userID, body)
	req.SetPathValue("id", strconv.Itoa(openPID))
	rr := httptest.NewRecorder()
	handler.Update(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200. body=%s", rr.Code, rr.Body.String())
	}
	var resp models.TimeProject
	decodeJSONBody(t, rr, &resp)

	if resp.Name != "Renamed" {
		t.Errorf("got name %q, want %q", resp.Name, "Renamed")
	}
	if resp.Status != "Inactive" {
		t.Errorf("got status %q, want %q", resp.Status, "Inactive")
	}

	// Verify persistence.
	var dbName, dbStatus string
	if err := db.QueryRow(`SELECT name, status FROM time_projects WHERE id = ?`, openPID).Scan(&dbName, &dbStatus); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if dbName != "Renamed" || dbStatus != "Inactive" {
		t.Errorf("db row name=%q status=%q, want Renamed/Inactive", dbName, dbStatus)
	}
}

func TestTimeProjectHandler_Update_Forbidden_NotManager(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	seedNegativeTestUser(t, db, 999)
	_, restrictedPID := seedTwoTimeProjectsForUser(t, db)

	handler := newTimeProjectHandler(t, db)

	customerID := 1
	body := models.TimeProject{Name: "Hijack", CustomerID: &customerID}
	req := authedRequest(http.MethodPut, "/api/time-projects/2", userID, body)
	req.SetPathValue("id", strconv.Itoa(restrictedPID))
	rr := httptest.NewRecorder()
	handler.Update(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403. body=%s", rr.Code, rr.Body.String())
	}
}

func TestTimeProjectHandler_Delete_Success(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	openPID, _ := seedTwoTimeProjectsForUser(t, db)
	grantGlobalPermission(t, db, userID, models.PermissionProjectManage)

	handler := newTimeProjectHandler(t, db)

	req := authedRequest(http.MethodDelete, "/api/time-projects/1", userID, nil)
	req.SetPathValue("id", strconv.Itoa(openPID))
	rr := httptest.NewRecorder()
	handler.Delete(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204. body=%s", rr.Code, rr.Body.String())
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM time_projects WHERE id = ?`, openPID).Scan(&remaining); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if remaining != 0 {
		t.Errorf("project %d still present after delete (count=%d)", openPID, remaining)
	}
}

func TestTimeProjectHandler_Delete_Forbidden_NoProjectManage(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	openPID, _ := seedTwoTimeProjectsForUser(t, db)
	// project.manage NOT granted; even though user is implicit-manager of the
	// open project (no managers configured), Delete requires the global
	// permission specifically.

	handler := newTimeProjectHandler(t, db)

	req := authedRequest(http.MethodDelete, "/api/time-projects/1", userID, nil)
	req.SetPathValue("id", strconv.Itoa(openPID))
	rr := httptest.NewRecorder()
	handler.Delete(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403. body=%s", rr.Code, rr.Body.String())
	}
}
