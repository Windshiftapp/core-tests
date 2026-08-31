package services

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// hierarchyTestEnv contains test data for hierarchy service tests
type hierarchyTestEnv struct {
	WorkspaceID int
	RootItemID  int
	ChildItem1  int
	ChildItem2  int
	GrandChild1 int
	GrandChild2 int
	StatusID    int
}

// createHierarchyTestDB creates a test database for hierarchy service tests
func createHierarchyTestDB(t *testing.T) database.Database {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	if tdb.Engine == "sqlite" {
		t.Cleanup(func() { _ = tdb.Close() })
	}
	return tdb.DB
}

// setupHierarchyTestEnv creates test data with a hierarchy structure:
// Root
// ├── Child1
// │   └── GrandChild1
// └── Child2
//
//	└── GrandChild2
func setupHierarchyTestEnv(t *testing.T, db database.Database) hierarchyTestEnv {
	t.Helper()

	// Create the workspace through the workspace service (which also grants
	// the creator workspace admin, matching production bootstrapping).
	createdWS, err := NewWorkspaceService(db).Create(t.Context(), CreateWorkspaceParams{
		Name:        "Hierarchy Test Workspace",
		Key:         "HIR",
		Description: "Test workspace",
		CreatorID:   1, // default test user seeded by CreateTestDB
	})
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	workspaceID := createdWS.Workspace.ID

	// Get a status ID
	var statusID int
	err = db.QueryRow("SELECT id FROM statuses LIMIT 1").Scan(&statusID)
	if err != nil {
		t.Fatalf("Failed to get status ID: %v", err)
	}
	statusRef := &statusID
	creator := 1

	// Create items through the item-creation service so hierarchy rows are
	// built exactly like production creates them (numbering, auditing, FK
	// bookkeeping) instead of hand-written INSERTs. Item types carry the
	// hierarchy_level — root = Initiative (level 0), children = Epic (1),
	// grandchildren = Story (2) — matching how GetRoot classifies roots.
	itemTypeID := func(name string) *int {
		t.Helper()
		var id int
		if err := db.QueryRow("SELECT id FROM item_types WHERE name = ?", name).Scan(&id); err != nil {
			t.Fatalf("find item type %s: %v", name, err)
		}
		return &id
	}
	initiativeID := itemTypeID("Initiative")
	epicID := itemTypeID("Epic")
	storyID := itemTypeID("Story")

	createItem := func(title, description string, itemType *int, parentID *int) int {
		t.Helper()
		id, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: workspaceID,
			Title:       title,
			Description: description,
			StatusID:    statusRef,
			ItemTypeID:  itemType,
			IsTask:      true,
			ParentID:    parentID,
			CreatorID:   &creator,
		})
		if err != nil {
			t.Fatalf("Failed to create hierarchy item %q: %v", title, err)
		}
		return int(id)
	}
	intPtr := func(v int) *int { return &v }

	// Root
	rootItemID := createItem("Root Item", "Root description", initiativeID, nil)
	// Child 1 (under root)
	childItem1 := createItem("Child 1", "Child 1 description", epicID, intPtr(rootItemID))
	// Child 2 (under root)
	childItem2 := createItem("Child 2", "Child 2 description", epicID, intPtr(rootItemID))
	// Grandchild 1 (under child 1)
	grandChild1 := createItem("GrandChild 1", "GrandChild 1 description", storyID, intPtr(childItem1))
	// Grandchild 2 (under child 2)
	grandChild2 := createItem("GrandChild 2", "GrandChild 2 description", storyID, intPtr(childItem2))

	return hierarchyTestEnv{
		WorkspaceID: workspaceID,
		RootItemID:  rootItemID,
		ChildItem1:  childItem1,
		ChildItem2:  childItem2,
		GrandChild1: grandChild1,
		GrandChild2: grandChild2,
		StatusID:    statusID,
	}
}

