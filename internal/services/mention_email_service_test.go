//go:build test

package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// fakeMentionEmailSender captures dedicated mention email sends.
type fakeMentionEmailSender struct {
	configured bool
	sendErr    error
	sent       []fakeMentionSend
}

type fakeMentionSend struct {
	toEmail      string
	templateName string
	data         any
}

func (f *fakeMentionEmailSender) IsSMTPConfigured() bool { return f.configured }

func (f *fakeMentionEmailSender) SendTransactionalContext(_ context.Context, toEmail, templateName string, data any) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, fakeMentionSend{toEmail: toEmail, templateName: templateName, data: data})
	return nil
}

// fakeMentionMarker records which notifications were suppressed.
type fakeMentionMarker struct {
	sent []int
	err  error
}

func (m *fakeMentionMarker) MarkNotificationsSent(ids []int) error {
	m.sent = append(m.sent, ids...)
	return m.err
}

type mentionEmailTestEnv struct {
	db             database.Database
	workspaceID    int
	itemID         int
	actorID        int
	recipientID    int
	mentionID      int
	notificationID int
	perm           *PermissionService
	authorizer     *NotificationAuthorizer
}

func setupMentionEmailTestEnv(t *testing.T, suffix string) *mentionEmailTestEnv {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	data := tdb.SeedTestData(t)

	actorID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES (?, ?, 'Actor', 'User', TRUE)
	`, "mention-actor-"+suffix+"@example.com", "mention-actor-"+suffix)

	recipientID := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES (?, ?, 'Recipient', 'User', TRUE)
	`, "mention-recipient-"+suffix+"@example.com", "mention-recipient-"+suffix)

	createdItemID, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: data.WorkspaceID,
		Title:       "Mention email item " + suffix,
		Description: "Item description",
		StatusID:    &data.StatusID,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID := int(createdItemID)

	mentionID := testutils.InsertID(t, db, `
		INSERT INTO mentions (source_type, source_id, mentioned_user_id, item_id, workspace_id, created_by, mentioned_user_display_name, created_at)
		VALUES ('comment', 999, ?, ?, ?, ?, 'Recipient User', CURRENT_TIMESTAMP)
	`, recipientID, itemID, data.WorkspaceID, actorID)

	notificationID := testutils.InsertID(t, db, `
		INSERT INTO notifications (user_id, title, message, type, timestamp, read, authorization_scope, workspace_id, item_id)
		VALUES (?, 'You were mentioned', 'Actor mentioned you', 'mention', CURRENT_TIMESTAMP, FALSE, 'workspace', ?, ?)
	`, recipientID, data.WorkspaceID, itemID)

	perm, err := NewPermissionService(db, PermissionCacheConfig{TTL: time.Minute, MaxCacheSize: 8, BatchSize: 10})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = perm.cache.Close() })

	return &mentionEmailTestEnv{
		db:             db,
		workspaceID:    data.WorkspaceID,
		itemID:         itemID,
		actorID:        actorID,
		recipientID:    recipientID,
		mentionID:      mentionID,
		notificationID: notificationID,
		perm:           perm,
		authorizer:     NewNotificationAuthorizer(db, perm, nil),
	}
}

