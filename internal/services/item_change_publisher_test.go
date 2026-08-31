//go:build test

package services

import (
	"sync"
	"testing"

	"windshift/internal/database"
)

// itemChangeSpy is a test ItemChangePublisher that records every published
// (itemID, kind). It guards the Plan 1 (WI-483) requirement that every item
// mutation chokepoint publishes a live-update event after commit — a write path
// that bypasses publishing (e.g. a direct-SQL comment write) makes the relevant
// assertion below fail.
type itemChangeSpy struct {
	mu     sync.Mutex
	events []itemChangeRecord
}

type itemChangeRecord struct {
	itemID int
	kind   ItemChangeKind
}

func (s *itemChangeSpy) PublishItemChange(itemID int, kind ItemChangeKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, itemChangeRecord{itemID: itemID, kind: kind})
}

func (s *itemChangeSpy) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
}

func (s *itemChangeSpy) has(itemID int, kind ItemChangeKind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.itemID == itemID && e.kind == kind {
			return true
		}
	}
	return false
}

func (s *itemChangeSpy) snapshot() []itemChangeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]itemChangeRecord(nil), s.events...)
}

func (s *itemChangeSpy) assertHas(t *testing.T, itemID int, kind ItemChangeKind) {
	t.Helper()
	if !s.has(itemID, kind) {
		t.Errorf("expected publish (item=%d, kind=%s); got %v", itemID, kind, s.snapshot())
	}
}

// installItemChangeSpy swaps the process publisher for a spy and restores the
// no-op default after the test.
func installItemChangeSpy(t *testing.T) *itemChangeSpy {
	t.Helper()
	spy := &itemChangeSpy{}
	SetItemChangePublisher(spy)
	t.Cleanup(func() { SetItemChangePublisher(nil) })
	return spy
}

func defaultStatusID(t *testing.T, db database.Database) int {
	t.Helper()
	var id int
	if err := db.QueryRow("SELECT id FROM statuses WHERE is_default = true LIMIT 1").Scan(&id); err != nil {
		t.Fatalf("get default status: %v", err)
	}
	return id
}

// itemTypeIDAtLevel returns the id of a seeded item type at the given hierarchy
// level (0=Initiative, 1=Epic, 2=Story, 3=Task, ...). Used to build a valid
// parent/child pair, since the hierarchy validator requires a child to sit
// strictly deeper than its parent.
func itemTypeIDAtLevel(t *testing.T, db database.Database, level int) int {
	t.Helper()
	var id int
	if err := db.QueryRow("SELECT id FROM item_types WHERE hierarchy_level = ? ORDER BY id LIMIT 1", level).Scan(&id); err != nil {
		t.Skipf("no seeded item type at hierarchy level %d: %v", level, err)
	}
	return id
}

func TestItemChangePublish_CreateItem_AnnouncesItemAndParent(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupNotificationEmissionTestEnv(t, db)
	spy := installItemChangeSpy(t)
	status := defaultStatusID(t, db)

	parentID := env.itemID
	childID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: env.workspaceID,
		Title:       "Child item",
		StatusID:    &status,
		CreatorID:   &env.userID,
		ParentID:    &parentID,
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	spy.assertHas(t, int(childID), ItemChangeCreated)
	spy.assertHas(t, parentID, ItemChangeUpdated) // parent's child list must refresh
}

