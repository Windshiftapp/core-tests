package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
)

func TestPlanningEnumMutationsRequireGlobalPermissionInHandler(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "planning-enum-auth.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	permissionService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: 0, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })

	insertUser := func(name string) *models.User {
		t.Helper()
		result, err := db.ExecWrite(`
			INSERT INTO users (email, username, first_name, last_name, is_active)
			VALUES (?, ?, ?, 'User', true)
		`, name+"@example.test", name, name)
		if err != nil {
			t.Fatalf("insert user %s: %v", name, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return &models.User{ID: int(id), Username: name, Email: name + "@example.test"}
	}
	grant := func(userID int, permission string) {
		t.Helper()
		if _, err := db.ExecWrite(`
			INSERT INTO user_global_permissions (user_id, permission_id)
			SELECT ?, id FROM permissions WHERE permission_key = ?
		`, userID, permission); err != nil {
			t.Fatalf("grant %s: %v", permission, err)
		}
	}
	request := func(method, target, body string, user *models.User) *http.Request {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		return req.WithContext(context.WithValue(req.Context(), contextkeys.User, user))
	}

	viewer := insertUser("catalog-viewer")
	milestoneManager := insertUser("milestone-manager")
	iterationManager := insertUser("iteration-manager")
	admin := insertUser("catalog-admin")
	grant(milestoneManager.ID, models.PermissionMilestoneCreate)
	grant(iterationManager.ID, models.PermissionIterationManage)
	grant(admin.ID, models.PermissionSystemAdmin)

	milestoneHandler := NewEnumHandler(
		services.NewEnumService(db, services.NewMilestoneCategoryConfig()),
		func() interface{} { return &models.MilestoneCategory{} },
	).WithGlobalMutationPermission(permissionService, models.PermissionMilestoneCreate)
	iterationHandler := NewEnumHandler(
		services.NewEnumService(db, services.NewIterationTypeConfig()),
		func() interface{} { return &models.IterationType{} },
	).WithGlobalMutationPermission(permissionService, models.PermissionIterationManage)

	recorder := httptest.NewRecorder()
	milestoneHandler.Create(recorder, request(http.MethodPost, "/milestone-categories", `{"name":"Denied","color":"#123456"}`, viewer))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	milestoneHandler.Create(recorder, request(http.MethodPost, "/milestone-categories", `{"name":"Allowed","color":"#123456"}`, milestoneManager))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("milestone manager create status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var categoryID int
	if err := db.QueryRow(`SELECT id FROM milestone_categories WHERE name = 'Allowed'`).Scan(&categoryID); err != nil {
		t.Fatalf("load created category: %v", err)
	}

	recorder = httptest.NewRecorder()
	updateRequest := request(http.MethodPut, fmt.Sprintf("/milestone-categories/%d", categoryID), `{"name":"Changed","color":"#654321"}`, viewer)
	updateRequest.SetPathValue("id", fmt.Sprintf("%d", categoryID))
	milestoneHandler.Update(recorder, updateRequest)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer update status = %d, want 403", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	deleteRequest := request(http.MethodDelete, fmt.Sprintf("/milestone-categories/%d", categoryID), "", viewer)
	deleteRequest.SetPathValue("id", fmt.Sprintf("%d", categoryID))
	milestoneHandler.Delete(recorder, deleteRequest)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer delete status = %d, want 403", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	updateRequest = request(http.MethodPut, fmt.Sprintf("/milestone-categories/%d", categoryID), `{"name":"Changed","color":"#654321"}`, milestoneManager)
	updateRequest.SetPathValue("id", fmt.Sprintf("%d", categoryID))
	milestoneHandler.Update(recorder, updateRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("milestone manager update status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	iterationHandler.Create(recorder, request(http.MethodPost, "/iteration-types", `{"name":"Denied sprint","color":"#123456"}`, milestoneManager))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("wrong catalog permission status = %d, want 403", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	iterationHandler.Create(recorder, request(http.MethodPost, "/iteration-types", `{"name":"Security sprint","color":"#123456"}`, iterationManager))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("iteration manager create status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	milestoneHandler.Create(recorder, request(http.MethodPost, "/milestone-categories", `{"name":"Admin category","color":"#abcdef"}`, admin))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("system admin create status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	deleteRequest = request(http.MethodDelete, fmt.Sprintf("/milestone-categories/%d", categoryID), "", milestoneManager)
	deleteRequest.SetPathValue("id", fmt.Sprintf("%d", categoryID))
	milestoneHandler.Delete(recorder, deleteRequest)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("milestone manager delete status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
	}
}
