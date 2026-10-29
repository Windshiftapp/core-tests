//go:build test

package services

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type cannedResponseItemFixture struct {
	db          database.Database
	canned      *CannedResponseService
	comments    *CommentService
	labels      *repository.LabelRepository
	workspaceID int
	itemID      int
	actorID     int
}

func newCannedResponseItemFixture(t *testing.T) *cannedResponseItemFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID int
	if err := db.QueryRow(`INSERT INTO users (email, username, first_name, last_name) VALUES ('agent2@test.local', 'agent2', 'Ann', 'Agent') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Node WS', 'NODEWS') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, creator_id) VALUES (?, 5, 'Crashed portal', 'details', ?, ?) RETURNING id`,
		workspaceID, testutils.NextTestFracIndex(), userID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	canned := NewCannedResponseService(
		repository.NewCannedResponseRepository(db),
		repository.NewUserRepository(db),
		repository.NewItemRepository(db),
	)
	comments := NewCommentService(db)
	return &cannedResponseItemFixture{
		db: db, canned: canned, comments: comments,
		labels:      repository.NewLabelRepository(db),
		workspaceID: workspaceID, itemID: itemID, actorID: userID,
	}
}

func (f *cannedResponseItemFixture) mustCreateResponse(t *testing.T, name, body string, isPrivate bool) *models.CannedResponse {
	t.Helper()
	cr, err := f.canned.Create(f.workspaceID, CannedResponseInput{Name: name, Body: body, IsPrivate: isPrivate}, AuditActor{UserID: f.actorID})
	if err != nil {
		t.Fatalf("create canned response %q: %v", name, err)
	}
	return cr
}