// grantView seeds an item.view permission snapshot for the recipient.
func (e *mentionEmailTestEnv) grantView(t *testing.T) {
	t.Helper()
	now := time.Now()
	if err := e.perm.storeUserPermissionCache(e.recipientID, &models.UserPermissionCache{
		UserID: e.recipientID,
		WorkspacePermissions: map[int]map[string]bool{
			e.workspaceID: {models.PermissionItemView: true},
		},
		CachedAt:  now,
		ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store permission snapshot: %v", err)
	}
}

// revokeView overwrites the recipient's snapshot without item.view.
func (e *mentionEmailTestEnv) revokeView(t *testing.T) {
	t.Helper()
	now := time.Now()
	if err := e.perm.storeUserPermissionCache(e.recipientID, &models.UserPermissionCache{
		UserID:               e.recipientID,
		WorkspacePermissions: map[int]map[string]bool{e.workspaceID: {}},
		CachedAt:             now,
		ExpiresAt:            now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store revoked permission snapshot: %v", err)
	}
}

func (e *mentionEmailTestEnv) enqueue(t *testing.T, service *MentionEmailService) {
	t.Helper()
	if err := service.EnqueueMentionEmail(MentionEmailParams{
		MentionID:      e.mentionID,
		NotificationID: e.notificationID,
		UserID:         e.recipientID,
		WorkspaceID:    e.workspaceID,
		ItemID:         e.itemID,
		SourceType:     "comment",
		Content:        "Hello @Recipient, please review the token refresh path.",
		ActorName:      "Actor User",
		ItemKey:        "TEST-1",
		ItemTitle:      "Mention email item",
		ActionURL:      "/workspaces/1/items/1",
	}); err != nil {
		t.Fatalf("enqueue mention email: %v", err)
	}
}

func TestMentionEmailServiceDeliversContentAndSuppressesDigest(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "deliver")
	env.grantView(t)

	sender := &fakeMentionEmailSender{configured: true}
	marker := &fakeMentionMarker{}
	service := NewMentionEmailService(env.db, sender, marker, env.authorizer)
	service.SetBaseURL("https://app.example.com")

	env.enqueue(t, service)

	var rows int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM mention_email_outbox WHERE mention_id = ?`, env.mentionID).Scan(&rows); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("outbox rows = %d, want 1", rows)
	}

	delivered, err := service.ProcessPendingMentions(context.Background(), 50)
	if err != nil {
		t.Fatalf("ProcessPendingMentions: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent emails = %d, want 1", len(sender.sent))
	}
	got := sender.sent[0]
	if got.templateName != "mention" {
		t.Fatalf("template = %q, want mention", got.templateName)
	}
	if got.toEmail != "mention-recipient-deliver@example.com" {
		t.Fatalf("recipient = %q", got.toEmail)
	}
	data, ok := got.data.(MentionEmailData)
	if !ok {
		t.Fatalf("data type = %T, want MentionEmailData", got.data)
	}
	if data.Content != "Hello @Recipient, please review the token refresh path." {
		t.Fatalf("content = %q", data.Content)
	}
	if data.SourceLabel != "a comment" {
		t.Fatalf("source label = %q, want a comment", data.SourceLabel)
	}
	if data.ActionURL != "https://app.example.com/workspaces/1/items/1" {
		t.Fatalf("action URL = %q", data.ActionURL)
	}

	var deliveredAt any
	if err := env.db.QueryRow(`SELECT delivered_at FROM mention_email_outbox WHERE mention_id = ?`, env.mentionID).Scan(&deliveredAt); err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	if deliveredAt == nil {
		t.Fatal("delivered_at is NULL after a successful send")
	}
	if len(marker.sent) != 1 || marker.sent[0] != env.notificationID {
		t.Fatalf("suppressed notifications = %v, want [%d]", marker.sent, env.notificationID)
	}

	// A second pass must not re-send the same mention.
	again, err := service.ProcessPendingMentions(context.Background(), 50)
	if err != nil {
		t.Fatalf("second ProcessPendingMentions: %v", err)
	}
	if again != 0 || len(sender.sent) != 1 {
		t.Fatalf("second pass delivered=%d sent=%d, want 0/1", again, len(sender.sent))
	}
}

func TestMentionEmailServiceDiscardsWhenRecipientLosesAccess(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "revoked")
	env.grantView(t)

	sender := &fakeMentionEmailSender{configured: true}
	marker := &fakeMentionMarker{}
	service := NewMentionEmailService(env.db, sender, marker, env.authorizer)
	env.enqueue(t, service)

	// Permission is revoked after the mention was queued.
	env.revokeView(t)

	delivered, err := service.ProcessPendingMentions(context.Background(), 50)
	if err != nil {
		t.Fatalf("ProcessPendingMentions: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("delivered = %d, want 0", delivered)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent emails = %d, want 0 (permission revoked)", len(sender.sent))
	}
	if len(marker.sent) != 0 {
		t.Fatalf("suppressed notifications = %v, want none", marker.sent)
	}

	var discardedAt, deliveredAt any
	if err := env.db.QueryRow(`SELECT discarded_at, delivered_at FROM mention_email_outbox WHERE mention_id = ?`, env.mentionID).Scan(&discardedAt, &deliveredAt); err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	if discardedAt == nil {
		t.Fatal("discarded_at is NULL after a permission denial")
	}
	if deliveredAt != nil {
		t.Fatal("delivered_at set despite permission denial")
	}
}

func TestMentionEmailServiceSchedulesRetryOnSendFailure(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "retry")
	env.grantView(t)

	sender := &fakeMentionEmailSender{configured: true, sendErr: errors.New("smtp unavailable")}
	service := NewMentionEmailService(env.db, sender, &fakeMentionMarker{}, env.authorizer)
	env.enqueue(t, service)

	delivered, err := service.ProcessPendingMentions(context.Background(), 50)
	if err == nil {
		t.Fatal("expected an error from a failed send")
	}
	if delivered != 0 {
		t.Fatalf("delivered = %d, want 0", delivered)
	}

	var attemptCount int
	var deliveredAt any
	var nextAttempt time.Time
	if err := env.db.QueryRow(`
		SELECT attempt_count, delivered_at, next_attempt_at
		FROM mention_email_outbox WHERE mention_id = ?
	`, env.mentionID).Scan(&attemptCount, &deliveredAt, &nextAttempt); err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	if attemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1", attemptCount)
	}
	if deliveredAt != nil {
		t.Fatal("delivered_at set after a failed send")
	}
	if !nextAttempt.After(time.Now()) {
		t.Fatalf("next_attempt_at = %v, want a future retry", nextAttempt)
	}
}

func TestMentionEmailServiceSkipsNotificationAlreadyHandled(t *testing.T) {
	cases := []struct {
		name string
		mark string
	}{
		{name: "read in app", mark: `UPDATE notifications SET read = TRUE WHERE id = ?`},
		{name: "already emailed", mark: `UPDATE notifications SET sent_at = CURRENT_TIMESTAMP WHERE id = ?`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupMentionEmailTestEnv(t, "handled-"+strings.ReplaceAll(tc.name, " ", "-"))
			env.grantView(t)

			sender := &fakeMentionEmailSender{configured: true}
			service := NewMentionEmailService(env.db, sender, &fakeMentionMarker{}, env.authorizer)
			env.enqueue(t, service)

			if _, err := env.db.Exec(tc.mark, env.notificationID); err != nil {
				t.Fatalf("mark notification handled: %v", err)
			}

			delivered, err := service.ProcessPendingMentions(context.Background(), 50)
			if err != nil {
				t.Fatalf("ProcessPendingMentions: %v", err)
			}
			if delivered != 0 || len(sender.sent) != 0 {
				t.Fatalf("delivered=%d sent=%d, want 0/0 when the notification is already handled", delivered, len(sender.sent))
			}
			var discardedAt any
			if err := env.db.QueryRow(`SELECT discarded_at FROM mention_email_outbox WHERE mention_id = ?`, env.mentionID).Scan(&discardedAt); err != nil {
				t.Fatalf("read outbox row: %v", err)
			}
			if discardedAt == nil {
				t.Fatal("discarded_at is NULL after the notification was already handled")
			}
		})
	}
}

func TestMentionEmailServiceEnqueueIsIdempotent(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "idempotent")
	env.grantView(t)

	service := NewMentionEmailService(env.db, &fakeMentionEmailSender{configured: true}, &fakeMentionMarker{}, env.authorizer)
	env.enqueue(t, service)
	env.enqueue(t, service)

	var rows int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM mention_email_outbox WHERE mention_id = ?`, env.mentionID).Scan(&rows); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("outbox rows = %d, want 1", rows)
	}
}

