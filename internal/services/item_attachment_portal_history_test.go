//go:build test

package services_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// seedPortalAttachmentItem creates the workspace data, attachment settings, a
// portal customer, and one item the portal customer owns. Returns the item ID
// and portal customer ID.
func seedPortalAttachmentItem(t *testing.T, tdb *testutils.TestDB, attachmentPath string) (int, int) {
	t.Helper()
	data := tdb.SeedTestData(t)

	if _, err := tdb.ExecWrite(`DELETE FROM attachment_settings`); err != nil {
		t.Fatalf("clear attachment settings: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO attachment_settings (max_file_size, allowed_mime_types, attachment_path, enabled)
		VALUES (5242880, '["text/plain; charset=utf-8","text/plain"]', ?, true)
	`, attachmentPath); err != nil {
		t.Fatalf("insert attachment settings: %v", err)
	}

	var customerID int
	if err := tdb.QueryRow(`
		INSERT INTO portal_customers (name, email) VALUES ('Cara Customer', ?) RETURNING id
	`, "cara-"+strings.ReplaceAll(t.Name(), "/", "_")+"@example.com").Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}

	f := factory.NewTestFactory(tdb.GetDatabase())
	itemID, err := f.CreateItem(factory.CreateItemOpts{
		WorkspaceID: data.WorkspaceID,
		Title:       "Portal upload target",
		StatusID:    &data.StatusID,
		CreatorID:   &data.UserID,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	return itemID, customerID
}

func TestUploadPublicFormAttachment_AttributesPortalCustomerUpload(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	attachmentPath := t.TempDir()
	itemID, customerID := seedPortalAttachmentItem(t, tdb, attachmentPath)

	svc := services.NewItemAttachmentService(tdb.GetDatabase(), blobstore.NewLocal(attachmentPath), nil)
	response, err := svc.UploadPublicFormAttachment(context.Background(), services.ItemAttachmentUploadInput{
		ItemID:                   itemID,
		UploaderPortalCustomerID: &customerID,
		OriginalFilename:         "evidence.txt",
		FileData:                 []byte("customer evidence\n"),
		FileSize:                 int64(len("customer evidence\n")),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	var uploadedBy, uploadedByCustomer *int
	if err := tdb.QueryRow(`
		SELECT uploaded_by, uploaded_by_portal_customer_id FROM attachments WHERE id = ?
	`, response.Attachment.ID).Scan(&uploadedBy, &uploadedByCustomer); err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if uploadedBy != nil {
		t.Fatalf("uploaded_by = %d, want NULL for a portal-customer upload", *uploadedBy)
	}
	if uploadedByCustomer == nil || *uploadedByCustomer != customerID {
		t.Fatalf("uploaded_by_portal_customer_id = %v, want %d", uploadedByCustomer, customerID)
	}

	var fieldName string
	var userID, actorCustomerID *int
	var actorKind string
	var newValue string
	if err := tdb.QueryRow(`
		SELECT field_name, user_id, actor_kind, actor_portal_customer_id, new_value
		FROM item_history WHERE item_id = ? AND field_name = 'attachment_uploaded'
	`, itemID).Scan(&fieldName, &userID, &actorKind, &actorCustomerID, &newValue); err != nil {
		t.Fatalf("read upload history row: %v", err)
	}
	if userID != nil {
		t.Fatalf("history user_id = %d, want NULL", *userID)
	}
	if actorKind != "portal_customer" {
		t.Fatalf("actor_kind = %q, want portal_customer", actorKind)
	}
	if actorCustomerID == nil || *actorCustomerID != customerID {
		t.Fatalf("actor_portal_customer_id = %v, want %d", actorCustomerID, customerID)
	}
	wantValue := "attachment:" + strconv.Itoa(int(response.Attachment.ID)) + ":evidence.txt"
	if newValue != wantValue {
		t.Fatalf("new_value = %q, want %q", newValue, wantValue)
	}
}

func TestUploadPublicFormAttachment_RecordsAnonymousUploadAsSystemActor(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	attachmentPath := t.TempDir()
	itemID, _ := seedPortalAttachmentItem(t, tdb, attachmentPath)

	svc := services.NewItemAttachmentService(tdb.GetDatabase(), blobstore.NewLocal(attachmentPath), nil)
	response, err := svc.UploadPublicFormAttachment(context.Background(), services.ItemAttachmentUploadInput{
		ItemID:           itemID,
		OriginalFilename: "anon.txt",
		FileData:         []byte("anonymous upload\n"),
		FileSize:         int64(len("anonymous upload\n")),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	var uploadedBy, uploadedByCustomer *int
	if err := tdb.QueryRow(`
		SELECT uploaded_by, uploaded_by_portal_customer_id FROM attachments WHERE id = ?
	`, response.Attachment.ID).Scan(&uploadedBy, &uploadedByCustomer); err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if uploadedBy != nil || uploadedByCustomer != nil {
		t.Fatalf("uploader columns = %v/%v, want NULL/NULL for an anonymous upload", uploadedBy, uploadedByCustomer)
	}

	var userID *int
	var actorKind string
	if err := tdb.QueryRow(`
		SELECT user_id, actor_kind FROM item_history
		WHERE item_id = ? AND field_name = 'attachment_uploaded'
	`, itemID).Scan(&userID, &actorKind); err != nil {
		t.Fatalf("read upload history row: %v", err)
	}
	if userID != nil {
		t.Fatalf("history user_id = %d, want NULL", *userID)
	}
	if actorKind != "system" {
		t.Fatalf("actor_kind = %q, want system", actorKind)
	}
}
