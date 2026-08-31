package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
)

// itemLinkServiceTestDB opens a fresh in-memory SQLite, runs Initialize so
// the seeded link_types (notably id=1 "Tests") exist, and returns it.
func itemLinkServiceTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:linksvc-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return db
}

func TestItemLinkService_CreateLink_Type1RejectsItemToItem(t *testing.T) {
	db := itemLinkServiceTestDB(t)
	svc := NewItemLinkService(db)
	// Item↔item with the Tests link type — must be rejected, this is the
	// exact gap that allowed the AI AcceptDependencies handler to create
	// semantically invalid Tests links.
	_, err := svc.CreateLink(CreateItemLinkParams{
		LinkTypeID: 1,
		SourceType: "item",
		SourceID:   1,
		TargetType: "item",
		TargetID:   2,
	})
	if !errors.Is(err, ErrInvalidLinkTypeForEntities) {
		t.Fatalf("expected ErrInvalidLinkTypeForEntities, got %v", err)
	}
}

func TestItemLinkService_CreateLink_Type1AllowsItemToTestCase(t *testing.T) {
	db := itemLinkServiceTestDB(t)

	// Seed enough rows so foreign keys / referential integrity (where it
	// exists) hold; the service does not actually validate the entity rows
	// exist, but the table has FKs.
	if _, err := db.Exec(`INSERT INTO workspaces (name, key, active, is_personal) VALUES ('ws', 'WS', true, false)`); err != nil {
		t.Fatalf("workspace: %v", err)
	}

	svc := NewItemLinkService(db)
	_, err := svc.CreateLink(CreateItemLinkParams{
		LinkTypeID: 1,
		SourceType: "item",
		SourceID:   1,
		TargetType: "test_case",
		TargetID:   1,
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
}

func TestItemLinkService_CreateLink_NonTestsAllowsItemToItem(t *testing.T) {
	db := itemLinkServiceTestDB(t)
	svc := NewItemLinkService(db)
	// link_type_id=2 is "Implements" (seeded). Item↔item is allowed.
	_, err := svc.CreateLink(CreateItemLinkParams{
		LinkTypeID: 2,
		SourceType: "item",
		SourceID:   1,
		TargetType: "item",
		TargetID:   2,
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
}

// pageLinkTypeID looks up the seeded "Page" link type so tests stay
// independent of insert order in the seed loop.
func pageLinkTypeID(t *testing.T, db database.Database) int {
	t.Helper()
	var id int
	if err := db.QueryRow("SELECT id FROM link_types WHERE name='Page'").Scan(&id); err != nil {
		t.Fatalf("lookup Page link type: %v", err)
	}
	return id
}

func TestItemLinkService_CreateLink_PageAllowsItemPage(t *testing.T) {
	db := itemLinkServiceTestDB(t)
	svc := NewItemLinkService(db)
	id, err := svc.CreateLink(CreateItemLinkParams{
		LinkTypeID: pageLinkTypeID(t, db),
		SourceType: "item",
		SourceID:   1,
		TargetType: "page",
		TargetID:   2,
	})
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected new link id, got 0")
	}
}

func TestItemLinkService_CreateLink_PageRejectsItemItem(t *testing.T) {
	db := itemLinkServiceTestDB(t)
	svc := NewItemLinkService(db)
	_, err := svc.CreateLink(CreateItemLinkParams{
		LinkTypeID: pageLinkTypeID(t, db),
		SourceType: "item",
		SourceID:   1,
		TargetType: "item",
		TargetID:   2,
	})
	if !errors.Is(err, ErrInvalidLinkTypeForEntities) {
		t.Fatalf("expected ErrInvalidLinkTypeForEntities, got %v", err)
	}
}

func TestItemLinkService_CreateLink_PageRejectsPagePage(t *testing.T) {
	db := itemLinkServiceTestDB(t)
	svc := NewItemLinkService(db)
	_, err := svc.CreateLink(CreateItemLinkParams{
		LinkTypeID: pageLinkTypeID(t, db),
		SourceType: "page",
		SourceID:   1,
		TargetType: "page",
		TargetID:   2,
	})
	if !errors.Is(err, ErrInvalidLinkTypeForEntities) {
		t.Fatalf("expected ErrInvalidLinkTypeForEntities, got %v", err)
	}
}
