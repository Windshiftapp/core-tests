//go:build test

package repository

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func newRequestTypeVisibilityFixture(t *testing.T) (*RequestTypeRepository, int, int, int) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find seeded item type: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status)
		VALUES ('Visibility preservation channel', 'portal', 'inbound', 'enabled')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	var requestTypeID int
	if err := db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, is_active, visibility_group_ids)
		VALUES (?, 'Restricted request type', ?, true, '[42]')
		RETURNING id
	`, channelID, itemTypeID).Scan(&requestTypeID); err != nil {
		t.Fatalf("create restricted request type: %v", err)
	}

	return NewRequestTypeRepository(db), channelID, requestTypeID, itemTypeID
}

func assertVisibilityGroupIDs(t *testing.T, repo *RequestTypeRepository, id int, expected []int) {
	t.Helper()
	rt, err := repo.GetByID(id)
	if err != nil {
		t.Fatalf("GetByID(%d): %v", id, err)
	}
	if len(rt.VisibilityGroupIDs) != len(expected) {
		t.Fatalf("visibility_group_ids: got %v, want %v", rt.VisibilityGroupIDs, expected)
	}
	for i, want := range expected {
		if rt.VisibilityGroupIDs[i] != want {
			t.Fatalf("visibility_group_ids[%d]: got %d, want %d", i, rt.VisibilityGroupIDs[i], want)
		}
	}
}

// TestRequestTypeUpdatePreservesVisibility verifies that Update does not
// overwrite visibility_group_ids / visibility_org_ids. The general Update
// endpoint is called by routine edits (rename, icon, title template) that
// do not include visibility in the request body. If Update wrote NULL to
// the visibility columns, restricted request types would silently become
// visible to all portal customers — a privilege escalation.
func TestRequestTypeUpdatePreservesVisibility(t *testing.T) {
	repo, channelID, requestTypeID, itemTypeID := newRequestTypeVisibilityFixture(t)

	// Simulate a routine edit (e.g. rename) that omits visibility fields
	// entirely, exactly as the frontend does in RequestTypeModal.svelte,
	// formBuilderStore.svelte.js, and RequestTypeFieldsBuilder.svelte.
	err := repo.Update(requestTypeID, channelID, &models.RequestType{
		Name:       "Renamed request type",
		ItemTypeID: itemTypeID,
		IsActive:   true,
		// VisibilityGroupIDs and VisibilityOrgIDs are intentionally nil/omitted.
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The visibility restriction set at creation time must survive the edit.
	assertVisibilityGroupIDs(t, repo, requestTypeID, []int{42})
}

// TestRequestTypeUpdateVisibilityStillWorks verifies that the dedicated
// UpdateVisibility method is the only path that writes the visibility
// columns, and that it correctly replaces the restriction.
func TestRequestTypeUpdateVisibilityStillWorks(t *testing.T) {
	repo, channelID, requestTypeID, _ := newRequestTypeVisibilityFixture(t)

	// Replace the group restriction with an org restriction.
	if err := repo.UpdateVisibility(requestTypeID, channelID, nil, []int{7}); err != nil {
		t.Fatalf("UpdateVisibility: %v", err)
	}

	rt, err := repo.GetByID(requestTypeID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(rt.VisibilityGroupIDs) != 0 {
		t.Fatalf("visibility_group_ids should be empty after UpdateVisibility, got %v", rt.VisibilityGroupIDs)
	}
	if len(rt.VisibilityOrgIDs) != 1 || rt.VisibilityOrgIDs[0] != 7 {
		t.Fatalf("visibility_org_ids: got %v, want [7]", rt.VisibilityOrgIDs)
	}
}
