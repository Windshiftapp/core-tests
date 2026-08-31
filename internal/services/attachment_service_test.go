package services

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
)

// TestCreateRecord_NullsItemIDForBrandingTypes pins the WI-46 fix: when
// an attachment is created for a branding/avatar entity type, item_id is
// stored as NULL so it can't collide with a real work-item id and leak
// onto that item's attachment list via GetByItem.
func TestCreateRecord_NullsItemIDForBrandingTypes(t *testing.T) {
	dsn := fmt.Sprintf("file:wi46-create-record-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER,
			entity_type TEXT,
			filename TEXT,
			original_filename TEXT,
			file_path TEXT,
			mime_type TEXT,
			file_size INTEGER,
			uploaded_by INTEGER,
			has_thumbnail INTEGER,
			thumbnail_path TEXT,
			category TEXT
		)
	`); err != nil {
		t.Fatalf("create attachments table: %v", err)
	}

	svc := NewAttachmentService(db)

	brandingTypes := []string{
		"avatar",
		"workspace_avatar", "workspace_background",
		"team_avatar", "customer_avatar",
		"portal_background", "portal_logo", "hub_logo",
	}

	// The bug specifically required item_id to be the workspace id (so a
	// matching item id would leak). Use a non-zero ItemID to simulate the
	// frontend posting workspaceId.toString() into the item_id field.
	const ownerID = 42

	for _, et := range brandingTypes {
		id, err := svc.CreateRecord(CreateAttachmentParams{
			ItemID:           ownerID,
			EntityType:       et,
			Filename:         et + ".png",
			OriginalFilename: et + ".png",
			FilePath:         "/tmp/" + et + ".png",
			MimeType:         "image/png",
			FileSize:         1,
		})
		if err != nil {
			t.Fatalf("CreateRecord(%s): %v", et, err)
		}
		var stored sql.NullInt64
		if err := db.QueryRow(`SELECT item_id FROM attachments WHERE id = ?`, id).Scan(&stored); err != nil {
			t.Fatalf("read back %s row: %v", et, err)
		}
		if stored.Valid {
			t.Errorf("entity_type %q: item_id stored as %d, want NULL (would collide with work item %d via GetByItem)", et, stored.Int64, stored.Int64)
		}
	}

	// Sanity: item-type attachments keep their item_id so the regular
	// item-attachments list still works.
	id, err := svc.CreateRecord(CreateAttachmentParams{
		ItemID:           ownerID,
		EntityType:       "item",
		Filename:         "real.txt",
		OriginalFilename: "real.txt",
		FilePath:         "/tmp/real.txt",
		MimeType:         "text/plain",
		FileSize:         1,
	})
	if err != nil {
		t.Fatalf("CreateRecord(item): %v", err)
	}
	var stored sql.NullInt64
	if err := db.QueryRow(`SELECT item_id FROM attachments WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("read back item row: %v", err)
	}
	if !stored.Valid || stored.Int64 != ownerID {
		t.Errorf("entity_type=item: item_id stored as %v, want %d", stored, ownerID)
	}
}
