//go:build test

package services

import (
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func actionItemInsertID(t *testing.T, db database.Database, query string, args ...interface{}) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("fixture insert: %v", err)
	}
	return id
}

func actionItemMutationFixture(t *testing.T) (*ActionService, *notificationEmissionTestEnv, *models.ExecutionContext) {
	t.Helper()

	db := createNotificationEmissionTestDB(t)
	env := setupNotificationEmissionTestEnv(t, db)
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
	if err := permissionService.storeUserPermissionCache(env.userID, &models.UserPermissionCache{
		UserID: env.userID,
		WorkspacePermissions: map[int]map[string]bool{
			env.workspaceID: {models.PermissionItemEdit: true},
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
			WorkspaceID:  env.workspaceID,
			ItemID:       env.itemID,
			ActorUserID:  env.userID,
			CascadeDepth: 1,
		},
		EffectiveActorID: env.userID,
		ChainID:          "action-item-update-test",
		Variables:        map[string]interface{}{},
	}
	return service, env, ctx
}

func TestActionCustomFieldUpdateRecordsCanonicalItemHistory(t *testing.T) {
	service, env, ctx := actionItemMutationFixture(t)
	actions := &milestoneActionEventCollector{}
	webhooks := &milestoneWebhookCollector{}
	coordinator := NewEventCoordinator(env.db)
	coordinator.SetActionService(actions)
	coordinator.SetWebhookDispatcher(webhooks)
	service.itemUpdate.SetEmitter(coordinator)
	fieldID := actionItemInsertID(t, env.db, `
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Action text field', 'text')
	`)

	if err := service.executeSetFieldCustom(ctx, &models.StepResult{}, models.SetFieldNodeConfig{
		Target:        "custom_field",
		CustomFieldID: fieldID,
	}, "set through action"); err != nil {
		t.Fatalf("executeSetFieldCustom: %v", err)
	}

	var count, actorID int
	var oldValue, newValue string
	if err := env.db.QueryRow(`
		SELECT COUNT(*), COALESCE(MIN(user_id), -1), COALESCE(MIN(old_value), ''), COALESCE(MIN(new_value), '')
		FROM item_history
		WHERE item_id = ? AND field_name = ?
	`, env.itemID, fmt.Sprintf("cf_%d", fieldID)).Scan(&count, &actorID, &oldValue, &newValue); err != nil {
		t.Fatalf("read custom-field history: %v", err)
	}
	if count != 1 || actorID != env.userID || oldValue != "" || newValue != "set through action" {
		t.Fatalf("custom-field history = count %d actor %d old %q new %q", count, actorID, oldValue, newValue)
	}
	if len(actions.events) != 1 {
		t.Fatalf("action events = %d, want 1", len(actions.events))
	}
	event := actions.events[0]
	fieldName := fmt.Sprintf("custom_field_%d", fieldID)
	if event.EventType != models.ActionTriggerItemUpdated ||
		event.NewValues[fieldName] != "set through action" ||
		!event.TriggeredByAction ||
		event.ExecutionChainID != ctx.ChainID ||
		event.CascadeDepth != 2 ||
		event.SourceApplication != "workspace" {
		t.Fatalf("custom-field action event = %+v", event)
	}
	if len(webhooks.eventTypes) != 1 || webhooks.eventTypes[0] != "item.updated" {
		t.Fatalf("custom-field webhooks = %v, want [item.updated]", webhooks.eventTypes)
	}
}

func TestActionCustomFieldNumberStringIsNormalized(t *testing.T) {
	service, env, ctx := actionItemMutationFixture(t)
	fieldID := actionItemInsertID(t, env.db, `
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Action number field', 'number')
	`)
	stepResult := &models.StepResult{}

	if err := service.executeSetFieldCustom(ctx, stepResult, models.SetFieldNodeConfig{
		Target:        "custom_field",
		CustomFieldID: fieldID,
	}, "12.5"); err != nil {
		t.Fatalf("executeSetFieldCustom: %v", err)
	}

	fieldKey := fmt.Sprintf("custom_field_%d", fieldID)
	if got := stepResult.Output["new_value"]; got != float64(12.5) {
		t.Fatalf("action output %s = %#v (%T), want 12.5 as a number", fieldKey, got, got)
	}
	stored, err := service.itemRepo.GetItemCustomFieldValue(env.itemID, fieldID)
	if err != nil {
		t.Fatalf("GetItemCustomFieldValue: %v", err)
	}
	if stored != float64(12.5) {
		t.Fatalf("stored %s = %#v (%T), want 12.5 as a number", fieldKey, stored, stored)
	}
}

func TestActionCustomFieldBooleanStringIsNormalized(t *testing.T) {
	tests := []struct {
		name      string
		fieldType string
		value     string
		want      any
	}{
		{name: "boolean true", fieldType: "boolean", value: "true", want: true},
		{name: "checkbox false", fieldType: "checkbox", value: "false", want: false},
		{name: "blank clears", fieldType: "boolean", value: "  ", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, env, ctx := actionItemMutationFixture(t)
			fieldID := actionItemInsertID(t, env.db, `
				INSERT INTO custom_field_definitions (name, field_type)
				VALUES ('Action boolean field', ?)
			`, tt.fieldType)
			stepResult := &models.StepResult{}

			if err := service.executeSetFieldCustom(ctx, stepResult, models.SetFieldNodeConfig{
				Target:        "custom_field",
				CustomFieldID: fieldID,
			}, tt.value); err != nil {
				t.Fatalf("executeSetFieldCustom: %v", err)
			}

			if got := stepResult.Output["new_value"]; got != tt.want {
				t.Fatalf("action output = %#v (%T), want %#v", got, got, tt.want)
			}
			stored, err := service.itemRepo.GetItemCustomFieldValue(env.itemID, fieldID)
			if err != nil {
				t.Fatalf("GetItemCustomFieldValue: %v", err)
			}
			if stored != tt.want {
				t.Fatalf("stored value = %#v (%T), want %#v", stored, stored, tt.want)
			}
		})
	}
}

