//go:build test

package services

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

type inactivityFixture struct {
	db          database.Database
	sweeper     *ActionInactivitySweeper
	emitter     *stubNodeAPI
	workspaceID int
	openStatus  int
	doneStatus  int
	itemNumber  int
	statusSeq   int
}

func newInactivityFixture(t *testing.T) *inactivityFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Inactive WS', 'INACT') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	f := &inactivityFixture{db: db, workspaceID: workspaceID, emitter: &stubNodeAPI{}}
	f.sweeper = NewActionInactivitySweeper(db, f.emitter, ActionInactivitySweeperConfig{TickInterval: time.Hour, BatchSize: 100})
	f.openStatus = f.mustInsertStatus(t, "Open", false)
	f.doneStatus = f.mustInsertStatus(t, "Done", true)
	return f
}

func (f *inactivityFixture) mustInsertStatus(t *testing.T, name string, completed bool) int {
	t.Helper()
	var categoryID int
	if err := f.db.QueryRow(`INSERT INTO status_categories (name, color, is_completed) VALUES (?, '#123456', ?) RETURNING id`, name+" category", completed).Scan(&categoryID); err != nil {
		t.Fatalf("insert status category %s: %v", name, err)
	}
	f.statusSeq++
	statusName := name + "-" + strconv.Itoa(f.statusSeq) // status names are globally unique
	var id int
	if err := f.db.QueryRow(`INSERT INTO statuses (name, category_id) VALUES (?, ?) RETURNING id`, statusName, categoryID).Scan(&id); err != nil {
		t.Fatalf("insert status %s: %v", statusName, err)
	}
	return id
}

// insertItem returns an open item last updated at updatedAt.
func (f *inactivityFixture) insertItem(t *testing.T, title string, statusID, itemTypeID int, updatedAt time.Time) int {
	t.Helper()
	f.itemNumber++
	var id int
	var err error
	if itemTypeID > 0 {
		err = f.db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, item_type_id, frac_index, updated_at) VALUES (?, ?, ?, 'x', ?, ?, ?, ?) RETURNING id`,
			f.workspaceID, f.itemNumber, title, statusID, itemTypeID, testutils.NextTestFracIndex(), updatedAt).Scan(&id)
	} else {
		err = f.db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, description, status_id, frac_index, updated_at) VALUES (?, ?, ?, 'x', ?, ?, ?) RETURNING id`,
			f.workspaceID, f.itemNumber, title, statusID, testutils.NextTestFracIndex(), updatedAt).Scan(&id)
	}
	if err != nil {
		t.Fatalf("insert item %s: %v", title, err)
	}
	return id
}

func (f *inactivityFixture) insertComment(t *testing.T, itemID int, at time.Time) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO comments (item_id, author_id, content, is_private, created_at, updated_at) VALUES (?, ?, 'note', false, ?, ?)`,
		itemID, 1, at, at); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
}

func (f *inactivityFixture) insertInactiveAction(t *testing.T, hours int) *models.Action {
	t.Helper()
	config, err := json.Marshal(models.ActionTriggerConfig{InactiveHours: hours})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var id int
	createdAt := time.Now().UTC()
	if err := f.db.QueryRow(`INSERT INTO actions (workspace_id, name, is_enabled, trigger_type, trigger_config, created_at, updated_at) VALUES (?, 'stale sweep', true, 'item_inactive', ?, ?, ?) RETURNING id`,
		f.workspaceID, string(config), createdAt, createdAt).Scan(&id); err != nil {
		t.Fatalf("insert action: %v", err)
	}
	actions, err := f.sweeper.actions.ListEnabledByTrigger(models.ActionTriggerItemInactive)
	if err != nil || len(actions) == 0 {
		t.Fatalf("load action: %v (len %d)", err, len(actions))
	}
	return actions[len(actions)-1]
}

func (f *inactivityFixture) markItemMerged(t *testing.T, itemID, intoID int) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE items SET merged_into_item_id = ? WHERE id = ?`, intoID, itemID); err != nil {
		t.Fatalf("merge item: %v", err)
	}
}

