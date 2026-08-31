package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/assetevents"
	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
)

type selectiveAssetPermissionChecker struct {
	allowed map[int]bool
}

func (c selectiveAssetPermissionChecker) HasAssetSetPermission(userID, _ int, _ string) (bool, error) {
	return c.allowed[userID], nil
}

func TestActionServicesRejectDisabledActions(t *testing.T) {
	t.Parallel()

	if err := (&ActionService{}).executeAction(&models.Action{ID: 4}, nil, nil); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("core action error = %v, want disabled", err)
	}
	if err := (&AssetActionService{}).executeAction(&models.AssetAction{ID: 5}, nil, nil); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("asset action error = %v, want disabled", err)
	}
}

func TestExecuteActionManuallyRejectsItemFromAnotherWorkspace(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "manual-action-workspace.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	actionWorkspaceID := insertID(`INSERT INTO workspaces (name, key) VALUES ('Action workspace', 'AWX') RETURNING id`)
	itemWorkspaceID := insertID(`INSERT INTO workspaces (name, key) VALUES ('Item workspace', 'IWX') RETURNING id`)
	createdItemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: itemWorkspaceID,
		Title:       "Foreign item",
	})
	if err != nil {
		t.Fatalf("insert fixture item: %v", err)
	}
	itemID := int(createdItemID)

	service := &ActionService{itemRepo: repository.NewItemRepository(db)}
	err = service.ExecuteActionManually(&models.Action{
		ID:          7,
		Name:        "Workspace-bound action",
		WorkspaceID: actionWorkspaceID,
		IsEnabled:   true,
	}, itemID, 42)
	if err == nil || !strings.Contains(err.Error(), "not action workspace") {
		t.Fatalf("ExecuteActionManually error = %v, want cross-workspace rejection", err)
	}
}

func TestSetFieldUsesValidatedItemUpdatePath(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "action-set-field.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	actorID := insertID(`INSERT INTO users (email, username, first_name, last_name) VALUES ('set-field@example.test', 'set-field', 'Set', 'Field') RETURNING id`)
	workspaceID := insertID(`INSERT INTO workspaces (name, key) VALUES ('Set field', 'SFX') RETURNING id`)
	createdItemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: workspaceID,
		Title:       "Cycle target",
	})
	if err != nil {
		t.Fatalf("insert fixture item: %v", err)
	}
	itemID := int(createdItemID)

	permissionService, err := NewPermissionService(db, PermissionCacheConfig{
		TTL:          time.Minute,
		MaxCacheSize: 8,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.cache.Close() })
	now := time.Now()
	if err := permissionService.storeUserPermissionCache(actorID, &models.UserPermissionCache{
		UserID: actorID,
		WorkspacePermissions: map[int]map[string]bool{
			workspaceID: {models.PermissionItemEdit: true},
		},
		CachedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store permission snapshot: %v", err)
	}

	service := &ActionService{
		db:                db,
		itemRepo:          repository.NewItemRepository(db),
		permissionService: permissionService,
		itemUpdate:        NewItemUpdateApplicationService(db, permissionService),
	}
	ctx := &models.ExecutionContext{
		Event: &models.ActionEvent{
			WorkspaceID: workspaceID,
			ItemID:      itemID,
		},
		EffectiveActorID: actorID,
	}
	err = service.executeSetFieldColumn(ctx, &models.StepResult{}, models.SetFieldNodeConfig{
		FieldName: "parent_id",
	}, fmt.Sprintf("%d", itemID))
	if err == nil || !strings.Contains(err.Error(), "own parent") {
		t.Fatalf("executeSetFieldColumn error = %v, want hierarchy-cycle rejection", err)
	}

	var parentID *int
	if err := db.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, itemID).Scan(&parentID); err != nil {
		t.Fatalf("load parent_id: %v", err)
	}
	if parentID != nil {
		t.Fatalf("parent_id = %v, want unchanged nil", *parentID)
	}
}