func (f *cannedResponseItemFixture) itemCountComments(t *testing.T, itemID int) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE item_id = ?`, itemID).Scan(&count); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return count
}

func executionContextFor(itemID, workspaceID, actorID int) *models.ExecutionContext {
	return &models.ExecutionContext{
		Item:             &models.Item{ID: itemID, WorkspaceID: workspaceID},
		EffectiveActorID: actorID,
	}
}

func TestInsertCannedResponseExecutorPostsPublicComment(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	response := f.mustCreateResponse(t, "greeting", "Hello from support", false)
	executor := NewInsertCannedResponseExecutor(f.canned, f.comments, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeInsertCannedResponse, NodeConfig: fmt.Sprintf(`{"canned_response_id": %d}`, response.ID)}
	step := &models.StepResult{}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, f.actorID), step); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.itemCountComments(t, f.itemID) != 1 {
		t.Fatal("public insert did not create a comment")
	}
	var isPrivate bool
	if err := f.db.QueryRow(`SELECT is_private FROM comments WHERE item_id = ?`, f.itemID).Scan(&isPrivate); err != nil {
		t.Fatalf("load comment: %v", err)
	}
	if isPrivate {
		t.Fatal("public canned response created a private comment")
	}
	if step.Output["comment_id"] == nil {
		t.Fatalf("step output missing comment_id: %v", step.Output)
	}

	// Usage counter incremented exactly once.
	updated, err := f.canned.Get(f.workspaceID, response.ID)
	if err != nil || updated.UsedCount != 1 {
		t.Fatalf("used_count=%d (err %v), want 1", updated.UsedCount, err)
	}
}

func TestInsertCannedResponseExecutorPostsPrivateSnippetPrivately(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	response := f.mustCreateResponse(t, "internal", "internal diagnostics", true)
	executor := NewInsertCannedResponseExecutor(f.canned, f.comments, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeInsertCannedResponse, NodeConfig: fmt.Sprintf(`{"canned_response_id": %d}`, response.ID)}
	step := &models.StepResult{}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, f.actorID), step); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var isPrivate bool
	if err := f.db.QueryRow(`SELECT is_private FROM comments WHERE item_id = ?`, f.itemID).Scan(&isPrivate); err != nil {
		t.Fatalf("load comment: %v", err)
	}
	if !isPrivate {
		t.Fatal("private canned response created a public comment; internal snippets must stay internal")
	}
	if step.Output["is_private"] != true {
		t.Fatalf("step output is_private = %v, want true", step.Output["is_private"])
	}
}

func TestInsertCannedResponseExecutorDowngradesPublicSnippetForPrivateTrigger(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	response := f.mustCreateResponse(t, "greeting", "Hello from support", false)
	executor := NewInsertCannedResponseExecutor(f.canned, f.comments, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeInsertCannedResponse, NodeConfig: fmt.Sprintf(`{"canned_response_id": %d}`, response.ID)}
	ctx := executionContextFor(f.itemID, f.workspaceID, f.actorID)
	ctx.TriggerCommentIsPrivate = true
	step := &models.StepResult{}
	if err := executor.Execute(node, ctx, step); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var isPrivate bool
	if err := f.db.QueryRow(`SELECT is_private FROM comments WHERE item_id = ?`, f.itemID).Scan(&isPrivate); err != nil {
		t.Fatalf("load comment: %v", err)
	}
	if !isPrivate {
		t.Fatal("public snippet from a private trigger produced a customer-visible comment")
	}
	if step.Output["is_private"] != true || step.Output["privacy_downgraded"] != true {
		t.Fatalf("output = %v, want is_private=true and privacy_downgraded=true", step.Output)
	}
}

// TestInsertCannedResponseExecutorRejectsRevokedActor pins finding 3: a
// revoked override actor must not post a canned response even though the
// action's CRUD-time permission check passed when it was created.
func TestInsertCannedResponseExecutorRejectsRevokedActor(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	response := f.mustCreateResponse(t, "greeting", "Hello from support", false)
	api := &stubNodeAPI{authorizeErr: fmt.Errorf("user %d not authorized (item.comment) on workspace %d", f.actorID, f.workspaceID)}
	executor := NewInsertCannedResponseExecutor(f.canned, f.comments, api)

	node := &models.ActionNode{NodeType: models.ActionNodeInsertCannedResponse, NodeConfig: fmt.Sprintf(`{"canned_response_id": %d}`, response.ID)}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, f.actorID), &models.StepResult{}); err == nil {
		t.Fatal("revoked actor accepted; want authorization error")
	}
	if f.itemCountComments(t, f.itemID) != 0 {
		t.Fatal("revoked actor posted a canned response")
	}
}

// A system-triggered execution (no override actor) has no user whose rights
// can be revoked, so it must keep running even though the authorization stub
// would reject an identified actor.
func TestInsertCannedResponseExecutorRunsWithoutActor(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	response := f.mustCreateResponse(t, "system", "Hello from automation", false)
	api := &stubNodeAPI{authorizeErr: fmt.Errorf("workspace mutation requires an identified actor")}
	executor := NewInsertCannedResponseExecutor(f.canned, f.comments, api)

	node := &models.ActionNode{NodeType: models.ActionNodeInsertCannedResponse, NodeConfig: fmt.Sprintf(`{"canned_response_id": %d}`, response.ID)}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, 0), &models.StepResult{}); err != nil {
		t.Fatalf("system-triggered insert failed: %v", err)
	}
	if f.itemCountComments(t, f.itemID) != 1 {
		t.Fatal("system-triggered insert did not post the canned response")
	}
}

func TestInsertCannedResponseExecutorRejectsArchivedResponse(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	response := f.mustCreateResponse(t, "gone", "body", false)
	archived := false
	if _, err := f.canned.Update(f.workspaceID, response.ID, CannedResponseUpdate{IsActive: &archived}, AuditActor{UserID: f.actorID}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	executor := NewInsertCannedResponseExecutor(f.canned, f.comments, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeInsertCannedResponse, NodeConfig: fmt.Sprintf(`{"canned_response_id": %d}`, response.ID)}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, f.actorID), &models.StepResult{}); err == nil {
		t.Fatal("archived response accepted; want error")
	}
	if f.itemCountComments(t, f.itemID) != 0 {
		t.Fatal("archived insert created a comment")
	}
}

type fakeCustomerNotifier struct {
	delivered bool
	skip      string
	err       error
	lastItem  int
	lastActor int
	subject   string
	message   string
}

func (n *fakeCustomerNotifier) SendAutomationNotice(itemID, actorID int, subject, message string) (bool, string, error) {
	n.lastItem, n.lastActor, n.subject, n.message = itemID, actorID, subject, message
	if n.err != nil {
		return false, "", n.err
	}
	return n.delivered, n.skip, nil
}

func TestNotifyCustomerExecutorDeliversRenderedMessage(t *testing.T) {
	notifier := &fakeCustomerNotifier{delivered: true}
	executor := NewNotifyCustomerExecutor(notifier, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{"subject": "Update on {{ticket.title}}", "message": "Hi there"}`}
	step := &models.StepResult{}
	ctx := executionContextFor(7, 1, 3)
	if err := executor.Execute(node, ctx, step); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if notifier.lastItem != 7 || notifier.lastActor != 3 {
		t.Fatalf("notifier got item=%d actor=%d, want 7/3", notifier.lastItem, notifier.lastActor)
	}
	if notifier.message != "Hi there" {
		t.Fatalf("message = %q, want passthrough", notifier.message)
	}
	if step.Output["delivered"] != true {
		t.Fatalf("output = %v, want delivered=true", step.Output)
	}
}

