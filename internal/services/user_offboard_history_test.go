//go:build test

package services_test

import (
	"strconv"
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// TestOffboardUserRecordsAssigneeClearHistory asserts that items an offboarded
// user was assigned to keep a system-attributed assignee_id history row, so
// the audit trail shows why the assignee disappeared.
func TestOffboardUserRecordsAssigneeClearHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Assigned to offboarded user",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
		AssigneeID:  &data.UserID,
	})
	if err != nil {
		t.Fatalf("insert assigned item: %v", err)
	}

	offboarded := data.UserID
	if _, err := services.OffboardUser(tdb.GetDatabase(), offboarded, nil); err != nil {
		t.Fatalf("offboard user: %v", err)
	}

	var fieldName string
	var userID *int
	var actorKind, oldValue, newValue string
	if err := tdb.QueryRow(`
		SELECT field_name, user_id, actor_kind, COALESCE(old_value, ''), COALESCE(new_value, '')
		FROM item_history
		WHERE item_id = ? AND field_name = 'assignee_id' AND old_value = ?
	`, itemID, data.UserID).Scan(&fieldName, &userID, &actorKind, &oldValue, &newValue); err != nil {
		t.Fatalf("read assignee history row: %v", err)
	}
	if oldValue != strconv.Itoa(data.UserID) || newValue != "" {
		t.Fatalf("assignee history values = %q -> %q, want %q -> \"\"", oldValue, newValue, strconv.Itoa(data.UserID))
	}
	if userID != nil {
		t.Fatalf("history user_id = %d, want NULL for the system actor", *userID)
	}
	if actorKind != "system" {
		t.Fatalf("actor_kind = %q, want system", actorKind)
	}
}