func TestSetFieldRejectsDedicatedWorkflowColumns(t *testing.T) {
	tests := []struct {
		field string
		want  string
	}{
		{field: "item_type_id", want: "item type change workflow"},
		{field: "status_id", want: "workflow transition"},
		{field: "custom_field_values", want: "custom_field target"},
		{field: "frac_index", want: "reorder workflow"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if _, err := parseActionSetFieldValue(tt.field, "1"); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseActionSetFieldValue(%q) error = %v, want %q", tt.field, err, tt.want)
			}
		})
	}

	// The public set_field dispatcher keeps legacy status configs working, but
	// only by handing them to executeSetStatusID. Reaching the permission gate
	// (instead of the raw-column rejection above) proves that routing.
	service := &ActionService{}
	err := service.executeSetField(&models.ActionNode{
		NodeConfig: `{"target":"column","field_name":"status_id","value":"7"}`,
	}, &models.ExecutionContext{
		Event:            &models.ActionEvent{WorkspaceID: 3, ItemID: 4},
		EffectiveActorID: 5,
		Variables:        map[string]interface{}{},
	}, &models.StepResult{})
	if err == nil || !strings.Contains(err.Error(), "permission service not configured") {
		t.Fatalf("status set_field dispatch error = %v, want workflow path permission gate", err)
	}
}

type emptyRunnerPoolLister struct{}

func (emptyRunnerPoolLister) ListCapabilitiesForWorkspace(int, string) ([]*models.ActionCapability, error) {
	return nil, nil
}

func TestBindingDispatchRevalidatesStoredRunnerPool(t *testing.T) {
	poolID := 91
	service := &BindingService{
		runs:  &RunService{},
		pools: emptyRunnerPoolLister{},
	}
	err := service.startRunForBinding(context.Background(), &models.WorkspaceAgentBinding{
		ID:           13,
		Lifecycle:    models.AgentLifecycleReady,
		TargetPoolID: &poolID,
	}, 5, 8, 21, &models.RunTrigger{Kind: "assignee"})
	if !errors.Is(err, ErrBindingInvalidPool) {
		t.Fatalf("startRunForBinding error = %v, want ErrBindingInvalidPool", err)
	}
}

func TestAssetActionMutationsFailClosedWithoutPermissionDependencies(t *testing.T) {
	t.Parallel()

	service := &AssetActionService{}
	ctx := &models.AssetActionExecutionContext{Event: &models.AssetActionEvent{
		SetID:       9,
		AssetID:     10,
		ActorUserID: 11,
	}}
	step := &models.StepResult{}
	if err := service.executeNode(&models.AssetActionNode{NodeType: models.AssetNodeSetField}, ctx, step); err == nil || !strings.Contains(err.Error(), "permission checker not configured") {
		t.Fatalf("set_field error = %v, want missing asset permission checker", err)
	}
	createItemConfig := `{"workspace_id":12,"item_type_id":1,"title":"test"}`
	if err := service.executeNode(&models.AssetActionNode{NodeType: models.AssetNodeCreateItem, NodeConfig: createItemConfig}, ctx, step); err == nil || !strings.Contains(err.Error(), "permission service not configured") {
		t.Fatalf("create_item error = %v, want missing workspace permission service", err)
	}
}

