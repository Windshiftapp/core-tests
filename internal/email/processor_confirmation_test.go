//go:build test

package email_test

import (
	"context"
	"testing"

	"windshift/internal/email"
	"windshift/internal/models"
)

type confirmationCall struct {
	itemID     int
	customerID int
}

type recordingConfirmationSender struct {
	calls []confirmationCall
	err   error
}

func (r *recordingConfirmationSender) EnqueueIntakeConfirmation(itemID, customerID int) error {
	r.calls = append(r.calls, confirmationCall{itemID: itemID, customerID: customerID})
	return r.err
}

// The receipt is sent once per new ticket when the intake opts in, and never
// for a reply on the same thread.
func TestProcessor_SendsIntakeConfirmationOnceForNewItem(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	sender := &recordingConfirmationSender{}
	env.processor.SetConfirmationSender(sender)
	intake := &models.Intake{ID: 1, SendConfirmation: true}
	ctx := context.Background()

	result, err := env.processor.ProcessEmailWithIntake(
		ctx, newTestEmail("<conf-1@example.com>", "New request", "body"), env.channelID, 0, env.config, intake)
	if err != nil {
		t.Fatalf("new item: %v", err)
	}
	if result.Action != email.ActionItemCreated {
		t.Fatalf("action = %s, want %s", result.Action, email.ActionItemCreated)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("confirmation calls = %d, want 1", len(sender.calls))
	}
	if sender.calls[0].itemID != *result.ItemID || sender.calls[0].customerID != *result.CustomerID {
		t.Fatalf("confirmation args = %+v, want item %d customer %d",
			sender.calls[0], *result.ItemID, *result.CustomerID)
	}

	reply := newTestReplyEmail("<conf-reply-1@example.com>", "<conf-1@example.com>", "more info")
	if _, err := env.processor.ProcessEmailWithIntake(ctx, reply, env.channelID, 0, env.config, intake); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("reply triggered another confirmation; calls = %d, want 1", len(sender.calls))
	}
}

func TestProcessor_SkipsIntakeConfirmationWhenDisabled(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	sender := &recordingConfirmationSender{}
	env.processor.SetConfirmationSender(sender)
	intake := &models.Intake{ID: 1, SendConfirmation: false}

	if _, err := env.processor.ProcessEmailWithIntake(
		context.Background(), newTestEmail("<conf-off@example.com>", "New request", "body"), env.channelID, 0, env.config, intake); err != nil {
		t.Fatalf("new item: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("confirmation calls = %d, want 0 when the intake opts out", len(sender.calls))
	}
}

// Auto-generated mail never receives a receipt, so an auto-responder cannot
// bounce receipts back and forth with the mailbox.
func TestProcessor_SkipsIntakeConfirmationForAutomatedMail(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string][]string
	}{
		{name: "auto-submitted", headers: map[string][]string{"Auto-Submitted": {"auto-replied"}}},
		{name: "precedence bulk", headers: map[string][]string{"Precedence": {"bulk"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupProcessorTestEnv(t, false)
			sender := &recordingConfirmationSender{}
			env.processor.SetConfirmationSender(sender)
			intake := &models.Intake{ID: 1, SendConfirmation: true}
			message := newTestEmail("<conf-auto@example.com>", "Out of office", "body")
			message.RawHeaders = tc.headers

			if _, err := env.processor.ProcessEmailWithIntake(
				context.Background(), message, env.channelID, 0, env.config, intake); err != nil {
				t.Fatalf("new item: %v", err)
			}
			if len(sender.calls) != 0 {
				t.Fatalf("confirmation calls = %d, want 0 for automated mail", len(sender.calls))
			}
		})
	}
}

func TestProcessor_SkipsIntakeConfirmationWithoutIntake(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	sender := &recordingConfirmationSender{}
	env.processor.SetConfirmationSender(sender)

	if _, err := env.processor.ProcessEmail(
		context.Background(), newTestEmail("<conf-legacy@example.com>", "New request", "body"), env.channelID, 0, env.config); err != nil {
		t.Fatalf("new item: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("confirmation calls = %d, want 0 on the legacy channel path", len(sender.calls))
	}
}

// A reply whose parent is no longer tracked creates a fallback ticket but must
// not receive a receipt: replying to an outbound receipt is exactly how an
// auto-responder would start a confirmation loop.
func TestProcessor_SkipsIntakeConfirmationForReplyWithoutTrackedParent(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	sender := &recordingConfirmationSender{}
	env.processor.SetConfirmationSender(sender)
	intake := &models.Intake{ID: 1, SendConfirmation: true}

	orphanReply := newTestReplyEmail("<conf-orphan@example.com>", "<missing-parent@example.com>", "following up")
	result, err := env.processor.ProcessEmailWithIntake(
		context.Background(), orphanReply, env.channelID, 0, env.config, intake)
	if err != nil {
		t.Fatalf("orphan reply: %v", err)
	}
	if result.Action != email.ActionItemCreated {
		t.Fatalf("action = %s, want %s", result.Action, email.ActionItemCreated)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("confirmation calls = %d, want 0 for a reply", len(sender.calls))
	}
}
