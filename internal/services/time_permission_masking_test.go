package services

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// Regression coverage for TimePermissionService.MaskInaccessibleProjectNames
// (WI-292): restricted time-project names must be blanked on item payloads for
// viewers without access to the project, on every surface that returns joined
// item rows (cookie-auth GetAll/Search/backlog/detail and the whole v1 API).
type maskingTestEnv struct {
	t        *testing.T
	db       database.Database
	timePerm *TimePermissionService

	viewerID     int // plain user, not a member of the restricted project
	managerID    int // sole manager of the restricted project
	restrictedID int // time project with a manager row → restricted
	openID       int // time project with no manager/member rows → open access
}

func newMaskingTestEnv(t *testing.T) *maskingTestEnv {
	t.Helper()

	dsn := fmt.Sprintf("file:maskproj-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	permService, err := NewPermissionService(db, DefaultPermissionCacheConfig())
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}

	env := &maskingTestEnv{
		t:        t,
		db:       db,
		timePerm: NewTimePermissionService(db, permService),
	}

	exec := func(q string, args ...interface{}) int64 {
		t.Helper()
		res, err := db.Exec(q, args...)
		if err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}

	env.viewerID = int(exec(`INSERT INTO users (email, username, first_name, last_name) VALUES ('viewer@test', 'viewer', 'viewer', '')`))
	env.managerID = int(exec(`INSERT INTO users (email, username, first_name, last_name) VALUES ('manager@test', 'manager', 'manager', '')`))

	customerID := exec(`INSERT INTO customer_organisations (name) VALUES ('Acme')`)
	env.restrictedID = int(exec(`INSERT INTO time_projects (customer_id, name, status) VALUES (?, 'Restricted Project', 'Active')`, customerID))
	env.openID = int(exec(`INSERT INTO time_projects (customer_id, name, status) VALUES (?, 'Open Project', 'Active')`, customerID))
	// A manager row flips the project into restricted mode for everyone else.
	exec(`INSERT INTO time_project_managers (project_id, manager_type, manager_id) VALUES (?, 'user', ?)`, env.restrictedID, env.managerID)

	return env
}

// items returns one item on the restricted project (all three project slots)
// and one on the open project.
func (e *maskingTestEnv) items() []models.Item {
	restricted := models.Item{
		ProjectID:            &e.restrictedID,
		ProjectName:          "Restricted Project",
		TimeProjectID:        &e.restrictedID,
		TimeProjectName:      "Restricted Project",
		EffectiveProjectID:   &e.restrictedID,
		EffectiveProjectName: "Restricted Project",
	}
	open := models.Item{
		ProjectID:            &e.openID,
		ProjectName:          "Open Project",
		TimeProjectID:        &e.openID,
		TimeProjectName:      "Open Project",
		EffectiveProjectID:   &e.openID,
		EffectiveProjectName: "Open Project",
	}
	return []models.Item{restricted, open}
}

func TestMaskInaccessibleProjectNames_BlanksRestrictedForNonMember(t *testing.T) {
	env := newMaskingTestEnv(t)
	items := env.items()

	env.timePerm.MaskInaccessibleProjectNames(env.viewerID, items)

	if items[0].ProjectName != "" || items[0].TimeProjectName != "" || items[0].EffectiveProjectName != "" {
		t.Fatalf("restricted project names not masked for non-member: %+v", items[0])
	}
	if items[0].ProjectID == nil || *items[0].ProjectID != env.restrictedID {
		t.Fatalf("project ID must survive masking, got %v", items[0].ProjectID)
	}
	if items[1].ProjectName != "Open Project" || items[1].TimeProjectName != "Open Project" || items[1].EffectiveProjectName != "Open Project" {
		t.Fatalf("open project names must be kept: %+v", items[1])
	}
}

func TestMaskInaccessibleProjectNames_KeepsNamesForManager(t *testing.T) {
	env := newMaskingTestEnv(t)
	items := env.items()

	env.timePerm.MaskInaccessibleProjectNames(env.managerID, items)

	if items[0].ProjectName != "Restricted Project" || items[0].TimeProjectName != "Restricted Project" || items[0].EffectiveProjectName != "Restricted Project" {
		t.Fatalf("manager must see restricted project names: %+v", items[0])
	}
}

func TestMaskInaccessibleProjectNames_KeepsNamesForProjectManagePermission(t *testing.T) {
	env := newMaskingTestEnv(t)
	if _, err := env.db.Exec(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = ?
	`, env.viewerID, models.PermissionProjectManage); err != nil {
		t.Fatalf("grant project.manage: %v", err)
	}
	items := env.items()

	env.timePerm.MaskInaccessibleProjectNames(env.viewerID, items)

	if items[0].ProjectName != "Restricted Project" {
		t.Fatalf("project.manage holder must see restricted project names: %+v", items[0])
	}
}

func TestMaskInaccessibleProjectNames_NoProjectAssigned(t *testing.T) {
	env := newMaskingTestEnv(t)
	items := []models.Item{{Title: "no project"}}

	env.timePerm.MaskInaccessibleProjectNames(env.viewerID, items)

	if items[0].Title != "no project" {
		t.Fatalf("item without projects must pass through untouched: %+v", items[0])
	}
}
