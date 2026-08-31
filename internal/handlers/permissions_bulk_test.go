package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func TestAllUserGlobalPermissionsReturnsEffectiveAssignmentsInBulk(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "bulk-user-permissions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	user1 := insertID("user 1", `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('bulk-one@example.test', 'bulk-one', 'Bulk', 'One') RETURNING id
	`)
	user2 := insertID("user 2", `
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('bulk-two@example.test', 'bulk-two', 'Bulk', 'Two') RETURNING id
	`)
	permission1 := insertID("permission 1", `
		INSERT INTO permissions (permission_key, permission_name, scope)
		VALUES ('bulk.permission.one', 'Bulk permission one', 'global') RETURNING id
	`)
	permission2 := insertID("permission 2", `
		INSERT INTO permissions (permission_key, permission_name, scope)
		VALUES ('bulk.permission.two', 'Bulk permission two', 'global') RETURNING id
	`)
	activeGroup := insertID("active group", `
		INSERT INTO groups (name, is_active) VALUES ('Active bulk group', true) RETURNING id
	`)
	inactiveGroup := insertID("inactive group", `
		INSERT INTO groups (name, is_active) VALUES ('Inactive bulk group', false) RETURNING id
	`)

	statements := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO user_global_permissions (user_id, permission_id) VALUES (?, ?)`, []interface{}{user1, permission1}},
		{`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, []interface{}{activeGroup, user1}},
		{`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, []interface{}{inactiveGroup, user2}},
		{`INSERT INTO group_global_permissions (group_id, permission_id) VALUES (?, ?)`, []interface{}{activeGroup, permission1}},
		{`INSERT INTO group_global_permissions (group_id, permission_id) VALUES (?, ?)`, []interface{}{activeGroup, permission2}},
		{`INSERT INTO group_global_permissions (group_id, permission_id) VALUES (?, ?)`, []interface{}{inactiveGroup, permission2}},
	}
	for _, statement := range statements {
		if _, err := db.ExecWrite(statement.query, statement.args...); err != nil {
			t.Fatalf("seed assignment %q: %v", statement.query, err)
		}
	}

	handler := NewPermissionHandlerWithCache(repository.NewPermissionRepository(db), nil, nil)
	recorder := httptest.NewRecorder()
	handler.GetAllUserGlobalPermissions(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/users/permissions/global", nil),
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var grants []repository.UserEffectiveGlobalGrant
	if err := json.NewDecoder(recorder.Body).Decode(&grants); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []repository.UserEffectiveGlobalGrant{
		{UserID: user1, PermissionID: permission1},
		{UserID: user1, PermissionID: permission2},
	}
	if len(grants) != len(want) {
		t.Fatalf("grants = %+v, want %+v", grants, want)
	}
	for i := range want {
		if grants[i] != want[i] {
			t.Fatalf("grant %d = %+v, want %+v", i, grants[i], want[i])
		}
	}
}