func TestItemChangePublish_UpdateItem(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupNotificationEmissionTestEnv(t, db)
	spy := installItemChangeSpy(t)

	_, err := NewItemUpdateService(db).UpdateItem(UpdateItemRequest{
		ItemID:     env.itemID,
		UpdateData: map[string]interface{}{"title": "Renamed"},
		UserID:     env.userID,
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	spy.assertHas(t, env.itemID, ItemChangeUpdated)
}

func TestItemChangePublish_UpdateItem_ReparentNotifiesBothParents(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupNotificationEmissionTestEnv(t, db)
	status := defaultStatusID(t, db)
	parentType := itemTypeIDAtLevel(t, db, 2) // Story
	childType := itemTypeIDAtLevel(t, db, 3)  // Task (strictly deeper)

	// Two valid parents and a child under the first one.
	oldParentID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: env.workspaceID, ItemTypeID: &parentType, Title: "Old parent", StatusID: &status, CreatorID: &env.userID,
	})
	if err != nil {
		t.Fatalf("create old parent: %v", err)
	}
	oldParent := int(oldParentID)
	newParentID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: env.workspaceID, ItemTypeID: &parentType, Title: "New parent", StatusID: &status, CreatorID: &env.userID,
	})
	if err != nil {
		t.Fatalf("create new parent: %v", err)
	}
	childID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: env.workspaceID, ItemTypeID: &childType, Title: "Child", StatusID: &status, CreatorID: &env.userID, ParentID: &oldParent,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	spy := installItemChangeSpy(t)
	newParent := int(newParentID)
	if _, err := NewItemUpdateService(db).UpdateItem(UpdateItemRequest{
		ItemID:     int(childID),
		UpdateData: map[string]interface{}{"parent_id": newParent},
		UserID:     env.userID,
	}); err != nil {
		t.Fatalf("reparent: %v", err)
	}

	spy.assertHas(t, int(childID), ItemChangeUpdated)
	spy.assertHas(t, oldParent, ItemChangeUpdated)
	spy.assertHas(t, newParent, ItemChangeUpdated)
}

func TestItemChangePublish_DeleteSingle_AnnouncesItemAndParent(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupNotificationEmissionTestEnv(t, db)
	status := defaultStatusID(t, db)

	parentID := env.itemID
	childID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: env.workspaceID, Title: "Child", StatusID: &status, CreatorID: &env.userID, ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	spy := installItemChangeSpy(t)
	if err := NewItemCRUDService(db).DeleteSingle(int(childID)); err != nil {
		t.Fatalf("DeleteSingle: %v", err)
	}

	spy.assertHas(t, int(childID), ItemChangeDeleted)
	spy.assertHas(t, parentID, ItemChangeUpdated)
}

func TestItemChangePublish_DeleteCascade(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupNotificationEmissionTestEnv(t, db)
	status := defaultStatusID(t, db)

	parentID := env.itemID
	childID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: env.workspaceID, Title: "Child", StatusID: &status, CreatorID: &env.userID, ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	spy := installItemChangeSpy(t)
	if _, err := NewItemCRUDService(db).Delete(int(childID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	spy.assertHas(t, int(childID), ItemChangeDeleted)
	spy.assertHas(t, parentID, ItemChangeUpdated)
}

// TestItemChangePublish_CommentLifecycle is the core regression guard the WI-483
// amendment calls for: comment create / update / delete must each publish a
// comment item-change. CommentService.Update/Delete had NO event surface before
// this change, and the cookie handler writes comments with direct SQL — so if a
// future change drops a publish from any of these, this test fails.
func TestItemChangePublish_CommentLifecycle(t *testing.T) {
	db := createNotificationEmissionTestDB(t)
	defer func() { _ = db.Close() }()
	env := setupNotificationEmissionTestEnv(t, db)
	cs := NewCommentService(db)

	spy := installItemChangeSpy(t)
	res, err := cs.Create(CreateCommentParams{
		ItemID:                env.itemID,
		AuthorID:              env.userID,
		Content:               "first comment",
		ActorUserID:           env.userID,
		SuppressNotifications: true,
	})
	if err != nil {
		t.Fatalf("comment Create: %v", err)
	}
	spy.assertHas(t, env.itemID, ItemChangeComment)

	spy.reset()
	if _, err := cs.Update(int(res.CommentID), "edited comment", env.userID); err != nil {
		t.Fatalf("comment Update: %v", err)
	}
	spy.assertHas(t, env.itemID, ItemChangeComment)

	spy.reset()
	if err := cs.Delete(int(res.CommentID)); err != nil {
		t.Fatalf("comment Delete: %v", err)
	}
	spy.assertHas(t, env.itemID, ItemChangeComment) // item id captured before the destructive write
}

// TestItemChangePublish_NoopDefault verifies the process default is a no-op so
// Plan 1 ships with zero behavior change until Plan 2 installs the hub.
func TestItemChangePublish_NoopDefault(t *testing.T) {
	SetItemChangePublisher(nil)
	// Must not panic and must accept an ignored zero id.
	PublishItemChange(0, ItemChangeUpdated)
	PublishItemChange(123, ItemChangeCreated)
}
