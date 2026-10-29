//go:build test

package services_test

import (
	"testing"

	"windshift/internal/services"
	"windshift/internal/testutils"
)

type recordingParticipantNotifier struct {
	calls []struct{ itemID, customerID, actorID int }
}

func (n *recordingParticipantNotifier) SendParticipantAddedNotice(itemID, customerID, actorID int) (bool, string, error) {
	n.calls = append(n.calls, struct{ itemID, customerID, actorID int }{itemID, customerID, actorID})
	return true, "", nil
}

// TestItemParticipantServiceAddNotifiesAndRecordsHistory covers the add
// side-effects (WI-1136): the customer-facing notifier fires exactly once for a
// new participant, never for an idempotent re-add, and item history records the
// change.
func TestItemParticipantServiceAddNotifiesAndRecordsHistory(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	workspaceID := testutils.InsertID(t, db, `INSERT INTO workspaces (name, key) VALUES ('Participant svc', 'PSV')`)
	actorID := testutils.InsertID(t, db,
		`INSERT INTO users (email, username, first_name, last_name) VALUES ('svc-actor@example.test', 'svc-actor', 'Svc', 'Actor')`)
	itemID := testutils.InsertID(t, db,
		`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Svc item', ?)`,
		workspaceID, testutils.NextTestFracIndex())

	notifier := &recordingParticipantNotifier{}
	svc := services.NewItemParticipantService(db)
	svc.SetNotifier(notifier)

	list, created, err := svc.Add(t.Context(), actorID, itemID, services.ParticipantInput{
		Email: "external@example.test",
		Name:  "External",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !created || len(list) != 1 {
		t.Fatalf("created=%v list=%v, want one new participant", created, list)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1", len(notifier.calls))
	}
	if notifier.calls[0].itemID != itemID || notifier.calls[0].customerID != list[0].PortalCustomerID {
		t.Fatalf("notifier call = %+v, want item %d customer %d", notifier.calls[0], itemID, list[0].PortalCustomerID)
	}

	// Idempotent re-add must not notify again.
	if _, created, err := svc.Add(t.Context(), actorID, itemID, services.ParticipantInput{Email: "external@example.test"}); err != nil || created {
		t.Fatalf("re-add created=%v err=%v, want false/nil", created, err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier calls after re-add = %d, want still 1", len(notifier.calls))
	}

	var historyCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM item_history WHERE item_id = ? AND field_name = 'participant_added'`, itemID,
	).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("history rows = %d, want 1", historyCount)
	}
}
