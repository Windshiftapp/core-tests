//go:build test

package services_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type userDisplayBatchAccess struct {
	canList bool
	err     error
	calls   int
}

func (a *userDisplayBatchAccess) CanViewWorkspace(int, int) (bool, error)  { return true, nil }
func (a *userDisplayBatchAccess) CanAdminWorkspace(int, int) (bool, error) { return true, nil }
func (a *userDisplayBatchAccess) HasGlobalPermission(_ int, permission string) (bool, error) {
	if permission != models.PermissionUserList {
		return false, fmt.Errorf("unexpected permission %q", permission)
	}
	a.calls++
	return a.canList, a.err
}
func (a *userDisplayBatchAccess) WorkspaceVisibility(int) (services.WorkspaceVisibility, error) {
	return nil, errors.New("unexpected workspace visibility lookup")
}

type userDisplayBatchCountingDB struct {
	database.Database
	reads int
}

func (db *userDisplayBatchCountingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.reads++
	return db.Database.QueryContext(ctx, query, args...)
}

func seedDisplayUser(t *testing.T, db database.Database, name string, active bool) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES (?, ?, ?, 'User', ?) RETURNING id
	`, name+"@example.test", name, name, active).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", name, err)
	}
	return id
}

func newUserDisplayBatchService(t *testing.T, canList bool) (*services.CatalogReadService, *userDisplayBatchCountingDB, *userDisplayBatchAccess, database.Database) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	counting := &userDisplayBatchCountingDB{Database: tdb.GetDatabase()}
	access := &userDisplayBatchAccess{canList: canList}
	service := services.NewCatalogReadService(counting, nil, access, nil)
	return service, counting, access, tdb.GetDatabase()
}

func TestCatalogGetUserDisplayBatchOmitsInactiveAndMissingInRequestOrder(t *testing.T) {
	service, counting, access, db := newUserDisplayBatchService(t, true)
	first := seedDisplayUser(t, db, "display-first", true)
	second := seedDisplayUser(t, db, "display-second", true)
	inactive := seedDisplayUser(t, db, "display-inactive", false)

	users, err := service.GetUserDisplayBatch(context.Background(), first, []int{inactive, second, first, 999999})
	if err != nil {
		t.Fatalf("GetUserDisplayBatch: %v", err)
	}
	if got := userIDs(users); fmt.Sprint(got) != fmt.Sprint([]int{second, first}) {
		t.Fatalf("user ids = %v, want request-order [%d %d]", got, second, first)
	}
	if counting.reads != 1 {
		t.Fatalf("read queries = %d, want 1", counting.reads)
	}
	if access.calls != 1 {
		t.Fatalf("user.list checks = %d, want 1", access.calls)
	}
}

func TestCatalogGetUserDisplayBatchReturnsSelfWithoutUserList(t *testing.T) {
	service, counting, access, db := newUserDisplayBatchService(t, false)
	self := seedDisplayUser(t, db, "display-self", true)
	other := seedDisplayUser(t, db, "display-other", true)

	users, err := service.GetUserDisplayBatch(context.Background(), self, []int{other, self})
	if err != nil {
		t.Fatalf("GetUserDisplayBatch: %v", err)
	}
	if got := userIDs(users); fmt.Sprint(got) != fmt.Sprint([]int{self}) {
		t.Fatalf("user ids = %v, want only self %d", got, self)
	}
	if counting.reads != 1 {
		t.Fatalf("read queries = %d, want 1", counting.reads)
	}
	if access.calls != 1 {
		t.Fatalf("user.list checks = %d, want 1", access.calls)
	}
}

func TestCatalogGetUserDisplayBatchOmitsOtherIDsWithoutUserList(t *testing.T) {
	service, counting, _, db := newUserDisplayBatchService(t, false)
	self := seedDisplayUser(t, db, "display-self-only", true)
	other := seedDisplayUser(t, db, "display-other-only", true)

	users, err := service.GetUserDisplayBatch(context.Background(), self, []int{other})
	if err != nil {
		t.Fatalf("GetUserDisplayBatch: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("users = %+v, want none without user.list", users)
	}
	if counting.reads != 0 {
		t.Fatalf("read queries = %d, want 0 when every id is forbidden", counting.reads)
	}
}

func TestCatalogGetUserDisplayBatchFailsOnPermissionError(t *testing.T) {
	service, counting, access, db := newUserDisplayBatchService(t, false)
	self := seedDisplayUser(t, db, "display-permission-error", true)
	other := seedDisplayUser(t, db, "display-permission-other", true)
	access.err = errors.New("permission cache unavailable")

	if _, err := service.GetUserDisplayBatch(context.Background(), self, []int{other}); err == nil {
		t.Fatal("expected permission lookup failure to fail the batch")
	}
	if counting.reads != 0 {
		t.Fatalf("read queries = %d, want 0 after permission failure", counting.reads)
	}
}

func userIDs(users []models.User) []int {
	ids := make([]int, len(users))
	for i := range users {
		ids[i] = users[i].ID
	}
	return ids
}
