//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestItemTypeHandler_Update_PreservesOmittedDefaultFlag(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow("SELECT id FROM item_types WHERE is_default = true LIMIT 1").Scan(&itemTypeID); err != nil {
		t.Fatalf("find default item type: %v", err)
	}
	itemType, err := repository.NewItemTypeRepository(db).GetByID(itemTypeID)
	if err != nil {
		t.Fatalf("load default item type: %v", err)
	}

	payload := map[string]any{
		"name":            itemType.Name,
		"description":     "Updated without changing the default",
		"icon":            itemType.Icon,
		"color":           itemType.Color,
		"hierarchy_level": itemType.HierarchyLevel,
		"sort_order":      itemType.SortOrder,
	}
	req := testutils.CreateJSONRequest(t, "PUT", "/api/item-types/"+testutils.IntToString(itemTypeID), payload)
	req.SetPathValue("id", testutils.IntToString(itemTypeID))
	rr := testutils.ExecuteAuthenticatedRequest(t, NewItemTypeHandler(db).Update, req, nil)

	rr.AssertStatusCode(http.StatusOK)

	var isDefault bool
	if err := db.QueryRow("SELECT is_default FROM item_types WHERE id = ?", itemTypeID).Scan(&isDefault); err != nil {
		t.Fatalf("read updated item type: %v", err)
	}
	if !isDefault {
		t.Error("omitted is_default changed the default item type to non-default")
	}
}
