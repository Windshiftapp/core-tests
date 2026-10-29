//go:build test

package repository

import (
	"context"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

func TestApplyEffectiveConfigResolvesScreenFallbackChain(t *testing.T) {
	defaultCreate, defaultEdit, defaultView := 10, 11, 12
	typeCreate, typeEdit, typeView := 20, 21, 22
	itemTypeID := 7

	// item-type override → type create for edit/view → config-set level → create.
	t.Run("item type override with full screens", func(t *testing.T) {
		cs := &models.ConfigurationSet{
			ID: 3, CreateScreenID: &defaultCreate, EditScreenID: &defaultEdit, ViewScreenID: &defaultView,
			ItemTypeConfigs: []models.ItemTypeConfig{
				{ItemTypeID: itemTypeID, CreateScreenID: &typeCreate, EditScreenID: &typeEdit, ViewScreenID: &typeView},
			},
		}
		resolved := &EffectiveConfig{}
		applyEffectiveConfig(resolved, cs, &itemTypeID)

		if resolved.ConfigSetID == nil || *resolved.ConfigSetID != 3 {
			t.Fatalf("ConfigSetID = %v, want 3", resolved.ConfigSetID)
		}
		assertScreen(t, "create", resolved.CreateScreenID, typeCreate)
		assertScreen(t, "edit", resolved.EditScreenID, typeEdit)
		assertScreen(t, "view", resolved.ViewScreenID, typeView)
	})

	// A type override with only a create screen uses it for edit and view too.
	t.Run("item type override with create screen only", func(t *testing.T) {
		cs := &models.ConfigurationSet{
			ID: 3, CreateScreenID: &defaultCreate, EditScreenID: &defaultEdit, ViewScreenID: &defaultView,
			ItemTypeConfigs: []models.ItemTypeConfig{
				{ItemTypeID: itemTypeID, CreateScreenID: &typeCreate},
			},
		}
		resolved := &EffectiveConfig{}
		applyEffectiveConfig(resolved, cs, &itemTypeID)

		assertScreen(t, "create", resolved.CreateScreenID, typeCreate)
		assertScreen(t, "edit", resolved.EditScreenID, typeCreate)
		assertScreen(t, "view", resolved.ViewScreenID, typeCreate)
	})

	// Types without an override row use the config-set defaults, again via create.
	t.Run("no item type override falls back to config set screens", func(t *testing.T) {
		cs := &models.ConfigurationSet{
			ID: 3, CreateScreenID: &defaultCreate, EditScreenID: &defaultEdit,
		}
		resolved := &EffectiveConfig{}
		applyEffectiveConfig(resolved, cs, &itemTypeID)

		assertScreen(t, "create", resolved.CreateScreenID, defaultCreate)
		assertScreen(t, "edit", resolved.EditScreenID, defaultEdit)
		assertScreen(t, "view", resolved.ViewScreenID, defaultCreate)
	})

	// An override row that configures nothing defers to the config-set level.
	t.Run("empty item type override defers to config set", func(t *testing.T) {
		cs := &models.ConfigurationSet{
			ID: 3, CreateScreenID: &defaultCreate, EditScreenID: &defaultEdit,
			ItemTypeConfigs: []models.ItemTypeConfig{{ItemTypeID: itemTypeID}},
		}
		resolved := &EffectiveConfig{}
		applyEffectiveConfig(resolved, cs, &itemTypeID)

		assertScreen(t, "edit", resolved.EditScreenID, defaultEdit)
		assertScreen(t, "view", resolved.ViewScreenID, defaultCreate)
	})

	t.Run("condition and approval overrides", func(t *testing.T) {
		csCondition, csApproval := 31, 32
		typeApproval := 42
		cs := &models.ConfigurationSet{
			ID: 3, ConditionSetID: &csCondition, ApprovalSetID: &csApproval,
			ItemTypeConfigs: []models.ItemTypeConfig{
				{ItemTypeID: itemTypeID, ApprovalSetID: &typeApproval},
			},
		}
		resolved := &EffectiveConfig{}
		applyEffectiveConfig(resolved, cs, &itemTypeID)

		if resolved.ConditionSetID == nil || *resolved.ConditionSetID != csCondition {
			t.Fatalf("ConditionSetID = %v, want %d", resolved.ConditionSetID, csCondition)
		}
		if resolved.ApprovalSetID == nil || *resolved.ApprovalSetID != typeApproval {
			t.Fatalf("ApprovalSetID = %v, want %d", resolved.ApprovalSetID, typeApproval)
		}
	})
}

func assertScreen(t *testing.T, mode string, got *int, want int) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s screen = %v, want %d", mode, got, want)
	}
}