// mentionRecordingNotifier captures mention email enqueue requests.
type mentionRecordingNotifier struct {
	calls []MentionEmailParams
}

func (r *mentionRecordingNotifier) EnqueueMentionEmail(params MentionEmailParams) error {
	r.calls = append(r.calls, params)
	return nil
}

func (r *mentionRecordingNotifier) RefreshPendingMentionContent(string, int, string) error { return nil }

// mentionRecordingNotificationManager is a minimal NotificationManager for the
// mention-service integration tests; it assigns ids and records sends.
type mentionRecordingNotificationManager struct {
	nextID int
	stored []models.Notification
	sent   []int
}

func (m *mentionRecordingNotificationManager) AddNotification(n models.Notification) (models.Notification, error) {
	stored, err := m.AddNotifications([]models.Notification{n})
	if err != nil {
		return models.Notification{}, err
	}
	return stored[0], nil
}

func (m *mentionRecordingNotificationManager) AddNotifications(notifications []models.Notification) ([]models.Notification, error) {
	out := make([]models.Notification, len(notifications))
	for i := range notifications {
		m.nextID++
		notifications[i].ID = m.nextID
		out[i] = notifications[i]
		m.stored = append(m.stored, notifications[i])
	}
	return out, nil
}

func (m *mentionRecordingNotificationManager) AddNotificationsContext(_ context.Context, notifications []models.Notification) ([]models.Notification, error) {
	return m.AddNotifications(notifications)
}

