//go:build test

package services

import (
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// commentPrivacyFixture wires an ActionService with the comment service and a
// permission snapshot that allows item.comment, so add_comment nodes execute.
func commentPrivacyFixture(t *testing.T) (*ActionService, *notificationEmissionTestEnv) {
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
			env.workspaceID: {
				models.PermissionItemEdit:    true,
				models.PermissionItemComment: true,
			},
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
		commentService:    NewCommentService(db),
	}
	return service, env
}

func commentPrivacyContext(env *notificationEmissionTestEnv, triggerCommentPrivate bool) *models.ExecutionContext {
	return &models.ExecutionContext{
		Event: &models.ActionEvent{
			EventType:   models.ActionTriggerCommentCreated,
			WorkspaceID: env.workspaceID,
			ItemID:      env.itemID,
			ActorUserID: env.userID,
		},
		Item:                    &models.Item{ID: env.itemID, WorkspaceID: env.workspaceID},
		EffectiveActorID:        env.userID,
		Variables:               map[string]any{},
		TriggerCommentIsPrivate: triggerCommentPrivate,
	}
}

func lastCommentIsPrivate(t *testing.T, env *notificationEmissionTestEnv) bool {
	t.Helper()
	var isPrivate bool
	if err := env.db.QueryRow(`SELECT is_private FROM comments WHERE item_id = ? ORDER BY id DESC LIMIT 1`, env.itemID).Scan(&isPrivate); err != nil {
		t.Fatalf("load comment: %v", err)
	}
	return isPrivate
}

func TestAddCommentDowngradesPublicCommentForPrivateTrigger(t *testing.T) {
	service, env := commentPrivacyFixture(t)
	node := &models.ActionNode{NodeType: models.ActionNodeAddComment, NodeConfig: `{"content": "customer-visible reply", "is_private": false}`}
	step := &models.StepResult{}

	if err := service.executeAddComment(node, commentPrivacyContext(env, true), step); err != nil {
		t.Fatalf("executeAddComment: %v", err)
	}

	if !lastCommentIsPrivate(t, env) {
		t.Fatal("a public add_comment triggered by a private comment produced a customer-visible comment")
	}
	if step.Output["is_private"] != true || step.Output["privacy_downgraded"] != true {
		t.Fatalf("step output = %v, want is_private=true and privacy_downgraded=true", step.Output)
	}
}

func TestAddCommentKeepsPublicCommentForPublicTrigger(t *testing.T) {
	service, env := commentPrivacyFixture(t)
	node := &models.ActionNode{NodeType: models.ActionNodeAddComment, NodeConfig: `{"content": "customer-visible reply", "is_private": false}`}
	step := &models.StepResult{}

	if err := service.executeAddComment(node, commentPrivacyContext(env, false), step); err != nil {
		t.Fatalf("executeAddComment: %v", err)
	}

	if lastCommentIsPrivate(t, env) {
		t.Fatal("a public add_comment from a public trigger was forced private")
	}
	if step.Output["privacy_downgraded"] == true {
		t.Fatalf("step output = %v, want no privacy_downgraded flag", step.Output)
	}
}

func TestActionContextFromExecutionPropagatesCommentPrivacy(t *testing.T) {
	ctx := &models.ExecutionContext{
		ChainID:                 "privacy-chain",
		TriggerCommentIsPrivate: true,
		Event:                   &models.ActionEvent{CascadeDepth: 1},
	}

	actionCtx := actionContextFromExecution(ctx)
	if actionCtx == nil {
		t.Fatal("actionContextFromExecution returned nil")
	}
	if !actionCtx.TriggerCommentIsPrivate {
		t.Fatal("actionContextFromExecution dropped TriggerCommentIsPrivate")
	}

	event := &models.ActionEvent{}
	applyActionContext(event, actionCtx)
	if !event.TriggerCommentIsPrivate {
		t.Fatal("applyActionContext did not copy TriggerCommentIsPrivate onto the event")
	}
}

// TestSetFieldCascadeCarriesCommentPrivacy proves a customer-visible node in a
// downstream action can still see the originating comment's privacy after the
// item.updated cascade.
func TestSetFieldCascadeCarriesCommentPrivacy(t *testing.T) {
	service, env, ctx := actionItemMutationFixture(t)
	ctx.TriggerCommentIsPrivate = true

	actions := &milestoneActionEventCollector{}
	coordinator := NewEventCoordinator(env.db)
	coordinator.SetActionService(actions)
	service.itemUpdate.SetEmitter(coordinator)

	fieldID := actionItemInsertID(t, env.db, `
		INSERT INTO custom_field_definitions (name, field_type)
		VALUES ('Privacy text field', 'text')
	`)

	if err := service.executeSetFieldCustom(ctx, &models.StepResult{}, models.SetFieldNodeConfig{
		Target:        "custom_field",
		CustomFieldID: fieldID,
	}, "cascade value"); err != nil {
		t.Fatalf("executeSetFieldCustom: %v", err)
	}

	if len(actions.events) != 1 {
		t.Fatalf("cascade events = %d, want 1", len(actions.events))
	}
	if !actions.events[0].TriggerCommentIsPrivate {
		t.Fatal("item.updated cascade dropped TriggerCommentIsPrivate")
	}
}