func TestNotifyCustomerExecutorSkipsWhenTriggerCommentIsPrivate(t *testing.T) {
	notifier := &fakeCustomerNotifier{delivered: true}
	executor := NewNotifyCustomerExecutor(notifier, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{"message": "hello"}`}
	ctx := executionContextFor(7, 1, 3)
	ctx.TriggerCommentIsPrivate = true
	step := &models.StepResult{}
	if err := executor.Execute(node, ctx, step); err != nil {
		t.Fatalf("privacy skip must not fail the action, got: %v", err)
	}
	if notifier.lastItem != 0 {
		t.Fatal("notifier was called for a private/internal trigger comment")
	}
	if step.Output["delivered"] != false || step.Output["skip_reason"] != "trigger_comment_private" {
		t.Fatalf("output = %v, want delivered=false skip_reason=trigger_comment_private", step.Output)
	}
}

// TestNotifyCustomerExecutorRejectsRevokedActor pins finding 3 for the email
// node: a revoked actor must not email the customer.
func TestNotifyCustomerExecutorRejectsRevokedActor(t *testing.T) {
	notifier := &fakeCustomerNotifier{delivered: true}
	api := &stubNodeAPI{authorizeErr: fmt.Errorf("user 3 not authorized (item.comment) on workspace 1")}
	executor := NewNotifyCustomerExecutor(notifier, api)

	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{"message": "hello"}`}
	if err := executor.Execute(node, executionContextFor(7, 1, 3), &models.StepResult{}); err == nil {
		t.Fatal("revoked actor accepted; want authorization error")
	}
	if notifier.lastItem != 0 {
		t.Fatal("revoked actor sent a customer email")
	}
}

// A system-triggered execution (no override actor) must still email the
// customer even though the authorization stub would reject an identified actor.
func TestNotifyCustomerExecutorRunsWithoutActor(t *testing.T) {
	notifier := &fakeCustomerNotifier{delivered: true}
	api := &stubNodeAPI{authorizeErr: fmt.Errorf("workspace mutation requires an identified actor")}
	executor := NewNotifyCustomerExecutor(notifier, api)

	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{"message": "hello"}`}
	if err := executor.Execute(node, executionContextFor(7, 1, 0), &models.StepResult{}); err != nil {
		t.Fatalf("system-triggered notify failed: %v", err)
	}
	if notifier.lastItem != 7 {
		t.Fatal("system-triggered notify did not deliver")
	}
}

func TestNotifyCustomerExecutorSkipsWithoutFailure(t *testing.T) {
	notifier := &fakeCustomerNotifier{delivered: false, skip: "no_customer"}
	executor := NewNotifyCustomerExecutor(notifier, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{"message": "hello"}`}
	step := &models.StepResult{}
	if err := executor.Execute(node, executionContextFor(7, 1, 3), step); err != nil {
		t.Fatalf("skip must not fail the action, got: %v", err)
	}
	if step.Output["skip_reason"] != "no_customer" {
		t.Fatalf("output = %v, want skip_reason=no_customer", step.Output)
	}
}

