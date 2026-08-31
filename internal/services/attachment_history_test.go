//go:build test

package services_test

import (
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

func TestAttachmentService_RecordItemHistoryPreservesNullableOldValue(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Attachment history",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}

	service := services.NewAttachmentService(tdb.GetDatabase())
	if err := service.RecordItemHistory(itemID, &data.UserID, "attachment_uploaded", nil, 17, "upload.txt"); err != nil {
		t.Fatalf("record upload history: %v", err)
	}
	filename := "deleted.txt"
	if err := service.RecordItemHistory(itemID, &data.UserID, "attachment_deleted", &filename, 0, filename); err != nil {
		t.Fatalf("record delete history: %v", err)
	}

	var uploadOldValueIsNull bool
	if err := tdb.QueryRow(`
		SELECT old_value IS NULL FROM item_history
		WHERE item_id = ? AND field_name = 'attachment_uploaded'
	`, itemID).Scan(&uploadOldValueIsNull); err != nil {
		t.Fatalf("query upload history: %v", err)
	}
	if !uploadOldValueIsNull {
		t.Fatal("upload old_value is not NULL")
	}

	var deletedOldValue string
	if err := tdb.QueryRow(`
		SELECT old_value FROM item_history
		WHERE item_id = ? AND field_name = 'attachment_deleted'
	`, itemID).Scan(&deletedOldValue); err != nil {
		t.Fatalf("query delete history: %v", err)
	}
	if deletedOldValue != filename {
		t.Fatalf("delete old_value = %q, want %q", deletedOldValue, filename)
	}
}