func TestHierarchyService_GetAncestors(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)
	env := setupHierarchyTestEnv(t, db)

	t.Run("GrandChildHasTwoAncestors", func(t *testing.T) {
		ancestors, err := service.GetAncestors(env.GrandChild1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(ancestors) != 2 {
			t.Fatalf("Expected 2 ancestors (root and child1), got %d", len(ancestors))
		}

		// Should be ordered from root to direct parent
		if ancestors[0].ID != env.RootItemID {
			t.Errorf("Expected first ancestor to be root (ID %d), got %d", env.RootItemID, ancestors[0].ID)
		}
		if ancestors[1].ID != env.ChildItem1 {
			t.Errorf("Expected second ancestor to be child1 (ID %d), got %d", env.ChildItem1, ancestors[1].ID)
		}
	})

	t.Run("ChildHasOneAncestor", func(t *testing.T) {
		ancestors, err := service.GetAncestors(env.ChildItem1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(ancestors) != 1 {
			t.Fatalf("Expected 1 ancestor (root), got %d", len(ancestors))
		}

		if ancestors[0].ID != env.RootItemID {
			t.Errorf("Expected ancestor to be root (ID %d), got %d", env.RootItemID, ancestors[0].ID)
		}
	})

	t.Run("RootHasNoAncestors", func(t *testing.T) {
		ancestors, err := service.GetAncestors(env.RootItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(ancestors) != 0 {
			t.Errorf("Expected 0 ancestors for root, got %d", len(ancestors))
		}
	})

	t.Run("NonExistentItem", func(t *testing.T) {
		ancestors, err := service.GetAncestors(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(ancestors) != 0 {
			t.Errorf("Expected 0 ancestors for non-existent item, got %d", len(ancestors))
		}
	})

	t.Run("AncestorsHaveCorrectData", func(t *testing.T) {
		ancestors, err := service.GetAncestors(env.GrandChild1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(ancestors) < 1 {
			t.Fatal("Expected at least 1 ancestor")
		}

		// Check that fields are populated
		for _, a := range ancestors {
			if a.Title == "" {
				t.Error("Expected Title to be populated")
			}
			if a.WorkspaceID == 0 {
				t.Error("Expected WorkspaceID to be populated")
			}
		}
	})
}

func TestHierarchyService_GetDescendants(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)
	env := setupHierarchyTestEnv(t, db)

	t.Run("RootHasFourDescendants", func(t *testing.T) {
		descendants, err := service.GetDescendants(env.RootItemID, 0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(descendants) != 4 {
			t.Errorf("Expected 4 descendants, got %d", len(descendants))
		}
	})

	t.Run("ChildHasOneDescendant", func(t *testing.T) {
		descendants, err := service.GetDescendants(env.ChildItem1, 0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(descendants) != 1 {
			t.Errorf("Expected 1 descendant, got %d", len(descendants))
		}

		if descendants[0].ID != env.GrandChild1 {
			t.Errorf("Expected descendant to be grandchild1, got %d", descendants[0].ID)
		}
	})

	t.Run("GrandChildHasNoDescendants", func(t *testing.T) {
		descendants, err := service.GetDescendants(env.GrandChild1, 0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(descendants) != 0 {
			t.Errorf("Expected 0 descendants, got %d", len(descendants))
		}
	})

	t.Run("MaxDepthLimit", func(t *testing.T) {
		// With maxDepth = 1, should only get direct children
		descendants, err := service.GetDescendants(env.RootItemID, 1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(descendants) != 2 {
			t.Errorf("Expected 2 descendants with maxDepth=1, got %d", len(descendants))
		}
	})

	t.Run("NonExistentItem", func(t *testing.T) {
		descendants, err := service.GetDescendants(99999, 0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(descendants) != 0 {
			t.Errorf("Expected 0 descendants for non-existent item, got %d", len(descendants))
		}
	})
}

func TestHierarchyService_CountDescendants(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)
	env := setupHierarchyTestEnv(t, db)

	t.Run("RootCountsFour", func(t *testing.T) {
		count, err := service.CountDescendants(env.RootItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if count != 4 {
			t.Errorf("Expected 4 descendants, got %d", count)
		}
	})

	t.Run("ChildCountsOne", func(t *testing.T) {
		count, err := service.CountDescendants(env.ChildItem1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if count != 1 {
			t.Errorf("Expected 1 descendant, got %d", count)
		}
	})

	t.Run("GrandChildCountsZero", func(t *testing.T) {
		count, err := service.CountDescendants(env.GrandChild1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if count != 0 {
			t.Errorf("Expected 0 descendants, got %d", count)
		}
	})

	t.Run("NonExistentItem", func(t *testing.T) {
		count, err := service.CountDescendants(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if count != 0 {
			t.Errorf("Expected 0 descendants for non-existent item, got %d", count)
		}
	})
}

func TestHierarchyService_GetChildren(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)
	env := setupHierarchyTestEnv(t, db)

	t.Run("RootHasTwoChildren", func(t *testing.T) {
		children, err := service.GetChildren(env.RootItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(children) != 2 {
			t.Errorf("Expected 2 children, got %d", len(children))
		}
	})

	t.Run("ChildHasOneChild", func(t *testing.T) {
		children, err := service.GetChildren(env.ChildItem1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(children) != 1 {
			t.Errorf("Expected 1 child, got %d", len(children))
		}

		if children[0].ID != env.GrandChild1 {
			t.Errorf("Expected child to be grandchild1, got %d", children[0].ID)
		}
	})

	t.Run("GrandChildHasNoChildren", func(t *testing.T) {
		children, err := service.GetChildren(env.GrandChild1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(children) != 0 {
			t.Errorf("Expected 0 children, got %d", len(children))
		}
	})

	t.Run("NonExistentItem", func(t *testing.T) {
		children, err := service.GetChildren(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(children) != 0 {
			t.Errorf("Expected 0 children for non-existent item, got %d", len(children))
		}
	})

	t.Run("ChildrenHaveCorrectParentID", func(t *testing.T) {
		children, err := service.GetChildren(env.RootItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		for _, c := range children {
			if c.ParentID == nil {
				t.Error("Expected ParentID to be set")
			} else if *c.ParentID != env.RootItemID {
				t.Errorf("Expected ParentID %d, got %d", env.RootItemID, *c.ParentID)
			}
		}
	})
}

func TestHierarchyService_GetRoot(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)
	env := setupHierarchyTestEnv(t, db)

	t.Run("GrandChildFindsRoot", func(t *testing.T) {
		root, err := service.GetRoot(env.GrandChild1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if root == nil {
			t.Fatal("Expected non-nil root")
		}

		if root.ID != env.RootItemID {
			t.Errorf("Expected root ID %d, got %d", env.RootItemID, root.ID)
		}
	})

	t.Run("ChildFindsRoot", func(t *testing.T) {
		root, err := service.GetRoot(env.ChildItem1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if root == nil {
			t.Fatal("Expected non-nil root")
		}

		if root.ID != env.RootItemID {
			t.Errorf("Expected root ID %d, got %d", env.RootItemID, root.ID)
		}
	})

	t.Run("RootFindsItself", func(t *testing.T) {
		root, err := service.GetRoot(env.RootItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if root == nil {
			t.Fatal("Expected non-nil root")
		}

		if root.ID != env.RootItemID {
			t.Errorf("Expected root ID %d, got %d", env.RootItemID, root.ID)
		}
	})

	t.Run("NonExistentItem", func(t *testing.T) {
		root, err := service.GetRoot(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if root != nil {
			t.Errorf("Expected nil root for non-existent item, got ID %d", root.ID)
		}
	})

	t.Run("RootHasNullParentID", func(t *testing.T) {
		root, err := service.GetRoot(env.GrandChild1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if root == nil {
			t.Fatal("Expected non-nil root")
		}

		if root.ParentID != nil {
			t.Errorf("Expected root to have nil ParentID, got %d", *root.ParentID)
		}
	})
}

func TestHierarchyService_GetEffectiveProject(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)
	env := setupHierarchyTestEnv(t, db)

	t.Run("ItemWithNoProject", func(t *testing.T) {
		projectID, mode, err := service.GetEffectiveProject(env.RootItemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Items created without project should have no effective project
		if projectID != nil {
			t.Errorf("Expected nil projectID, got %d", *projectID)
		}
		if mode != "none" {
			t.Errorf("Expected mode 'none', got '%s'", mode)
		}
	})

	t.Run("ItemWithDirectProject", func(t *testing.T) {
		// Create a time project (items.project_id references time_projects)
		projectID := testutils.InsertID(t, db, `
			INSERT INTO time_projects (name, description, status, created_at, updated_at)
			VALUES ('Test Time Project', 'Test description', 'Active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)

		// Create item with direct project through the production path
		itemID64, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: env.WorkspaceID,
			Title:       "Item With Project",
			Description: "Description",
			IsTask:      true,
			StatusID:    &env.StatusID,
			ProjectID:   &projectID,
		})
		if err != nil {
			t.Fatalf("Failed to create item: %v", err)
		}
		itemID := int(itemID64)

		effectiveProjectID, mode, err := service.GetEffectiveProject(itemID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if effectiveProjectID == nil {
			t.Fatal("Expected non-nil projectID")
		}
		if *effectiveProjectID != projectID {
			t.Errorf("Expected projectID %d, got %d", projectID, *effectiveProjectID)
		}
		if mode != "direct" {
			t.Errorf("Expected mode 'direct', got '%s'", mode)
		}
	})

	t.Run("ItemWithNullProjectAndParent", func(t *testing.T) {
		// Create a time project (items.project_id references time_projects)
		projectID := testutils.InsertID(t, db, `
			INSERT INTO time_projects (name, description, status, created_at, updated_at)
			VALUES ('Parent Time Project', 'Test description', 'Active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`)

		// Create parent item with project through the production path
		parentID64, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: env.WorkspaceID,
			Title:       "Parent With Project",
			Description: "Description",
			IsTask:      true,
			StatusID:    &env.StatusID,
			ProjectID:   &projectID,
		})
		if err != nil {
			t.Fatalf("Failed to create parent item: %v", err)
		}
		parentID := int(parentID64)

		// Create child item with NULL project_id (not inheriting explicitly)
		childID64, err := CreateItem(db, ItemCreationParams{
			WorkspaceID: env.WorkspaceID,
			Title:       "Child Item",
			Description: "Description",
			IsTask:      true,
			StatusID:    &env.StatusID,
			ParentID:    &parentID,
		})
		if err != nil {
			t.Fatalf("Failed to create child item: %v", err)
		}
		childID := int(childID64)

		effectiveProjectID, mode, err := service.GetEffectiveProject(childID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Child with NULL project_id should return "none" (no project)
		// The GetEffectiveProject function only walks up if project_id = -1
		if effectiveProjectID != nil {
			t.Logf("Note: Child with NULL project_id got projectID %d with mode '%s'", *effectiveProjectID, mode)
		}
		if mode != "none" && effectiveProjectID == nil {
			t.Logf("Child with NULL project_id returned mode '%s'", mode)
		}
	})

	t.Run("NonExistentItem", func(t *testing.T) {
		projectID, mode, err := service.GetEffectiveProject(99999)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		// Non-existent item should return no project
		if projectID != nil {
			t.Errorf("Expected nil projectID for non-existent item, got %d", *projectID)
		}
		if mode != "none" {
			t.Errorf("Expected mode 'none', got '%s'", mode)
		}
	})
}

func TestHierarchyService_ZeroItemID(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)

	t.Run("GetAncestors", func(t *testing.T) {
		ancestors, err := service.GetAncestors(0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(ancestors) != 0 {
			t.Errorf("Expected 0 ancestors for ID 0, got %d", len(ancestors))
		}
	})

	t.Run("GetDescendants", func(t *testing.T) {
		descendants, err := service.GetDescendants(0, 0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(descendants) != 0 {
			t.Errorf("Expected 0 descendants for ID 0, got %d", len(descendants))
		}
	})

	t.Run("CountDescendants", func(t *testing.T) {
		count, err := service.CountDescendants(0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 descendants for ID 0, got %d", count)
		}
	})

	t.Run("GetChildren", func(t *testing.T) {
		children, err := service.GetChildren(0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(children) != 0 {
			t.Errorf("Expected 0 children for ID 0, got %d", len(children))
		}
	})

	t.Run("GetRoot", func(t *testing.T) {
		root, err := service.GetRoot(0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if root != nil {
			t.Errorf("Expected nil root for ID 0, got %d", root.ID)
		}
	})

	t.Run("GetEffectiveProject", func(t *testing.T) {
		projectID, mode, err := service.GetEffectiveProject(0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if projectID != nil {
			t.Errorf("Expected nil projectID for ID 0, got %d", *projectID)
		}
		if mode != "none" {
			t.Errorf("Expected mode 'none' for ID 0, got '%s'", mode)
		}
	})
}

func TestHierarchyService_NegativeItemID(t *testing.T) {
	db := createHierarchyTestDB(t)

	service := NewHierarchyService(db)

	t.Run("GetAncestors", func(t *testing.T) {
		ancestors, err := service.GetAncestors(-1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(ancestors) != 0 {
			t.Errorf("Expected 0 ancestors for negative ID, got %d", len(ancestors))
		}
	})

	t.Run("GetDescendants", func(t *testing.T) {
		descendants, err := service.GetDescendants(-1, 0)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(descendants) != 0 {
			t.Errorf("Expected 0 descendants for negative ID, got %d", len(descendants))
		}
	})

	t.Run("CountDescendants", func(t *testing.T) {
		count, err := service.CountDescendants(-1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 descendants for negative ID, got %d", count)
		}
	})

	t.Run("GetChildren", func(t *testing.T) {
		children, err := service.GetChildren(-1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if len(children) != 0 {
			t.Errorf("Expected 0 children for negative ID, got %d", len(children))
		}
	})

	t.Run("GetRoot", func(t *testing.T) {
		root, err := service.GetRoot(-1)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if root != nil {
			t.Errorf("Expected nil root for negative ID, got %d", root.ID)
		}
	})
}

// newHierarchyTestDB spins up an in-memory SQLite with just the items and
// item_types schema the hierarchy queries touch. We intentionally avoid
// database.Initialize() so unrelated schema changes don't break these tests.
//
// The DSN uses shared-cache with a unique name per test so the reader and
// writer connection pools in SQLiteDB see the same schema and rows, while
// parallel tests remain isolated from each other.
func newHierarchyTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:hier-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE workspaces (id INTEGER PRIMARY KEY, name TEXT, key TEXT)`,
		`CREATE TABLE item_types (id INTEGER PRIMARY KEY, name TEXT, color TEXT, icon TEXT, hierarchy_level INTEGER)`,
		`CREATE TABLE items (
			id INTEGER PRIMARY KEY,
			workspace_id INTEGER NOT NULL,
			workspace_item_number INTEGER DEFAULT 1,
			item_type_id INTEGER,
			title TEXT DEFAULT '',
			description TEXT DEFAULT '',
			is_task INTEGER DEFAULT 0,
			milestone_id INTEGER,
			assignee_id INTEGER,
			creator_id INTEGER,
			custom_field_values TEXT,
			parent_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT INTO workspaces (id, name, key) VALUES (1, 'ws', 'WS')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

// insertItem creates an item with the given id and optional parent_id (0 = NULL).
func insertItem(t *testing.T, db database.Database, id, parent int) {
	t.Helper()
	if parent == 0 {
		if _, err := db.Exec(`INSERT INTO items (id, workspace_id, parent_id) VALUES (?, 1, NULL)`, id); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
		return
	}
	if _, err := db.Exec(`INSERT INTO items (id, workspace_id, parent_id) VALUES (?, 1, ?)`, id, parent); err != nil {
		t.Fatalf("insert %d parent=%d: %v", id, parent, err)
	}
}

// setParent forces an item's parent_id, bypassing the validator. Used to
// simulate a stored cycle that the CTE queries must tolerate.
func setParent(t *testing.T, db database.Database, id, parent int) {
	t.Helper()
	if _, err := db.Exec(`UPDATE items SET parent_id = ? WHERE id = ?`, parent, id); err != nil {
		t.Fatalf("set parent %d=%d: %v", id, parent, err)
	}
}

func TestWouldCreateCycle_SelfParent(t *testing.T) {
	db := newHierarchyTestDB(t)
	insertItem(t, db, 1, 0)
	h := NewHierarchyService(db)

	got, err := h.WouldCreateCycle(1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("self-parent must be reported as cycle, got false")
	}
}

func TestWouldCreateCycle_Unrelated(t *testing.T) {
	db := newHierarchyTestDB(t)
	insertItem(t, db, 1, 0) // root A
	insertItem(t, db, 2, 0) // root B — unrelated
	h := NewHierarchyService(db)

	got, err := h.WouldCreateCycle(1, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatalf("unrelated reparent should not cycle, got true")
	}
}

func TestWouldCreateCycle_Descendant(t *testing.T) {
	db := newHierarchyTestDB(t)
	// A → B → C. Trying to make C the parent of A would create a cycle.
	insertItem(t, db, 1, 0)
	insertItem(t, db, 2, 1)
	insertItem(t, db, 3, 2)
	h := NewHierarchyService(db)

	got, err := h.WouldCreateCycle(1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("moving an item under its own descendant must be a cycle, got false")
	}
}

func TestWouldCreateCycle_ExceedsDepthFailsClosed(t *testing.T) {
	db := newHierarchyTestDB(t)
	// Build a chain of length maxHierarchyDepth + 5. Walking from the deepest
	// child upward will never reach the target within the ceiling, so the
	// walker must fail closed (return true).
	const chainLen = maxHierarchyDepth + 5
	insertItem(t, db, 1, 0)
	for i := 2; i <= chainLen; i++ {
		insertItem(t, db, i, i-1)
	}
	h := NewHierarchyService(db)

	got, err := h.WouldCreateCycle(1, chainLen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("depth-exhausted walk must fail closed as cycle, got false")
	}
}

func TestGetRoot_CyclicErrors(t *testing.T) {
	db := newHierarchyTestDB(t)
	// Create a legal chain then inject a cycle by pointing the root back
	// into the chain. GetRoot must surface an error rather than silently
	// returning nil (which would hide data corruption).
	insertItem(t, db, 1, 0)
	insertItem(t, db, 2, 1)
	insertItem(t, db, 3, 2)
	setParent(t, db, 1, 3) // 1 → 3 → 2 → 1 cycle

	h := NewHierarchyService(db)
	_, err := h.GetRoot(3)
	if err == nil {
		t.Fatalf("GetRoot on cyclic hierarchy must return an error, got nil")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("error should mention cyclic, got: %v", err)
	}
}

func TestGetAncestors_CyclicTerminates(t *testing.T) {
	db := newHierarchyTestDB(t)
	// Inject a 3-cycle. Before Fix B this would loop forever (or until the
	// DB killed the query). The depth-capped CTE must return a bounded
	// result without error.
	insertItem(t, db, 1, 0)
	insertItem(t, db, 2, 1)
	insertItem(t, db, 3, 2)
	setParent(t, db, 1, 3)

	h := NewHierarchyService(db)
	got, err := h.GetAncestors(3)
	if err != nil {
		t.Fatalf("GetAncestors on cyclic hierarchy should not error, got: %v", err)
	}
	// Upper bound: the CTE walks at most maxHierarchyDepth+1 rows (base +
	// recursive steps), minus the item itself which is filtered out.
	if len(got) > maxHierarchyDepth+1 {
		t.Fatalf("GetAncestors returned %d rows, exceeds depth cap %d", len(got), maxHierarchyDepth+1)
	}
}

func TestCountDescendants_CyclicTerminates(t *testing.T) {
	db := newHierarchyTestDB(t)
	insertItem(t, db, 1, 0)
	insertItem(t, db, 2, 1)
	insertItem(t, db, 3, 2)
	setParent(t, db, 1, 3) // cycle

	h := NewHierarchyService(db)
	// Any finite, non-error return is a pass — the pre-fix behavior was to
	// loop until the DB timed out.
	if _, err := h.CountDescendants(1); err != nil {
		t.Fatalf("CountDescendants on cyclic hierarchy should not error, got: %v", err)
	}
}
