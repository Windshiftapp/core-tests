//go:build test

package services_test

import (
	"context"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// TestSetItemAttachmentInternalHidesFromPortal covers the service contract
// behind the v2 visibility endpoint: toggling is_internal removes the file
// from the portal list while workspace members keep seeing it flagged.
func TestSetItemAttachmentInternalHidesFromPortal(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	attachmentPath := t.TempDir()
	itemID, customerID := seedPortalAttachmentItem(t, tdb, attachmentPath)

	svc := services.NewItemAttachmentService(tdb.GetDatabase(), blobstore.NewLocal(attachmentPath), nil)
	uploaded, err := svc.UploadPublicFormAttachment(context.Background(), services.ItemAttachmentUploadInput{
		ItemID:                   itemID,
		UploaderPortalCustomerID: &customerID,
		OriginalFilename:         "evidence.txt",
		FileData:                 []byte("customer evidence\n"),
		FileSize:                 int64(len("customer evidence\n")),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	// A freshly uploaded customer file is portal-visible.
	portal, total, err := svc.ListPortalRequestAttachments(itemID, 50, 0)
	if err != nil {
		t.Fatalf("list portal attachments: %v", err)
	}
	if total != 1 || len(portal) != 1 {
		t.Fatalf("portal attachments = %d/%d, want 1/1", len(portal), total)
	}

	actor := 1
	updated, err := svc.SetItemAttachmentInternal(uploaded.Attachment.ID, actor, true)
	if err != nil {
		t.Fatalf("hide attachment: %v", err)
	}
	if !updated.IsInternal {
		t.Fatalf("returned attachment is_internal = false, want true")
	}

	portal, total, err = svc.ListPortalRequestAttachments(itemID, 50, 0)
	if err != nil {
		t.Fatalf("list portal attachments after hide: %v", err)
	}
	if total != 0 || len(portal) != 0 {
		t.Fatalf("portal attachments after hide = %d/%d, want 0/0", len(portal), total)
	}

	member, total, err := svc.ListItemAttachments(actor, itemID, 50, 0)
	if err != nil {
		t.Fatalf("list member attachments: %v", err)
	}
	if total != 1 || len(member) != 1 || !member[0].IsInternal {
		t.Fatalf("member attachments = %+v (total %d), want 1 internal", member, total)
	}

	// Revealing the file restores portal visibility.
	updated, err = svc.SetItemAttachmentInternal(uploaded.Attachment.ID, actor, false)
	if err != nil {
		t.Fatalf("reveal attachment: %v", err)
	}
	if updated.IsInternal {
		t.Fatalf("returned attachment is_internal = true, want false")
	}
	portal, total, err = svc.ListPortalRequestAttachments(itemID, 50, 0)
	if err != nil {
		t.Fatalf("list portal attachments after reveal: %v", err)
	}
	if total != 1 || len(portal) != 1 {
		t.Fatalf("portal attachments after reveal = %d/%d, want 1/1", len(portal), total)
	}

	if _, err := svc.SetItemAttachmentInternal(999999, actor, true); err == nil {
		t.Fatal("SetItemAttachmentInternal(missing) error = nil, want not found")
	}
}