func TestInactivitySweeperFiresOnlyForStaleOpenItems(t *testing.T) {
	f := newInactivityFixture(t)
	now := time.Now().UTC()
	action := f.insertInactiveAction(t, 24)

	stale := f.insertItem(t, "stale", f.openStatus, 0, now.Add(-48*time.Hour))
	fresh := f.insertItem(t, "fresh", f.openStatus, 0, now.Add(-1*time.Hour))
	completed := f.insertItem(t, "completed", f.doneStatus, 0, now.Add(-72*time.Hour))
	f.markItemMerged(t, f.insertItem(t, "merged away", f.openStatus, 0, now.Add(-72*time.Hour)), stale)

	emitted := f.sweeper.SweepOnce(now)
	if emitted != 1 {
		t.Fatalf("emitted = %d, want exactly the stale open item", emitted)
	}
	event := f.emitter.emitted[0]
	if event.ItemID != stale || event.WorkspaceID != f.workspaceID {
		t.Fatalf("event = item %d ws %d, want item %d ws %d", event.ItemID, event.WorkspaceID, stale, f.workspaceID)
	}
	if event.EventType != models.ActionTriggerItemInactive {
		t.Fatalf("event type = %s", event.EventType)
	}
	if event.NewValues["action_id"] != action.ID {
		t.Fatalf("event not targeted at action %d: %v", action.ID, event.NewValues)
	}
	_ = fresh
	_ = completed
}

func TestInactivitySweeperCommentActivityKeepsItemEligible(t *testing.T) {
	f := newInactivityFixture(t)
	now := time.Now().UTC()
	f.insertInactiveAction(t, 24)

	item := f.insertItem(t, "old update, fresh comment", f.openStatus, 0, now.Add(-72*time.Hour))
	f.insertComment(t, item, now.Add(-1*time.Hour))

	if emitted := f.sweeper.SweepOnce(now); emitted != 0 {
		t.Fatalf("emitted = %d, want 0: the fresh comment makes the item active", emitted)
	}
}

func TestInactivitySweeperDoesNotRefireWithoutNewActivity(t *testing.T) {
	f := newInactivityFixture(t)
	now := time.Now().UTC()
	f.insertInactiveAction(t, 24)
	item := f.insertItem(t, "stale", f.openStatus, 0, now.Add(-48*time.Hour))

	if emitted := f.sweeper.SweepOnce(now); emitted != 1 {
		t.Fatalf("first sweep emitted %d, want 1", emitted)
	}
	if emitted := f.sweeper.SweepOnce(now.Add(time.Hour)); emitted != 0 {
		t.Fatalf("second sweep emitted %d, want 0: same stale period must not re-fire", emitted)
	}

	// New activity re-arms the trigger; after the threshold passes again the
	// item fires once more.
	newerActivity := now.Add(2 * time.Hour)
	f.insertComment(t, item, newerActivity)
	if emitted := f.sweeper.SweepOnce(newerActivity.Add(time.Hour)); emitted != 0 {
		t.Fatalf("emitted %d within the fresh threshold window, want 0", emitted)
	}
	if emitted := f.sweeper.SweepOnce(newerActivity.Add(25 * time.Hour)); emitted != 1 {
		t.Fatalf("re-armed sweep emitted %d, want 1", emitted)
	}
}

func TestInactivitySweeperSkipsZeroThresholdActions(t *testing.T) {
	f := newInactivityFixture(t)
	now := time.Now().UTC()

	config, err := json.Marshal(models.ActionTriggerConfig{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO actions (workspace_id, name, is_enabled, trigger_type, trigger_config, created_at, updated_at) VALUES (?, 'misconfigured', true, 'item_inactive', ?, ?, ?)`,
		f.workspaceID, string(config), now, now); err != nil {
		t.Fatalf("insert action: %v", err)
	}
	f.insertItem(t, "very stale", f.openStatus, 0, now.Add(-720*time.Hour))

	if emitted := f.sweeper.SweepOnce(now); emitted != 0 {
		t.Fatalf("emitted = %d, want 0 for an action without a threshold", emitted)
	}
}

func TestInactivitySweeperOnlyFiresItsOwnAction(t *testing.T) {
	f := newInactivityFixture(t)
	now := time.Now().UTC()
	first := f.insertInactiveAction(t, 24)
	second := f.insertInactiveAction(t, 24)

	if first.ID == second.ID {
		t.Fatal("fixture produced one action, want two")
	}
	f.insertItem(t, "stale", f.openStatus, 0, now.Add(-48*time.Hour))

	if emitted := f.sweeper.SweepOnce(now); emitted != 2 {
		t.Fatalf("emitted = %d, want one event per configured action", emitted)
	}
	if len(f.emitter.emitted) != 2 {
		t.Fatalf("emitter saw %d events, want 2", len(f.emitter.emitted))
	}
	sawFirst, sawSecond := false, false
	for _, event := range f.emitter.emitted {
		switch event.NewValues["action_id"] {
		case first.ID:
			sawFirst = true
		case second.ID:
			sawSecond = true
		}
	}
	if !sawFirst || !sawSecond {
		t.Fatalf("events not targeted per action: %v", f.emitter.emitted)
	}
}