func (m *mentionRecordingNotificationManager) DeleteUserNotifications(int) error { return nil }

func (m *mentionRecordingNotificationManager) MarkNotificationsSent(ids []int) error {
	m.sent = append(m.sent, ids...)
	return nil
}

func (m *mentionRecordingNotificationManager) MarkNotificationsSendFailed([]int) error { return nil }

func (m *mentionRecordingNotificationManager) RollbackNotificationsSent([]int) error { return nil }

func TestMentionServiceQueuesEmailForPermittedMention(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "queued")
	env.grantView(t)

	manager := &mentionRecordingNotificationManager{}
	notifications := NewNotificationService(env.db, manager, env.perm, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = notifications.Close() })

	notifier := &mentionRecordingNotifier{}
	service := NewMentionService(env.db, notifications, env.perm)
	service.SetMentionEmailNotifier(notifier)

	content := "Hey @mention-recipient-queued, can you review this?"
	err := service.ProcessMentions(ProcessMentionsParams{
		SourceType:  "comment",
		SourceID:    4242,
		Content:     content,
		ItemID:      env.itemID,
		WorkspaceID: env.workspaceID,
		ActorUserID: env.actorID,
	})
	if err != nil {
		t.Fatalf("ProcessMentions: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("enqueued mention emails = %d, want 1", len(notifier.calls))
	}
	call := notifier.calls[0]
	if call.UserID != env.recipientID {
		t.Fatalf("enqueued user = %d, want %d", call.UserID, env.recipientID)
	}
	if call.Content != content {
		t.Fatalf("enqueued content = %q, want %q", call.Content, content)
	}
	if call.MentionID <= 0 || call.NotificationID <= 0 {
		t.Fatalf("enqueued ids = mention:%d notification:%d, want positive", call.MentionID, call.NotificationID)
	}
	if call.ActorName == "" || call.ItemKey == "" {
		t.Fatalf("enqueued context = actor:%q item:%q, want populated", call.ActorName, call.ItemKey)
	}
}

func TestMentionServiceDoesNotQueueEmailForUnpermittedMention(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "denied")
	// A fresh user may inherit a default viewer grant; seed an explicit
	// snapshot without item.view so this exercises a genuinely denied user.
	env.revokeView(t)

	manager := &mentionRecordingNotificationManager{}
	notifications := NewNotificationService(env.db, manager, env.perm, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = notifications.Close() })

	notifier := &mentionRecordingNotifier{}
	service := NewMentionService(env.db, notifications, env.perm)
	service.SetMentionEmailNotifier(notifier)

	err := service.ProcessMentions(ProcessMentionsParams{
		SourceType:  "comment",
		SourceID:    4243,
		Content:     "Hey @mention-recipient-denied, can you review this?",
		ItemID:      env.itemID,
		WorkspaceID: env.workspaceID,
		ActorUserID: env.actorID,
	})
	if err != nil {
		t.Fatalf("ProcessMentions: %v", err)
	}

	if len(notifier.calls) != 0 {
		t.Fatalf("enqueued mention emails = %d, want 0 for an unpermitted recipient", len(notifier.calls))
	}
	var mentions int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM mentions WHERE source_id = 4243 AND mentioned_user_id = ?`, env.recipientID).Scan(&mentions); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	if mentions != 0 {
		t.Fatalf("mentions = %d, want 0 for an unpermitted recipient", mentions)
	}
}

func TestMentionEmailOutboxCascadeOnMentionRemoval(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "cascade")
	env.grantView(t)

	service := NewMentionEmailService(env.db, &fakeMentionEmailSender{configured: true}, &fakeMentionMarker{}, env.authorizer)
	env.enqueue(t, service)

	mentionService := NewMentionService(env.db, nil, nil)
	if err := mentionService.DeleteMentionsForSource("comment", 999); err != nil {
		t.Fatalf("DeleteMentionsForSource: %v", err)
	}

	var rows int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM mention_email_outbox WHERE mention_id = ?`, env.mentionID).Scan(&rows); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("outbox rows = %d after mention removal, want 0", rows)
	}
}

