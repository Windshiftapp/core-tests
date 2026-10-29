//go:build test

package email_test

import (
	"context"
	"testing"
	"time"

	"windshift/internal/email"
	"windshift/internal/models"
)

func rateLimitPtr(n int) *int { return &n }

func countItems(t *testing.T, env *processorTestEnv) int {
	t.Helper()
	var n int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM items WHERE channel_id = ?`, env.channelID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

type trackingRow struct {
	ItemID        *int
	RateLimitedAt *time.Time
	UID           int
	UIDValidity   int
}

func getTrackingRow(t *testing.T, env *processorTestEnv, dedupKey string) trackingRow {
	t.Helper()
	var row trackingRow
	var itemID *int
	var rateLimitedAt *time.Time
	err := env.db.QueryRow(`
		SELECT item_id, rate_limited_at, uid, uid_validity
		FROM email_message_tracking
		WHERE channel_id = ? AND dedup_key = ?
	`, env.channelID, dedupKey).Scan(&itemID, &rateLimitedAt, &row.UID, &row.UIDValidity)
	if err != nil {
		t.Fatalf("load tracking row %s: %v", dedupKey, err)
	}
	row.ItemID = itemID
	row.RateLimitedAt = rateLimitedAt
	return row
}

// TestResolveEmailRateLimitPerHour pins the config resolution contract: unset
// and negative fall back to the default, 0 disables, n caps at n.
func TestResolveEmailRateLimitPerHour(t *testing.T) {
	cases := []struct {
		name   string
		config *models.ChannelConfig
		want   int
	}{
		{"nil config", nil, email.DefaultEmailRateLimitPerHour},
		{"unset", &models.ChannelConfig{}, email.DefaultEmailRateLimitPerHour},
		{"zero disables", &models.ChannelConfig{EmailRateLimitPerHour: rateLimitPtr(0)}, 0},
		{"explicit cap", &models.ChannelConfig{EmailRateLimitPerHour: rateLimitPtr(5)}, 5},
		{"negative falls back to default", &models.ChannelConfig{EmailRateLimitPerHour: rateLimitPtr(-3)}, email.DefaultEmailRateLimitPerHour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := email.ResolveEmailRateLimitPerHour(tc.config); got != tc.want {
				t.Fatalf("ResolveEmailRateLimitPerHour() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestProcessor_RateLimit_CapsNewTicketsPerSender verifies the flood cap: the
// first N messages create tickets, the next one is declined with an explicit
// rate-limited outcome, the declined mail is recorded for requeue, and no
// item is created for it.
func TestProcessor_RateLimit_CapsNewTicketsPerSender(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	*env.config = models.ChannelConfig{
		EmailWorkspaceID:      env.config.EmailWorkspaceID,
		EmailItemTypeID:       env.config.EmailItemTypeID,
		EmailRateLimitPerHour: rateLimitPtr(2),
	}

	create := func(id string, uid uint32) *email.ProcessingResult {
		t.Helper()
		pe := newTestEmail("<"+id+"@example.com>", "Flood "+id, "body "+id)
		pe.UID = uid
		result, err := env.processor.ProcessEmail(ctx, pe, env.channelID, 77, env.config)
		if err != nil {
			t.Fatalf("ProcessEmail(%s): %v", id, err)
		}
		return result
	}

	if got := create("flood-1", 1).Action; got != email.ActionItemCreated {
		t.Fatalf("first message action = %q, want %q", got, email.ActionItemCreated)
	}
	if got := create("flood-2", 2).Action; got != email.ActionItemCreated {
		t.Fatalf("second message action = %q, want %q", got, email.ActionItemCreated)
	}
	third := create("flood-3", 3)
	if third.Action != email.ActionRateLimited {
		t.Fatalf("third message action = %q, want %q", third.Action, email.ActionRateLimited)
	}
	if third.ItemID != nil {
		t.Fatalf("rate-limited result carries item_id %v, want nil", *third.ItemID)
	}
	if got := countItems(t, env); got != 2 {
		t.Fatalf("items created = %d, want 2", got)
	}

	row := getTrackingRow(t, env, "flood-3@example.com")
	if row.RateLimitedAt == nil {
		t.Fatal("declined message has no rate_limited_at; operators could not see or requeue it")
	}
	if row.ItemID != nil {
		t.Fatalf("declined message created item %d, want none", *row.ItemID)
	}
	if row.UID != 3 || row.UIDValidity != 77 {
		t.Fatalf("declined message recorded uid=%d validity=%d, want 3/77", row.UID, row.UIDValidity)
	}

	// Redelivery of the same message while the claim is fresh defers without
	// creating a duplicate ticket.
	redelivered := create("flood-3", 3)
	if redelivered.Action != email.ActionDeferred {
		t.Fatalf("fresh redelivery action = %q, want %q", redelivered.Action, email.ActionDeferred)
	}
	if got := countItems(t, env); got != 2 {
		t.Fatalf("items after fresh redelivery = %d, want 2", got)
	}

	// Redelivery after the claim window is reclaimed and declined again with
	// the same explicit rate-limited outcome.
	if _, err := env.db.Exec(`
		UPDATE email_message_tracking SET processed_at = ? WHERE channel_id = ? AND dedup_key = ?
	`, time.Now().Add(-6*time.Minute), env.channelID, "flood-3@example.com"); err != nil {
		t.Fatalf("age declined claim: %v", err)
	}
	reclaimed := create("flood-3", 3)
	if reclaimed.Action != email.ActionRateLimited {
		t.Fatalf("reclaimed redelivery action = %q, want %q", reclaimed.Action, email.ActionRateLimited)
	}
	if got := countItems(t, env); got != 2 {
		t.Fatalf("items after reclaimed redelivery = %d, want 2", got)
	}
}

// TestProcessor_RateLimit_ZeroDisablesCap verifies 0 means unlimited.
func TestProcessor_RateLimit_ZeroDisablesCap(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	*env.config = models.ChannelConfig{
		EmailWorkspaceID:      env.config.EmailWorkspaceID,
		EmailItemTypeID:       env.config.EmailItemTypeID,
		EmailRateLimitPerHour: rateLimitPtr(0),
	}
	for i := 1; i <= 3; i++ {
		pe := newTestEmail("<zero-cap-"+string(rune('0'+i))+"@example.com>", "msg", "body")
		pe.UID = uint32(i)
		result, err := env.processor.ProcessEmail(ctx, pe, env.channelID, 7, env.config)
		if err != nil {
			t.Fatalf("ProcessEmail #%d: %v", i, err)
		}
		if result.Action != email.ActionItemCreated {
			t.Fatalf("message #%d action = %q, want %q", i, result.Action, email.ActionItemCreated)
		}
	}
	if got := countItems(t, env); got != 3 {
		t.Fatalf("items = %d, want 3", got)
	}
}

// TestProcessor_RateLimit_RepliesExempt verifies replies append to the thread
// even when the sender is over the new-ticket cap.
func TestProcessor_RateLimit_RepliesExempt(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	*env.config = models.ChannelConfig{
		EmailWorkspaceID:      env.config.EmailWorkspaceID,
		EmailItemTypeID:       env.config.EmailItemTypeID,
		EmailRateLimitPerHour: rateLimitPtr(1),
	}
	originalID := "<thread-parent@example.com>"

	first, err := env.processor.ProcessEmail(ctx, newTestEmail(originalID, "Issue", "help"), env.channelID, 7, env.config)
	if err != nil || first.ItemID == nil {
		t.Fatalf("first message: action=%q err=%v", first.Action, err)
	}

	// Over the cap: new conversations are declined.
	over, err := env.processor.ProcessEmail(ctx, newTestEmail("<over-cap@example.com>", "Another", "hello again"), env.channelID, 7, env.config)
	if err != nil {
		t.Fatalf("over-cap message: %v", err)
	}
	if over.Action != email.ActionRateLimited {
		t.Fatalf("over-cap action = %q, want %q", over.Action, email.ActionRateLimited)
	}

	// But a reply to the original thread still creates a comment.
	reply := newTestReplyEmail("<thread-reply@example.com>", originalID, "Thanks, that worked")
	replyResult, err := env.processor.ProcessEmail(ctx, reply, env.channelID, 7, env.config)
	if err != nil {
		t.Fatalf("reply message: %v", err)
	}
	if replyResult.Action != email.ActionCommentAdded {
		t.Fatalf("reply action = %q, want %q", replyResult.Action, email.ActionCommentAdded)
	}
	if replyResult.CommentID == nil || replyResult.ItemID == nil || *replyResult.ItemID != *first.ItemID {
		t.Fatalf("reply landed on item %v comment %v, want item %d with a new comment", replyResult.ItemID, replyResult.CommentID, *first.ItemID)
	}
	if got := countItems(t, env); got != 1 {
		t.Fatalf("items = %d, want 1 (replies must not create tickets)", got)
	}
}

// TestProcessor_RateLimit_WindowExpiry verifies the cap is a rolling hour: a
// sender whose tickets aged out of the window can create a new ticket again.
func TestProcessor_RateLimit_WindowExpiry(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	*env.config = models.ChannelConfig{
		EmailWorkspaceID:      env.config.EmailWorkspaceID,
		EmailItemTypeID:       env.config.EmailItemTypeID,
		EmailRateLimitPerHour: rateLimitPtr(1),
	}

	first, err := env.processor.ProcessEmail(ctx, newTestEmail("<window-1@example.com>", "First", "body"), env.channelID, 7, env.config)
	if err != nil || first.Action != email.ActionItemCreated {
		t.Fatalf("first message: action=%q err=%v", first.Action, err)
	}

	aged, err := env.processor.ProcessEmail(ctx, newTestEmail("<window-2@example.com>", "Second", "body"), env.channelID, 7, env.config)
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	if aged.Action != email.ActionRateLimited {
		t.Fatalf("second message action = %q, want %q", aged.Action, email.ActionRateLimited)
	}

	// Age the sender's rows out of the window: the ticket that filled the cap,
	// and the declined message's own claim so the requeue poll can reclaim it
	// (a fresh claim defers, matching the stale-claim recovery cadence).
	if _, err := env.db.Exec(`
		UPDATE email_message_tracking SET processed_at = ?
		WHERE channel_id = ?
	`, time.Now().Add(-2*time.Hour), env.channelID); err != nil {
		t.Fatalf("age tracking rows: %v", err)
	}

	recovered, err := env.processor.ProcessEmail(ctx, newTestEmail("<window-2@example.com>", "Second", "body"), env.channelID, 7, env.config)
	if err != nil {
		t.Fatalf("recovered message: %v", err)
	}
	if recovered.Action != email.ActionItemCreated {
		t.Fatalf("action after window expiry = %q, want %q", recovered.Action, email.ActionItemCreated)
	}
	row := getTrackingRow(t, env, "window-2@example.com")
	if row.ItemID == nil {
		t.Fatal("recovered tracking row has no item_id")
	}
	if row.RateLimitedAt != nil {
		t.Fatal("recovered tracking row still marked rate_limited_at; it would linger as waiting-for-requeue")
	}
	if got := countItems(t, env); got != 2 {
		t.Fatalf("items = %d, want 2", got)
	}
}

// TestProcessor_RateLimit_PerSenderIsolation verifies one flooding sender
// cannot block other senders from getting help.
func TestProcessor_RateLimit_PerSenderIsolation(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()
	*env.config = models.ChannelConfig{
		EmailWorkspaceID:      env.config.EmailWorkspaceID,
		EmailItemTypeID:       env.config.EmailItemTypeID,
		EmailRateLimitPerHour: rateLimitPtr(1),
	}

	flooder := newTestEmail("<flooder-1@example.com>", "Flooder", "body")
	flooder.From = email.EmailAddress{Name: "Flooder", Address: "flooder@example.com"}
	if _, err := env.processor.ProcessEmail(ctx, flooder, env.channelID, 7, env.config); err != nil {
		t.Fatalf("flooder first message: %v", err)
	}
	flood2 := newTestEmail("<flooder-2@example.com>", "Flooder again", "body")
	flood2.From = email.EmailAddress{Name: "Flooder", Address: "flooder@example.com"}
	result, err := env.processor.ProcessEmail(ctx, flood2, env.channelID, 7, env.config)
	if err != nil {
		t.Fatalf("flooder second message: %v", err)
	}
	if result.Action != email.ActionRateLimited {
		t.Fatalf("flooder second message action = %q, want %q", result.Action, email.ActionRateLimited)
	}

	other := newTestEmail("<other-1@other.example>", "Help please", "body")
	other.From = email.EmailAddress{Name: "Other", Address: "other@other.example"}
	otherResult, err := env.processor.ProcessEmail(ctx, other, env.channelID, 7, env.config)
	if err != nil {
		t.Fatalf("other sender message: %v", err)
	}
	if otherResult.Action != email.ActionItemCreated {
		t.Fatalf("other sender action = %q, want %q", otherResult.Action, email.ActionItemCreated)
	}
}