func TestResolveEffectiveFollowsWorkspaceDefaultFallback(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	ctx := context.Background()
	repo := NewConfigurationSetRepository(tdb.GetDatabase())

	db := tdb.GetDatabase()
	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		return testutils.InsertID(t, db, query, args...)
	}
	queryInt := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		return id
	}
	insertScreen := func(name string) int {
		return insertID(`INSERT INTO screens (name, description) VALUES (?, '')`, name)
	}

	// The seeded database ships one global default configuration set.
	defaultSet := queryInt(`SELECT id FROM configuration_sets WHERE is_default = true ORDER BY id LIMIT 1`)
	defaultEditScreenID := queryInt(
		`SELECT screen_id FROM configuration_set_screens WHERE configuration_set_id = ? AND context = 'edit'`, defaultSet)
	defaultCreateScreenID := queryInt(
		`SELECT screen_id FROM configuration_set_screens WHERE configuration_set_id = ? AND context = 'create'`, defaultSet)

	assignedCreate := insertScreen("assigned-create")
	assignedEdit := insertScreen("assigned-edit")
	assignedSet := insertID(
		`INSERT INTO configuration_sets (name, description, is_default) VALUES ('assigned set', '', false)`)
	assignScreen := func(configSetID, screenID int, context string) {
		insertID(
			`INSERT INTO configuration_set_screens (configuration_set_id, screen_id, context) VALUES (?, ?, ?)`,
			configSetID, screenID, context,
		)
	}
	assignScreen(assignedSet, assignedCreate, "create")
	assignScreen(assignedSet, assignedEdit, "edit")

	insertWorkspace := func(key string, isPersonal bool) int {
		return insertID(`INSERT INTO workspaces (key, name, is_personal) VALUES (?, ?, ?)`, key, key, isPersonal)
	}

	// A workspace with an explicit assignment resolves screens from that set.
	t.Run("assigned config set wins", func(t *testing.T) {
		workspaceID := insertWorkspace("ASSIGN", false)
		insertID(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, assignedSet)

		resolved, err := repo.ResolveEffective(ctx, workspaceID, nil)
		if err != nil {
			t.Fatalf("ResolveEffective: %v", err)
		}
		if resolved.IsPersonal {
			t.Fatal("IsPersonal = true, want false")
		}
		if resolved.ConfigSetID == nil || *resolved.ConfigSetID != assignedSet {
			t.Fatalf("ConfigSetID = %v, want %d", resolved.ConfigSetID, assignedSet)
		}
		assertScreen(t, "edit", resolved.EditScreenID, assignedEdit)
		assertScreen(t, "view", resolved.ViewScreenID, assignedCreate)
	})

	// Without an assignment the global default config set applies: its lean
	// create screen for create, and its full screen for edit and view.
	t.Run("unassigned workspace falls back to default config set", func(t *testing.T) {
		workspaceID := insertWorkspace("UNASSIGN", false)

		resolved, err := repo.ResolveEffective(ctx, workspaceID, nil)
		if err != nil {
			t.Fatalf("ResolveEffective: %v", err)
		}
		if resolved.ConfigSetID == nil || *resolved.ConfigSetID != defaultSet {
			t.Fatalf("ConfigSetID = %v, want %d", resolved.ConfigSetID, defaultSet)
		}
		assertScreen(t, "create", resolved.CreateScreenID, defaultCreateScreenID)
		assertScreen(t, "edit", resolved.EditScreenID, defaultEditScreenID)
		assertScreen(t, "view", resolved.ViewScreenID, defaultEditScreenID)
	})

	// Item-type overrides resolve against whichever set is effective.
	t.Run("item type override applies to default config set", func(t *testing.T) {
		workspaceID := insertWorkspace("TYPEDEF", false)
		typeCreate := insertScreen("type-create")
		itemTypeID := insertID(
			`INSERT INTO item_types (name, hierarchy_level) VALUES (?, 3)`, "Resolution Probe Type")
		insertID(
			`INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, create_screen_id) VALUES (?, ?, ?)`,
			defaultSet, itemTypeID, typeCreate,
		)

		resolved, err := repo.ResolveEffective(ctx, workspaceID, &itemTypeID)
		if err != nil {
			t.Fatalf("ResolveEffective: %v", err)
		}
		assertScreen(t, "create", resolved.CreateScreenID, typeCreate)
		assertScreen(t, "edit", resolved.EditScreenID, typeCreate)
		assertScreen(t, "view", resolved.ViewScreenID, typeCreate)
	})

	// Personal workspaces resolve nothing; callers use the fallback screen.
	t.Run("personal workspace resolves empty", func(t *testing.T) {
		workspaceID := insertWorkspace("PERSONAL", true)

		resolved, err := repo.ResolveEffective(ctx, workspaceID, nil)
		if err != nil {
			t.Fatalf("ResolveEffective: %v", err)
		}
		if !resolved.IsPersonal {
			t.Fatal("IsPersonal = false, want true")
		}
		if resolved.ConfigSetID != nil || resolved.EditScreenID != nil {
			t.Fatalf("personal workspace resolved config = %+v, want empty", resolved)
		}
	})

	// An unknown workspace returns nil rather than an empty resolution.
	t.Run("missing workspace returns nil", func(t *testing.T) {
		resolved, err := repo.ResolveEffective(ctx, 999999999, nil)
		if err != nil {
			t.Fatalf("ResolveEffective: %v", err)
		}
		if resolved != nil {
			t.Fatalf("ResolveEffective = %+v, want nil", resolved)
		}
	})

	// Config-set priorities ride along with the resolution, on top of the
	// seeded defaults.
	t.Run("priorities ride along", func(t *testing.T) {
		workspaceID := insertWorkspace("PRIO", false)
		priorityID := insertID(`INSERT INTO priorities (name) VALUES (?)`, "Resolution Probe Priority")
		insertID(`INSERT INTO configuration_set_priorities (configuration_set_id, priority_id) VALUES (?, ?)`, defaultSet, priorityID)

		resolved, err := repo.ResolveEffective(ctx, workspaceID, nil)
		if err != nil {
			t.Fatalf("ResolveEffective: %v", err)
		}
		found := false
		for _, priority := range resolved.Priorities {
			if priority.ID == priorityID {
				found = true
			}
		}
		if !found {
			t.Fatalf("Priorities = %+v, want membership of priority %d", resolved.Priorities, priorityID)
		}
	})
}