func TestMentionEmailOutboxClearedWhenMentionRemovedFromComment(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "edit-removed")
	env.grantView(t)

	manager := &mentionRecordingNotificationManager{}
	notifications := NewNotificationService(env.db, manager, env.perm, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = notifications.Close() })

	sender := &fakeMentionEmailSender{configured: true}
	emailService := NewMentionEmailService(env.db, sender, &fakeMentionMarker{}, env.authorizer)

	mentionService := NewMentionService(env.db, notifications, env.perm)
	mentionService.SetMentionEmailNotifier(emailService)

	const sourceID = 5000
	if err := mentionService.ProcessMentions(ProcessMentionsParams{
		SourceType: "comment", SourceID: sourceID,
		Content: "Hey @mention-recipient-edit-removed, please review.",
		ItemID: env.itemID, WorkspaceID: env.workspaceID, ActorUserID: env.actorID,
	}); err != nil {
		t.Fatalf("ProcessMentions (add): %v", err)
	}

	var outboxRows int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM mention_email_outbox`).Scan(&outboxRows); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxRows != 1 {
		t.Fatalf("outbox rows after mention = %d, want 1", outboxRows)
	}

	// Edit the comment to drop the mention.
	if err := mentionService.ProcessMentions(ProcessMentionsParams{
		SourceType: "comment", SourceID: sourceID,
		Content: "Hey, please review.",
		ItemID: env.itemID, WorkspaceID: env.workspaceID, ActorUserID: env.actorID,
	}); err != nil {
		t.Fatalf("ProcessMentions (remove): %v", err)
	}

	if err := env.db.QueryRow(`SELECT COUNT(*) FROM mention_email_outbox`).Scan(&outboxRows); err != nil {
		t.Fatalf("count outbox after removal: %v", err)
	}
	if outboxRows != 0 {
		t.Fatalf("outbox rows after mention removed = %d, want 0", outboxRows)
	}

	delivered, err := emailService.ProcessPendingMentions(context.Background(), 50)
	if err != nil {
		t.Fatalf("ProcessPendingMentions: %v", err)
	}
	if delivered != 0 || len(sender.sent) != 0 {
		t.Fatalf("delivered=%d sent=%d after mention removal, want 0/0", delivered, len(sender.sent))
	}
}

func TestMentionEmailOutboxRefreshesContentOnCommentEdit(t *testing.T) {
	env := setupMentionEmailTestEnv(t, "edit-updated")
	env.grantView(t)

	manager := &mentionRecordingNotificationManager{}
	notifications := NewNotificationService(env.db, manager, env.perm, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = notifications.Close() })

	sender := &fakeMentionEmailSender{configured: true}
	emailService := NewMentionEmailService(env.db, sender, &fakeMentionMarker{}, env.authorizer)

	mentionService := NewMentionService(env.db, notifications, env.perm)
	mentionService.SetMentionEmailNotifier(emailService)

	const sourceID = 5001
	if err := mentionService.ProcessMentions(ProcessMentionsParams{
		SourceType: "comment", SourceID: sourceID,
		Content: "Original body mentioning @mention-recipient-edit-updated here.",
		ItemID: env.itemID, WorkspaceID: env.workspaceID, ActorUserID: env.actorID,
	}); err != nil {
		t.Fatalf("ProcessMentions (create): %v", err)
	}

	// Edit the comment, keeping the mention but changing the text.
	if err := mentionService.ProcessMentions(ProcessMentionsParams{
		SourceType: "comment", SourceID: sourceID,
		Content: "Edited body mentioning @mention-recipient-edit-updated here.",
		ItemID: env.itemID, WorkspaceID: env.workspaceID, ActorUserID: env.actorID,
	}); err != nil {
		t.Fatalf("ProcessMentions (edit): %v", err)
	}

	var content string
	if err := env.db.QueryRow(`SELECT content FROM mention_email_outbox ORDER BY id DESC LIMIT 1`).Scan(&content); err != nil {
		t.Fatalf("read outbox content: %v", err)
	}
	if content != "Edited body mentioning @mention-recipient-edit-updated here." {
		t.Fatalf("outbox content = %q, want the edited body", content)
	}

	delivered, err := emailService.ProcessPendingMentions(context.Background(), 50)
	if err != nil {
		t.Fatalf("ProcessPendingMentions: %v", err)
	}
	if delivered != 1 || len(sender.sent) != 1 {
		t.Fatalf("delivered=%d sent=%d, want 1/1", delivered, len(sender.sent))
	}
	data, ok := sender.sent[0].data.(MentionEmailData)
	if !ok {
		t.Fatalf("data type = %T, want MentionEmailData", sender.sent[0].data)
	}
	if data.Content != "Edited body mentioning @mention-recipient-edit-updated here." {
		t.Fatalf("delivered content = %q, want the edited body", data.Content)
	}
}
