package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestItemDetailSummaryRequiresAuthentication(t *testing.T) {
	handler := NewItemDetailHandler(&ItemHandler{}, nil, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/items/42/detail-summary", nil)
	request.SetPathValue("id", "42")
	recorder := httptest.NewRecorder()

	handler.Get(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestKeyItemDetailSummaryRequiresAuthenticationBeforeResolution(t *testing.T) {
	handler := NewItemDetailHandler(&ItemHandler{}, nil, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/WI/items/42/detail-summary", nil)
	request.SetPathValue("key", "WI")
	request.SetPathValue("number", "42")
	recorder := httptest.NewRecorder()

	handler.GetByKeyAndNumber(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestResolveItemDetailScreenIDMatchesFrontendFallbackChain(t *testing.T) {
	defaultCreate, defaultEdit, typeCreate, typeView := 10, 11, 20, 21
	itemTypeID := 7
	config := &models.ConfigurationSet{
		CreateScreenID: &defaultCreate,
		EditScreenID:   &defaultEdit,
		ItemTypeConfigs: []models.ItemTypeConfig{
			{ItemTypeID: itemTypeID, CreateScreenID: &typeCreate, ViewScreenID: &typeView},
		},
	}

	if got := resolveItemDetailScreenID(config, &itemTypeID, "view", 1); got != typeView {
		t.Fatalf("view screen = %d, want item-type view %d", got, typeView)
	}
	if got := resolveItemDetailScreenID(config, &itemTypeID, "edit", 1); got != typeCreate {
		t.Fatalf("edit screen = %d, want item-type create fallback %d", got, typeCreate)
	}
	otherType := 8
	if got := resolveItemDetailScreenID(config, &otherType, "edit", 1); got != defaultEdit {
		t.Fatalf("default edit screen = %d, want %d", got, defaultEdit)
	}
	if got := resolveItemDetailScreenID(nil, &itemTypeID, "view", 1); got != 1 {
		t.Fatalf("hard fallback screen = %d, want 1", got)
	}
}

func TestLoadManualActionsIncludesUnrestrictedActionForWorkspaceEditor(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	const editorUserID = 2
	if _, err := tdb.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (?, 'editor@example.com', 'editor', 'Workspace', 'Editor', 'hash', true)
	`, editorUserID); err != nil {
		t.Fatalf("create editor: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO user_workspace_roles (user_id, workspace_id, role_id)
		SELECT ?, ?, id FROM workspace_roles WHERE name = ?
	`, editorUserID, data.WorkspaceID, models.RoleEditor); err != nil {
		t.Fatalf("assign Editor role: %v", err)
	}

	actionRepo := repository.NewActionRepository(tdb.GetDatabase())
	actionID, err := actionRepo.Create(&models.Action{
		WorkspaceID: data.WorkspaceID,
		Name:        "Escalate",
		IsEnabled:   true,
		TriggerType: models.ActionTriggerManual,
	})
	if err != nil {
		t.Fatalf("create manual action: %v", err)
	}

	handler := &ItemDetailHandler{actions: createActionsHandler(t, tdb)}
	actions, err := handler.loadManualActions(editorUserID, data.WorkspaceID)
	if err != nil {
		t.Fatalf("load manual actions: %v", err)
	}
	if len(actions) != 1 || actions[0].ID != actionID {
		t.Fatalf("manual actions = %#v, want unrestricted action %d", actions, actionID)
	}
}

func TestLoadManualActionsHonorsRoleInheritedFromActiveGroup(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	const userID = 2
	if _, err := tdb.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, password_hash, is_active)
		VALUES (?, 'group-member@example.com', 'group-member', 'Group', 'Member', 'hash', true)
	`, userID); err != nil {
		t.Fatalf("create group member: %v", err)
	}
	var groupID int
	if err := tdb.QueryRow(`
		INSERT INTO groups (name, is_active) VALUES ('Manual action operators', true)
		RETURNING id
	`).Scan(&groupID); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		t.Fatalf("add group member: %v", err)
	}
	var viewerRoleID int
	if err := tdb.QueryRow(`SELECT id FROM workspace_roles WHERE name = ?`, models.RoleViewer).Scan(&viewerRoleID); err != nil {
		t.Fatalf("find Viewer role: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO group_workspace_roles (group_id, workspace_id, role_id)
		VALUES (?, ?, ?)
	`, groupID, data.WorkspaceID, viewerRoleID); err != nil {
		t.Fatalf("assign group workspace role: %v", err)
	}

	actionRepo := repository.NewActionRepository(tdb.GetDatabase())
	actionID, err := actionRepo.Create(&models.Action{
		WorkspaceID:    data.WorkspaceID,
		Name:           "Request review",
		IsEnabled:      true,
		TriggerType:    models.ActionTriggerManual,
		AllowedRoleIDs: []int{viewerRoleID},
	})
	if err != nil {
		t.Fatalf("create restricted manual action: %v", err)
	}

	handler := &ItemDetailHandler{actions: createActionsHandler(t, tdb)}
	actions, err := handler.loadManualActions(userID, data.WorkspaceID)
	if err != nil {
		t.Fatalf("load manual actions: %v", err)
	}
	if len(actions) != 1 || actions[0].ID != actionID {
		t.Fatalf("manual actions = %#v, want group-allowed action %d", actions, actionID)
	}
}
