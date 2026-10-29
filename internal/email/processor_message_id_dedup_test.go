//go:build test

package email_test

import (
	"context"
	"testing"

	"windshift/internal/email"
	"windshift/internal/models"
)

// TestProcessor_MessageIDLessDedupIsFolderScoped is the WI-1723 regression:
// IMAP UID is mailbox/folder-scoped, so two Message-ID-less messages with the
// same UIDVALIDITY/UID in different intakes on one mailbox must both be
// ingested. Same-folder retries stay idempotent, a UIDVALIDITY change is a new
// message, and a shared real Message-ID still deduplicates globally.
func TestProcessor_MessageIDLessDedupIsFolderScoped(t *testing.T) {
	env := setupProcessorTestEnv(t, false)
	ctx := context.Background()

	intakeA := &models.Intake{ID: 1}
	intakeB := &models.Intake{ID: 2}

	process := func(messageID string, uid uint32, uidValidity uint32, intake *models.Intake) *email.ProcessingResult {
		t.Helper()
		message := newTestEmail(messageID, "Message", "body")
		message.UID = uid
		result, err := env.processor.ProcessEmailWithIntake(ctx, message, env.channelID, uidValidity, env.config, intake)
		if err != nil {
			t.Fatalf("process message uid=%d validity=%d intake=%d: %v", uid, uidValidity, intake.ID, err)
		}
		return result
	}

	folderA := process("", 7, 100, intakeA)
	if folderA.Action != email.ActionItemCreated {
		t.Fatalf("folder A action = %s, want item_created", folderA.Action)
	}

	// Same mailbox, same UIDVALIDITY/UID, different folder: a distinct message.
	folderB := process("", 7, 100, intakeB)
	if folderB.Action != email.ActionItemCreated {
		t.Fatalf("folder B action = %s, want item_created (folder-scoped dedup)", folderB.Action)
	}
	if folderB.ItemID == nil || *folderB.ItemID == *folderA.ItemID {
		t.Fatalf("folder B item = %v, want a distinct ticket from %v", folderB.ItemID, folderA.ItemID)
	}

	// Same-folder retry stays idempotent.
	if retry := process("", 7, 100, intakeA); retry.Action != email.ActionAlreadyExists {
		t.Fatalf("folder A retry action = %s, want already_exists", retry.Action)
	}

	// A UIDVALIDITY change is a new message in the same folder.
	if reissued := process("", 7, 101, intakeA); reissued.Action != email.ActionItemCreated {
		t.Fatalf("reissued UID action = %s, want item_created", reissued.Action)
	}

	// A real Message-ID deduplicates across folders.
	shared := process("<shared@example.com>", 8, 100, intakeA)
	if shared.Action != email.ActionItemCreated {
		t.Fatalf("shared message first action = %s, want item_created", shared.Action)
	}
	if duplicate := process("<shared@example.com>", 9, 100, intakeB); duplicate.Action != email.ActionAlreadyExists {
		t.Fatalf("shared message duplicate action = %s, want already_exists", duplicate.Action)
	}

	// Intake provenance is recorded per folder.
	var intakeID int
	if err := env.db.QueryRow(`
		SELECT intake_id FROM email_message_tracking WHERE item_id = ? AND direction = 'inbound'
	`, *folderB.ItemID).Scan(&intakeID); err != nil {
		t.Fatalf("load folder B provenance: %v", err)
	}
	if intakeID != intakeB.ID {
		t.Fatalf("folder B provenance = %d, want %d", intakeID, intakeB.ID)
	}
}