func TestActionCustomFieldUpdateRejectsUnknownFieldWithoutPersistingOrphan(t *testing.T) {
	service, env, ctx := actionItemMutationFixture(t)
	const unknownFieldID = 999999

	err := service.executeSetFieldCustom(ctx, &models.StepResult{}, models.SetFieldNodeConfig{
		Target:        "custom_field",
		CustomFieldID: unknownFieldID,
	}, "orphaned value")
	if err == nil || err.Error() != "set_field: custom field 999999 does not exist" {
		t.Fatalf("executeSetFieldCustom error = %v, want unknown-field rejection", err)
	}

	stored, err := service.itemRepo.GetItemCustomFieldValue(env.itemID, unknownFieldID)
	if err != nil {
		t.Fatalf("GetItemCustomFieldValue: %v", err)
	}
	if stored != nil {
		t.Fatalf("unknown custom field value was persisted: %#v", stored)
	}
}

func TestActionRoundRobinAssignmentRecordsCanonicalItemHistory(t *testing.T) {
	service, env, ctx := actionItemMutationFixture(t)
	actions := &milestoneActionEventCollector{}
	webhooks := &milestoneWebhookCollector{}
	coordinator := NewEventCoordinator(env.db)
	coordinator.SetActionService(actions)
	coordinator.SetWebhookDispatcher(webhooks)
	service.itemUpdate.SetEmitter(coordinator)
	assigneeID := actionItemInsertID(t, env.db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('round-robin@example.test', 'round-robin', 'Round', 'Robin', true)
	`)
	teamID := actionItemInsertID(t, env.db, `
		INSERT INTO teams (name, is_active, created_by)
		VALUES ('Action assignment team', true, ?)
	`, env.userID)
	if _, err := env.db.Exec(`
		INSERT INTO team_members (team_id, user_id, role, added_by)
		VALUES (?, ?, 'member', ?)
	`, teamID, assigneeID, env.userID); err != nil {
		t.Fatalf("add team member: %v", err)
	}
	service.teamService = NewTeamService(
		env.db,
		repository.NewTeamRepository(env.db),
		repository.NewLeaveRepository(env.db),
	)

	node := &models.ActionNode{
		ID:         901,
		NodeConfig: fmt.Sprintf(`{"team_id":%d}`, teamID),
	}
	if err := service.executeRoundRobinAssign(node, ctx, &models.StepResult{}); err != nil {
		t.Fatalf("executeRoundRobinAssign: %v", err)
	}

	var count, actorID int
	var oldValue, newValue string
	if err := env.db.QueryRow(`
		SELECT COUNT(*), COALESCE(MIN(user_id), -1), COALESCE(MIN(old_value), ''), COALESCE(MIN(new_value), '')
		FROM item_history
		WHERE item_id = ? AND field_name = 'assignee_id'
	`, env.itemID).Scan(&count, &actorID, &oldValue, &newValue); err != nil {
		t.Fatalf("read assignee history: %v", err)
	}
	if count != 1 || actorID != env.userID || oldValue != "" || newValue != fmt.Sprint(assigneeID) {
		t.Fatalf("assignee history = count %d actor %d old %q new %q", count, actorID, oldValue, newValue)
	}
	if len(actions.events) != 1 {
		t.Fatalf("action events = %d, want 1", len(actions.events))
	}
	event := actions.events[0]
	if event.NewValues["assignee_id"] != fmt.Sprint(assigneeID) ||
		!event.TriggeredByAction ||
		event.ExecutionChainID != ctx.ChainID ||
		event.CascadeDepth != 2 {
		t.Fatalf("round-robin action event = %+v", event)
	}
	wantWebhooks := []string{"item.assigned", "item.updated"}
	if len(webhooks.eventTypes) != len(wantWebhooks) ||
		webhooks.eventTypes[0] != wantWebhooks[0] ||
		webhooks.eventTypes[1] != wantWebhooks[1] {
		t.Fatalf("round-robin webhooks = %v, want %v", webhooks.eventTypes, wantWebhooks)
	}
}