func TestAssetCreateItemActionEnforcesActorWorkspacePermission(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "asset-action-item-create.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	targetWorkspaceID := insertID(`INSERT INTO workspaces (name, key) VALUES ('Asset action target', 'AAT') RETURNING id`)
	itemTypeID := insertID(`INSERT INTO item_types (name) VALUES ('Asset action item') RETURNING id`)
	deniedActorID := insertID(`INSERT INTO users (email, username, first_name, last_name) VALUES ('asset-denied@example.test', 'asset-denied', 'Asset', 'Denied') RETURNING id`)
	allowedActorID := insertID(`INSERT INTO users (email, username, first_name, last_name) VALUES ('asset-allowed@example.test', 'asset-allowed', 'Asset', 'Allowed') RETURNING id`)
	setID := insertID(`INSERT INTO asset_management_sets (name, created_by) VALUES ('Asset action source', ?) RETURNING id`, allowedActorID)
	assetTypeID := insertID(`INSERT INTO asset_types (set_id, name) VALUES (?, 'Asset action source type') RETURNING id`, setID)
	assetID := insertID(`INSERT INTO assets (set_id, asset_type_id, title, created_by) VALUES (?, ?, 'Asset action source', ?) RETURNING id`, setID, assetTypeID, allowedActorID)
	actionID := insertID(`INSERT INTO asset_actions (set_id, name, trigger_type, created_by) VALUES (?, 'Create target item', 'asset_created', ?) RETURNING id`, setID, allowedActorID)

	permissionService, err := NewPermissionService(db, PermissionCacheConfig{
		TTL:          time.Minute,
		MaxCacheSize: 8,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permissionService.cache.Close() })
	now := time.Now()
	for actorID, allowed := range map[int]bool{
		deniedActorID:  false,
		allowedActorID: true,
	} {
		if err := permissionService.storeUserPermissionCache(actorID, &models.UserPermissionCache{
			UserID: actorID,
			WorkspacePermissions: map[int]map[string]bool{
				targetWorkspaceID: {models.PermissionItemCreate: allowed},
			},
			CachedAt:  now,
			ExpiresAt: now.Add(time.Minute),
		}); err != nil {
			t.Fatalf("store permission snapshot for actor %d: %v", actorID, err)
		}
	}

	actions := &milestoneActionEventCollector{}
	webhooks := &milestoneWebhookCollector{}
	coordinator := NewEventCoordinator(db)
	coordinator.SetActionService(actions)
	coordinator.SetWebhookDispatcher(webhooks)
	itemCreation := NewItemCreationService(db, permissionService)
	itemCreation.SetEmitter(coordinator)
	service := &AssetActionService{
		db:                db,
		repo:              repository.NewAssetActionRepository(db),
		chainStore:        NewExecutionChainStore(),
		permissionService: permissionService,
		itemCreation:      itemCreation,
	}
	makeAction := func(title string) *models.AssetAction {
		return &models.AssetAction{
			ID:          actionID,
			SetID:       setID,
			Name:        title,
			IsEnabled:   true,
			TriggerType: models.AssetTriggerAssetCreated,
			Nodes: []models.AssetActionNode{
				{ID: 1, NodeType: models.AssetNodeTrigger, NodeConfig: `{}`},
				{ID: 2, NodeType: models.AssetNodeCreateItem, NodeConfig: fmt.Sprintf(
					`{"workspace_id":%d,"item_type_id":%d,"title":%q}`,
					targetWorkspaceID, itemTypeID, title,
				)},
			},
			Edges: []models.AssetActionEdge{{SourceNodeID: 1, TargetNodeID: 2, EdgeType: "default"}},
		}
	}
	itemCount := func(title string) int {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE workspace_id = ? AND title = ?`, targetWorkspaceID, title).Scan(&count); err != nil {
			t.Fatalf("count items: %v", err)
		}
		return count
	}

	t.Run("automatic denial inserts no item", func(t *testing.T) {
		title := "automatic denied item"
		action := makeAction(title)
		event := &models.AssetActionEvent{
			EventType:   models.AssetTriggerAssetCreated,
			SetID:       action.SetID,
			AssetID:     assetID,
			ActorUserID: deniedActorID,
			OldValues:   map[string]interface{}{},
			NewValues:   map[string]interface{}{},
		}
		if err := service.executeAction(action, event, nil); err != nil {
			t.Fatalf("executeAction: %v", err)
		}
		if got := itemCount(title); got != 0 {
			t.Fatalf("created items = %d, want 0", got)
		}
		if len(actions.events) != 0 || len(webhooks.eventTypes) != 0 {
			t.Fatal("denied automatic creation emitted effects")
		}
	})

	t.Run("manual denial inserts no item", func(t *testing.T) {
		title := "manual denied item"
		if err := service.ExecuteActionManually(makeAction(title), assetID, deniedActorID); err != nil {
			t.Fatalf("ExecuteActionManually: %v", err)
		}
		if got := itemCount(title); got != 0 {
			t.Fatalf("created items = %d, want 0", got)
		}
		if len(actions.events) != 0 || len(webhooks.eventTypes) != 0 {
			t.Fatal("denied manual creation emitted effects")
		}
	})

	t.Run("automatic and manual allowed executions succeed", func(t *testing.T) {
		automaticTitle := "automatic allowed item"
		action := makeAction(automaticTitle)
		if err := service.executeAction(action, &models.AssetActionEvent{
			EventType:   models.AssetTriggerAssetCreated,
			SetID:       action.SetID,
			AssetID:     assetID,
			ActorUserID: allowedActorID,
			OldValues:   map[string]interface{}{},
			NewValues:   map[string]interface{}{},
		}, nil); err != nil {
			t.Fatalf("executeAction: %v", err)
		}
		if got := itemCount(automaticTitle); got != 1 {
			t.Fatalf("automatic created items = %d, want 1", got)
		}

		manualTitle := "manual allowed item"
		if err := service.ExecuteActionManually(makeAction(manualTitle), assetID, allowedActorID); err != nil {
			t.Fatalf("ExecuteActionManually: %v", err)
		}
		if got := itemCount(manualTitle); got != 1 {
			t.Fatalf("manual created items = %d, want 1", got)
		}
		if len(actions.events) != 2 {
			t.Fatalf("created-item action events = %d, want 2", len(actions.events))
		}
		for _, event := range actions.events {
			if event.EventType != models.ActionTriggerItemCreated ||
				event.WorkspaceID != targetWorkspaceID ||
				event.ItemID <= 0 ||
				!event.TriggeredByAction ||
				event.SourceApplication != "asset" {
				t.Fatalf("created-item action event = %+v", event)
			}
		}
		if len(webhooks.eventTypes) != 2 ||
			webhooks.eventTypes[0] != "item.created" ||
			webhooks.eventTypes[1] != "item.created" {
			t.Fatalf("created-item webhooks = %v, want two item.created events", webhooks.eventTypes)
		}
	})
}

func TestAssetActionNotificationsTargetOnlyAuthorizedConfiguredUsers(t *testing.T) {
	t.Parallel()

	manager := &recordingNotificationManager{}
	notifications := &NotificationService{notificationManager: manager}
	service := &AssetActionService{
		notificationService: notifications,
		assetPermChecker: selectiveAssetPermissionChecker{allowed: map[int]bool{
			21: true,
			22: false,
		}},
	}
	ctx := &models.AssetActionExecutionContext{Event: &models.AssetActionEvent{
		SetID:       7,
		AssetID:     8,
		ActorUserID: 20,
	}, Variables: map[string]interface{}{}}
	step := &models.StepResult{}
	node := &models.AssetActionNode{
		NodeType:   models.AssetNodeNotifyUser,
		NodeConfig: `{"recipients":["21","22"],"title":"Asset changed","message":"Review it","include_link":true}`,
	}
	if err := service.executeNotifyUser(node, ctx, step); err != nil {
		t.Fatalf("executeNotifyUser: %v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.batches) != 1 || len(manager.batches[0]) != 1 {
		t.Fatalf("notification batches = %+v, want one authorized recipient", manager.batches)
	}
	got := manager.batches[0][0]
	if got.UserID != 21 || got.ActionURL != "/assets/8" {
		t.Fatalf("notification = %+v, want user 21 and asset link", got)
	}
	if gotCount := step.Output["recipient_count"]; gotCount != 1 {
		t.Fatalf("recipient_count = %#v, want 1", gotCount)
	}
	gotIDs, ok := step.Output["recipient_ids"].([]int)
	if !ok || len(gotIDs) != 1 || gotIDs[0] != 21 {
		t.Fatalf("recipient_ids = %#v, want [21]", step.Output["recipient_ids"])
	}
}

func TestAssetActionNotificationsHandleMultipleSelfAndEmptyRecipients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		config        string
		actorID       int
		wantUserIDs   []int
		wantBatchSize int
	}{
		{
			name:          "multiple recipients",
			config:        `{"recipients":["21","22"],"message":"Review it"}`,
			actorID:       20,
			wantUserIDs:   []int{21, 22},
			wantBatchSize: 2,
		},
		{
			name:          "actor is suppressed",
			config:        `{"recipients":["20","21"],"message":"Review it"}`,
			actorID:       20,
			wantUserIDs:   []int{21},
			wantBatchSize: 1,
		},
		{
			name:          "empty recipients",
			config:        `{"recipient_type":"specific","recipients":[],"message":"Review it"}`,
			actorID:       20,
			wantUserIDs:   []int{},
			wantBatchSize: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &recordingNotificationManager{}
			service := &AssetActionService{
				notificationService: &NotificationService{notificationManager: manager},
				assetPermChecker: selectiveAssetPermissionChecker{allowed: map[int]bool{
					20: true,
					21: true,
					22: true,
				}},
			}
			ctx := &models.AssetActionExecutionContext{Event: &models.AssetActionEvent{
				SetID:       7,
				AssetID:     8,
				ActorUserID: tt.actorID,
			}, Variables: map[string]interface{}{}}
			step := &models.StepResult{}
			if err := service.executeNotifyUser(&models.AssetActionNode{
				NodeType:   models.AssetNodeNotifyUser,
				NodeConfig: tt.config,
			}, ctx, step); err != nil {
				t.Fatalf("executeNotifyUser: %v", err)
			}

			gotIDs, ok := step.Output["recipient_ids"].([]int)
			if !ok || len(gotIDs) != len(tt.wantUserIDs) {
				t.Fatalf("recipient_ids = %#v, want %v", step.Output["recipient_ids"], tt.wantUserIDs)
			}
			for i := range tt.wantUserIDs {
				if gotIDs[i] != tt.wantUserIDs[i] {
					t.Fatalf("recipient_ids = %v, want %v", gotIDs, tt.wantUserIDs)
				}
			}
			if got := step.Output["recipient_count"]; got != len(tt.wantUserIDs) {
				t.Fatalf("recipient_count = %#v, want %d", got, len(tt.wantUserIDs))
			}

			manager.mu.Lock()
			defer manager.mu.Unlock()
			gotBatchSize := 0
			if len(manager.batches) > 0 {
				gotBatchSize = len(manager.batches[0])
			}
			if gotBatchSize != tt.wantBatchSize {
				t.Fatalf("notification batch size = %d, want %d", gotBatchSize, tt.wantBatchSize)
			}
		})
	}
}

type failingNotificationManager struct {
	recordingNotificationManager
}

func (m *failingNotificationManager) AddNotifications([]models.Notification) ([]models.Notification, error) {
	return nil, errors.New("delivery unavailable")
}

func TestAssetActionNotificationDeliveryFailureFailsTheStep(t *testing.T) {
	t.Parallel()

	service := &AssetActionService{
		notificationService: &NotificationService{notificationManager: &failingNotificationManager{}},
		assetPermChecker: selectiveAssetPermissionChecker{allowed: map[int]bool{
			21: true,
		}},
	}
	ctx := &models.AssetActionExecutionContext{Event: &models.AssetActionEvent{
		SetID:       7,
		AssetID:     8,
		ActorUserID: 20,
	}, Variables: map[string]interface{}{}}
	step := &models.StepResult{}
	err := service.executeNotifyUser(&models.AssetActionNode{
		NodeType:   models.AssetNodeNotifyUser,
		NodeConfig: `{"recipients":["21"],"message":"Review it"}`,
	}, ctx, step)
	if err == nil || !strings.Contains(err.Error(), "delivery unavailable") {
		t.Fatalf("executeNotifyUser error = %v, want delivery failure", err)
	}
	if step.Output != nil {
		t.Fatalf("failed step output = %#v, want nil", step.Output)
	}
}

func TestNormalizeCredentialWorkspaceIDsRejectsInvalidIDs(t *testing.T) {
	t.Parallel()

	if _, err := normalizeCredentialWorkspaceIDs([]int{1, 0, 2}); err == nil {
		t.Fatal("workspace ID 0 was silently ignored")
	}
	got, err := normalizeCredentialWorkspaceIDs([]int{3, 3, 4})
	if err != nil {
		t.Fatalf("normalizeCredentialWorkspaceIDs: %v", err)
	}
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("normalized IDs = %v, want [3 4]", got)
	}
}

func TestBuildHTTPHeadersRejectsCaseInsensitiveDuplicates(t *testing.T) {
	t.Parallel()

	service := &ActionService{}
	_, err := service.buildHTTPHeadersWithCredentials(context.Background(), &models.HTTPClientConfig{
		DefaultHeaders: map[string]string{"X-Trace": "one", " x-trace ": "two"},
	}, nil, 1, 2)
	if err == nil {
		t.Fatal("case-insensitive duplicate default headers unexpectedly succeeded")
	}

	_, err = service.buildHTTPHeadersWithCredentials(context.Background(), &models.HTTPClientConfig{}, map[string]string{
		"X-Trace": "one",
		"x-trace": "two",
	}, 1, 2)
	if err == nil {
		t.Fatal("case-insensitive duplicate request headers unexpectedly succeeded")
	}
}

func TestBuildHTTPHeadersAllowsCallerToOverrideNonSecretDefault(t *testing.T) {
	t.Parallel()

	service := &ActionService{}
	got, err := service.buildHTTPHeadersWithCredentials(context.Background(), &models.HTTPClientConfig{
		DefaultHeaders: map[string]string{"Content-Type": "application/json"},
	}, map[string]string{"content-type": "text/plain"}, 1, 2)
	if err != nil {
		t.Fatalf("buildHTTPHeadersWithCredentials: %v", err)
	}
	if got["Content-Type"] != "text/plain" || len(got) != 1 {
		t.Fatalf("merged headers = %#v, want one overridden Content-Type", got)
	}
}

func TestRedirectStripsCredentialHeadersOnAnyCrossOriginHop(t *testing.T) {
	t.Parallel()

	client := newSSRFSafeClient(time.Second, []string{"https://**"})
	original := httptest.NewRequest(http.MethodGet, "https://api.example.test/start", nil)
	original.Header.Set("X-API-Key", "secret")
	original.Header.Set("X-Signature", "signature")
	original.Header.Set("X-Trace-ID", "safe")

	crossOrigin := httptest.NewRequest(http.MethodGet, "https://redirect.example.test/next", nil)
	crossOrigin.Header = original.Header.Clone()
	if err := client.CheckRedirect(crossOrigin, []*http.Request{original}); err != nil {
		t.Fatalf("CheckRedirect cross-origin: %v", err)
	}
	if crossOrigin.Header.Get("X-API-Key") != "" || crossOrigin.Header.Get("X-Signature") != "" {
		t.Fatalf("sensitive headers survived cross-origin redirect: %#v", crossOrigin.Header)
	}
	if crossOrigin.Header.Get("X-Trace-ID") != "safe" {
		t.Fatalf("non-sensitive header was stripped: %#v", crossOrigin.Header)
	}

	backToOrigin := httptest.NewRequest(http.MethodGet, "https://api.example.test/final", nil)
	backToOrigin.Header = original.Header.Clone()
	if err := client.CheckRedirect(backToOrigin, []*http.Request{original, crossOrigin}); err != nil {
		t.Fatalf("CheckRedirect return-to-origin: %v", err)
	}
	if backToOrigin.Header.Get("X-API-Key") != "" {
		t.Fatal("credential header was restored after a cross-origin redirect hop")
	}
}

func TestRedirectPreservesCredentialHeadersWithinOrigin(t *testing.T) {
	t.Parallel()

	client := newSSRFSafeClient(time.Second, []string{"https://api.example.test/**"})
	original := httptest.NewRequest(http.MethodGet, "https://api.example.test/start", nil)
	redirect := httptest.NewRequest(http.MethodGet, "https://api.example.test/next", nil)
	redirect.Header.Set("X-API-Key", "secret")
	if err := client.CheckRedirect(redirect, []*http.Request{original}); err != nil {
		t.Fatalf("CheckRedirect: %v", err)
	}
	if redirect.Header.Get("X-API-Key") != "secret" {
		t.Fatal("same-origin redirect stripped credential header")
	}
}

func TestActionHTTPDiagnosticURLRedactionDropsQueryFragmentAndUserInfo(t *testing.T) {
	t.Parallel()

	got := redactHTTPURLForDiagnostics("https://user:password@example.test/hook?token=plaintext&customer=private#fragment")
	if strings.Contains(got, "password") || strings.Contains(got, "plaintext") || strings.Contains(got, "private") || strings.Contains(got, "fragment") {
		t.Fatalf("diagnostic URL leaked confidential values: %q", got)
	}
	if !strings.Contains(got, "example.test/hook") {
		t.Fatalf("diagnostic URL lost useful endpoint context: %q", got)
	}
}

func TestActionHTTPResponsePreviewRedactsBeforeTruncating(t *testing.T) {
	t.Parallel()

	secret := strings.Repeat("s", 700)
	preview := truncateString(RedactString(`{"token":"`+secret+`"}`), 500)
	if strings.Contains(preview, secret[:20]) {
		t.Fatal("response preview leaked a long JSON credential")
	}
}

func TestCreateAssetActionEnforcesSetSchemaAndDefaultStatus(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "create-asset-action.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v", err)
		}
		return id
	}
	actorID := insertID(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('action-asset@example.test', 'action-asset', 'Action', 'Asset') RETURNING id
	`)
	setID := insertID(`INSERT INTO asset_management_sets (name, created_by) VALUES ('Action assets', ?) RETURNING id`, actorID)
	otherSetID := insertID(`INSERT INTO asset_management_sets (name, created_by) VALUES ('Other assets', ?) RETURNING id`, actorID)
	typeID := insertID(`INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id`, setID)
	otherTypeID := insertID(`INSERT INTO asset_types (set_id, name) VALUES (?, 'Other server') RETURNING id`, otherSetID)
	defaultStatusID := insertID(`INSERT INTO asset_statuses (set_id, name, is_default) VALUES (?, 'Available', true) RETURNING id`, setID)
	otherStatusID := insertID(`INSERT INTO asset_statuses (set_id, name, is_default) VALUES (?, 'Other', true) RETURNING id`, otherSetID)
	otherCategoryID := insertID(`INSERT INTO asset_categories (set_id, name) VALUES (?, 'Other category') RETURNING id`, otherSetID)
	fieldID := insertID(`INSERT INTO custom_field_definitions (name, field_type) VALUES ('Serial number', 'text') RETURNING id`)
	if _, err := db.ExecWrite(`
		INSERT INTO asset_type_fields (asset_type_id, custom_field_id, is_required)
		VALUES (?, ?, true)
	`, typeID, fieldID); err != nil {
		t.Fatalf("link required field: %v", err)
	}

	assetService := NewAssetService(db, repository.NewAssetRepository(db))
	service := &ActionService{
		itemRepo: repository.NewItemRepository(db),
	}
	service.SetAssetNodeServices(assetService, selectiveAssetPermissionChecker{allowed: map[int]bool{actorID: true}})
	ctx := &models.ExecutionContext{
		Event:            &models.ActionEvent{CascadeDepth: 2},
		EffectiveActorID: actorID,
		Variables:        map[string]interface{}{},
		ChainID:          "asset-create-chain",
	}
	execute := func(config string) error {
		t.Helper()
		return service.executeNode(&models.ActionNode{NodeType: models.ActionNodeCreateAsset, NodeConfig: config}, ctx, &models.StepResult{})
	}

	if err := execute(fmt.Sprintf(`{"asset_set_id":%d,"asset_type_id":%d,"title":"Server"}`, setID, otherTypeID)); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-set asset type error = %v, want ownership rejection", err)
	}
	if err := execute(fmt.Sprintf(`{"asset_set_id":%d,"asset_type_id":%d,"category_id":%d,"title":"Server"}`, setID, typeID, otherCategoryID)); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-set category error = %v, want ownership rejection", err)
	}
	if err := execute(fmt.Sprintf(`{"asset_set_id":%d,"asset_type_id":%d,"status_id":%d,"title":"Server"}`, setID, typeID, otherStatusID)); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-set status error = %v, want ownership rejection", err)
	}
	if err := execute(fmt.Sprintf(`{"asset_set_id":%d,"asset_type_id":%d,"title":"Server"}`, setID, typeID)); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing required custom field error = %v, want schema rejection", err)
	}

	validConfig := fmt.Sprintf(`{
		"asset_set_id":%d,
		"asset_type_id":%d,
		"title":"Server",
		"field_mappings":[{"source_type":"literal","source_value":"rack-7","target_field_id":"%d"}]
	}`, setID, typeID, fieldID)
	if err := execute(validConfig); err != nil {
		t.Fatalf("valid create_asset action: %v", err)
	}

	var gotStatusID, gotCreatorID int
	var gotFields string
	if err := db.QueryRow(`SELECT status_id, created_by, custom_field_values FROM assets`).Scan(&gotStatusID, &gotCreatorID, &gotFields); err != nil {
		t.Fatalf("load created asset: %v", err)
	}
	if gotStatusID != defaultStatusID {
		t.Fatalf("status_id = %d, want default %d", gotStatusID, defaultStatusID)
	}
	if gotCreatorID != actorID {
		t.Fatalf("created_by = %d, want effective actor %d", gotCreatorID, actorID)
	}
	if !strings.Contains(gotFields, "rack-7") {
		t.Fatalf("custom_field_values = %q, want mapped required field", gotFields)
	}
	var auditUserID int
	if err := db.QueryRow(`
		SELECT user_id FROM audit_logs
		WHERE action_type = ? AND resource_type = ?
		ORDER BY id DESC LIMIT 1
	`, logger.ActionAssetCreate, logger.ResourceAsset).Scan(&auditUserID); err != nil {
		t.Fatalf("load asset create audit: %v", err)
	}
	if auditUserID != actorID {
		t.Fatalf("audit user_id = %d, want effective actor %d", auditUserID, actorID)
	}
	var eventActorRef, correlationID, sourceRef, payloadJSON string
	if err := db.QueryRow(`
		SELECT actor_ref, correlation_id, source_ref, payload
		FROM domain_events WHERE event_type = ? ORDER BY id DESC LIMIT 1
	`, assetevents.Created).Scan(&eventActorRef, &correlationID, &sourceRef, &payloadJSON); err != nil {
		t.Fatalf("load asset-created fact: %v", err)
	}
	var payload assetevents.CreatedV1
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode asset-created fact: %v", err)
	}
	if eventActorRef != fmt.Sprint(actorID) || payload.Asset.ID <= 0 || payload.Automation == nil || !payload.Automation.TriggeredByAction {
		t.Fatalf("asset-created fact = actor:%s payload:%#v, want action event attributed to actor %d", eventActorRef, payload, actorID)
	}
	if correlationID != "asset-create-chain" || sourceRef != "workspace" || payload.Automation.CascadeDepth != 3 || payload.Automation.SourceApplication != "workspace" {
		t.Fatalf("asset-created cascade context = correlation:%q source:%q automation:%#v", correlationID, sourceRef, payload.Automation)
	}
}