func TestNotifyCustomerExecutorFailsOnTransportError(t *testing.T) {
	notifier := &fakeCustomerNotifier{err: fmt.Errorf("smtp down")}
	executor := NewNotifyCustomerExecutor(notifier, &stubNodeAPI{})

	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{"message": "hello"}`}
	if err := executor.Execute(node, executionContextFor(7, 1, 3), &models.StepResult{}); err == nil {
		t.Fatal("transport error swallowed; want action failure")
	}
}

func TestNotifyCustomerExecutorRequiresMessage(t *testing.T) {
	executor := NewNotifyCustomerExecutor(&fakeCustomerNotifier{}, &stubNodeAPI{})
	node := &models.ActionNode{NodeType: models.ActionNodeNotifyCustomer, NodeConfig: `{}`}
	if err := executor.Execute(node, executionContextFor(7, 1, 3), &models.StepResult{}); err == nil {
		t.Fatal("empty message accepted; want error")
	}
}

func TestAdjustLabelsExecutorAddsRemovesAndReportsMissing(t *testing.T) {
	f := newCannedResponseItemFixture(t)

	var addA, addB, removeMe int
	for _, name := range []string{"vip", "escalated", "temporary"} {
		var id int
		if err := f.db.QueryRow(`INSERT INTO labels (name) VALUES (?) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("insert label %s: %v", name, err)
		}
		switch name {
		case "vip":
			addA = id
		case "escalated":
			addB = id
		case "temporary":
			removeMe = id
		}
	}
	missingID := 99999 // a label that does not exist
	if err := f.labels.AddItemLabel(f.itemID, removeMe); err != nil {
		t.Fatalf("seed label: %v", err)
	}

	executor := NewAdjustLabelsExecutor(f.labels, &stubNodeAPI{})
	node := &models.ActionNode{NodeType: models.ActionNodeAdjustLabels, NodeConfig: fmt.Sprintf(
		`{"add_label_ids": [%d, %d, %d], "remove_label_ids": [%d, %d]}`, addA, addB, missingID, removeMe, missingID)}
	step := &models.StepResult{}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, f.actorID), step); err != nil {
		t.Fatalf("execute: %v", err)
	}

	output := step.Output
	if got := len(output["added"].([]int)); got != 2 {
		t.Fatalf("added = %v, want 2 labels", output["added"])
	}
	if got := output["removed"].([]int); len(got) != 1 || got[0] != removeMe {
		t.Fatalf("removed = %v, want [%d]", got, removeMe)
	}
	if got := output["missing"].([]int); len(got) != 2 || got[0] != missingID || got[1] != missingID {
		t.Fatalf("missing = %v, want the label twice (once per reference)", got)
	}

	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM item_labels WHERE item_id = ? AND label_id IN (?, ?)`, f.itemID, addA, addB).Scan(&count); err != nil {
		t.Fatalf("count labels: %v", err)
	}
	if count != 2 {
		t.Fatalf("applied labels = %d, want 2", count)
	}
	var removedCount int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM item_labels WHERE item_id = ? AND label_id = ?`, f.itemID, removeMe).Scan(&removedCount); err != nil {
		t.Fatalf("count removed label: %v", err)
	}
	if removedCount != 0 {
		t.Fatal("remove_label_ids left the label attached")
	}
}

func TestAdjustLabelsExecutorRequiresAChange(t *testing.T) {
	f := newCannedResponseItemFixture(t)
	executor := NewAdjustLabelsExecutor(f.labels, &stubNodeAPI{})
	node := &models.ActionNode{NodeType: models.ActionNodeAdjustLabels, NodeConfig: `{}`}
	if err := executor.Execute(node, executionContextFor(f.itemID, f.workspaceID, f.actorID), &models.StepResult{}); err == nil {
		t.Fatal("empty adjust_labels config accepted; want error")
	}
}

func TestMatchesTriggerCommentCreatedFromCustomerFilter(t *testing.T) {
	as := &ActionService{}
	customer := true

	customerAction := &models.Action{TriggerType: models.ActionTriggerCommentCreated, TriggerConfig: mustJSON(t, models.ActionTriggerConfig{FromCustomer: &customer})}
	anyAction := &models.Action{TriggerType: models.ActionTriggerCommentCreated}

	customerEvent := &models.ActionEvent{EventType: models.ActionTriggerCommentCreated, NewValues: map[string]any{"comment_from_customer": true}}
	agentEvent := &models.ActionEvent{EventType: models.ActionTriggerCommentCreated, NewValues: map[string]any{"comment_from_customer": false}}

	if !as.matchesTrigger(customerAction, customerEvent) {
		t.Fatal("customer action should match customer comment")
	}
	if as.matchesTrigger(customerAction, agentEvent) {
		t.Fatal("customer action must not match agent comment")
	}
	if !as.matchesTrigger(anyAction, agentEvent) {
		t.Fatal("unfiltered action should match agent comment")
	}
}

func TestMatchesTriggerTargetedActionGate(t *testing.T) {
	as := &ActionService{}
	inactive := &models.Action{ID: 3, TriggerType: models.ActionTriggerItemInactive, TriggerConfig: `{"inactive_hours": 24}`}

	targeted := &models.ActionEvent{EventType: models.ActionTriggerItemInactive, NewValues: map[string]any{"action_id": 3}}
	other := &models.ActionEvent{EventType: models.ActionTriggerItemInactive, NewValues: map[string]any{"action_id": 9}}

	if !as.matchesTrigger(inactive, targeted) {
		t.Fatal("targeted event must match its action")
	}
	if as.matchesTrigger(inactive, other) {
		t.Fatal("targeted event must not match a different action")
	}
}

func TestMatchesTriggerItemInactiveRequiresThreshold(t *testing.T) {
	as := &ActionService{}
	zero := &models.Action{TriggerType: models.ActionTriggerItemInactive, TriggerConfig: `{"inactive_hours": 0}`}
	event := &models.ActionEvent{EventType: models.ActionTriggerItemInactive}
	if as.matchesTrigger(zero, event) {
		t.Fatal("item_inactive without a positive inactive_hours must never match")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

var _ = time.Now
