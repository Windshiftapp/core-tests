//go:build test

package scheduler_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/handlers"
	"windshift/internal/repository"
	"windshift/internal/scheduler"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

type reminderFixture struct {
	db          database.Database
	scheduler   *scheduler.DueDateReminderScheduler
	assigneeID  int
	workspaceID int
	statusID    int
}

func newReminderFixture(t *testing.T) *reminderFixture {
	return newReminderFixtureWith(t, 100, false)
}

// newReminderFixtureWith builds the sweep with an explicit batch size and,
// optionally, a real permission service so recipient authorization is enforced
// instead of the nil-permission test default.
func newReminderFixtureWith(t *testing.T, batchSize int, withPermissions bool) *reminderFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	f := factory.NewTestFactory(db)

	assigneeID, err := f.CreateUser(&factory.CreateUserOpts{Email: "reminder-assignee@example.com", Username: "reminder-assignee", IsActive: true})
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	workspaceID, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{CreatorID: assigneeID, Name: "Reminders", Key: "REM"})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var statusID int
	if err := db.QueryRow(`
		SELECT s.id FROM statuses s
		JOIN status_categories sc ON sc.id = s.category_id
		WHERE COALESCE(sc.is_completed, FALSE) = FALSE
		ORDER BY s.id LIMIT 1
	`).Scan(&statusID); err != nil {
		t.Fatalf("find open status: %v", err)
	}

	var permService *services.PermissionService
	if withPermissions {
		permService, err = services.NewPermissionService(db, services.DefaultPermissionCacheConfig())
		if err != nil {
			t.Fatalf("new permission service: %v", err)
		}
		t.Cleanup(func() { _ = permService.Close() })
	}

	manager, err := handlers.NewNotificationManager(db, handlers.DefaultNotificationManagerConfig())
	if err != nil {
		t.Fatalf("new notification manager: %v", err)
	}
	t.Cleanup(manager.Stop)
	notificationService := services.NewNotificationService(db, manager, permService, services.DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = notificationService.Close() })

	reminderScheduler := scheduler.NewDueDateReminderScheduler(db, notificationService, scheduler.DueDateReminderSchedulerConfig{
		TickInterval: time.Hour,
		BatchSize:    batchSize,
	})
	return &reminderFixture{db: db, scheduler: reminderScheduler, assigneeID: assigneeID, workspaceID: workspaceID, statusID: statusID}
}

// enableReminders opts a user into due-date reminders with the given lead time.
func (f *reminderFixture) enableReminders(t *testing.T, userID, days int) {
	t.Helper()
	if err := repository.NewUserPreferencesRepository(f.db).UpsertReminderSettings(userID, true, days, time.Now()); err != nil {
		t.Fatalf("enable reminders for user %d: %v", userID, err)
	}
}

func (f *reminderFixture) createItem(t *testing.T, workspaceID int, title string, due *time.Time, assignee *int, statusID *int) int {
	t.Helper()
	itemID, err := factory.NewTestFactory(f.db).CreateItem(factory.CreateItemOpts{
		WorkspaceID: workspaceID,
		Title:       title,
		StatusID:    statusID,
		AssigneeID:  assignee,
		DueDate:     due,
	})
	if err != nil {
		t.Fatalf("create item %q: %v", title, err)
	}
	return itemID
}

func (f *reminderFixture) reminderCount(t *testing.T, itemID int) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE item_id = ? AND source_type = 'item.due_reminder'`, itemID).Scan(&count); err != nil {
		t.Fatalf("count reminders for item %d: %v", itemID, err)
	}
	return count
}

// TestDueDateReminderSchedulerCreatesReminderForOptedInUser pins the core
// behaviour: only a user who enabled reminders gets one, and a second sweep is
// a no-op.
func TestDueDateReminderSchedulerCreatesReminderForOptedInUser(t *testing.T) {
	f := newReminderFixture(t)
	f.enableReminders(t, f.assigneeID, 3)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	itemID := f.createItem(t, f.workspaceID, "Ship the release", &due, &f.assigneeID, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)

	var userID int
	var notifType, sourceType, title, message, actionURL, deliveryKey, scope string
	if err := f.db.QueryRow(`
		SELECT user_id, type, source_type, title, message, action_url, delivery_key, authorization_scope
		FROM notifications WHERE item_id = ? AND source_type = 'item.due_reminder'
	`, itemID).Scan(&userID, &notifType, &sourceType, &title, &message, &actionURL, &deliveryKey, &scope); err != nil {
		t.Fatalf("read reminder: %v", err)
	}
	if userID != f.assigneeID {
		t.Errorf("recipient = %d, want assignee %d", userID, f.assigneeID)
	}
	if notifType != "reminder" {
		t.Errorf("type = %q, want reminder", notifType)
	}
	if scope != "workspace" {
		t.Errorf("authorization scope = %q, want workspace", scope)
	}
	if want := "Reminder: REM-1 is due soon"; title != want {
		t.Errorf("title = %q, want %q", title, want)
	}
	if want := "Ship the release is due on 2026-10-02"; message != want {
		t.Errorf("message = %q, want %q", message, want)
	}
	if want := "/workspaces/" + strconv.Itoa(f.workspaceID) + "/items/" + strconv.Itoa(itemID); actionURL != want {
		t.Errorf("action url = %q, want %q", actionURL, want)
	}
	if want := "due-reminder:" + strconv.Itoa(itemID) + ":2026-10-02:" + strconv.Itoa(f.assigneeID); deliveryKey != want {
		t.Errorf("delivery key = %q, want %q", deliveryKey, want)
	}

	// A second sweep must not duplicate the reminder.
	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 1 {
		t.Fatalf("reminder count after second sweep = %d, want 1", got)
	}

	// Dismissing the notification must not resurrect it: the sent-reminder
	// ledger outlives the notification row.
	if _, err := f.db.ExecWrite(`DELETE FROM notifications WHERE item_id = ? AND source_type = 'item.due_reminder'`, itemID); err != nil {
		t.Fatalf("delete reminder notification: %v", err)
	}
	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 0 {
		t.Fatalf("reminder count after dismissal = %d, want 0", got)
	}
}

// TestDueDateReminderSchedulerRequiresOptIn proves reminders are never
// automatic: an assigned item gets nothing until the assignee opts in.
func TestDueDateReminderSchedulerRequiresOptIn(t *testing.T) {
	f := newReminderFixture(t)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	itemID := f.createItem(t, f.workspaceID, "Opt-in required", &due, &f.assigneeID, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 0 {
		t.Fatalf("reminder count before opt-in = %d, want 0", got)
	}

	f.enableReminders(t, f.assigneeID, 3)
	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 1 {
		t.Fatalf("reminder count after opt-in = %d, want 1", got)
	}
}

// TestDueDateReminderSchedulerHonorsPerUserLeadTime proves the window is the
// recipient's configured days, not a global default.
func TestDueDateReminderSchedulerHonorsPerUserLeadTime(t *testing.T) {
	f := newReminderFixture(t)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) // 5 days out
	itemID := f.createItem(t, f.workspaceID, "Five days out", &due, &f.assigneeID, &f.statusID)

	f.enableReminders(t, f.assigneeID, 3)
	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 0 {
		t.Fatalf("reminder count with 3-day lead = %d, want 0", got)
	}

	f.enableReminders(t, f.assigneeID, 7)
	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 1 {
		t.Fatalf("reminder count with 7-day lead = %d, want 1", got)
	}
}

// TestDueDateReminderSchedulerRemindsPersonalWorkspaceOwner proves personal
// workspace items without an assignee remind their owner when the owner opted in.
func TestDueDateReminderSchedulerRemindsPersonalWorkspaceOwner(t *testing.T) {
	f := newReminderFixture(t)
	f.enableReminders(t, f.assigneeID, 3)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	ownerID := f.assigneeID
	personal, err := services.NewWorkspaceService(f.db).Create(context.Background(), services.CreateWorkspaceParams{
		Name:       "Personal",
		Key:        "PERSONAL",
		IsPersonal: true,
		OwnerID:    &ownerID,
		CreatorID:  ownerID,
	})
	if err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}
	itemID := f.createItem(t, personal.Workspace.ID, "Personal errand", &due, nil, nil)

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 1 {
		t.Fatalf("personal workspace reminder count = %d, want 1", got)
	}
	var recipient int
	if err := f.db.QueryRow(`SELECT user_id FROM notifications WHERE item_id = ? AND source_type = 'item.due_reminder'`, itemID).Scan(&recipient); err != nil {
		t.Fatalf("read personal reminder recipient: %v", err)
	}
	if recipient != ownerID {
		t.Fatalf("personal reminder recipient = %d, want owner %d", recipient, ownerID)
	}
}

// TestDueDateReminderSchedulerSkipsIneligibleItems covers the remaining
// filters: completed items and items in shared workspaces without an assignee.
func TestDueDateReminderSchedulerSkipsIneligibleItems(t *testing.T) {
	f := newReminderFixture(t)
	f.enableReminders(t, f.assigneeID, 3)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	upcoming := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	var completedStatusID int
	if err := f.db.QueryRow(`
		SELECT s.id FROM statuses s
		JOIN status_categories sc ON sc.id = s.category_id
		WHERE COALESCE(sc.is_completed, FALSE) = TRUE
		ORDER BY s.id LIMIT 1
	`).Scan(&completedStatusID); err != nil {
		t.Fatalf("find completed status: %v", err)
	}

	completed := f.createItem(t, f.workspaceID, "Completed item", &upcoming, &f.assigneeID, &completedStatusID)
	unassigned := f.createItem(t, f.workspaceID, "Unassigned item", &upcoming, nil, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)

	if got := f.reminderCount(t, completed); got != 0 {
		t.Errorf("completed item got %d reminders, want 0", got)
	}
	if got := f.reminderCount(t, unassigned); got != 0 {
		t.Errorf("unassigned shared-workspace item got %d reminders, want 0", got)
	}
}

// TestDueDateReminderSchedulerReRemindsAfterDueDateChange proves the ledger is
// scoped to the due date: rescheduling the item produces a fresh reminder.
func TestDueDateReminderSchedulerReRemindsAfterDueDateChange(t *testing.T) {
	f := newReminderFixture(t)
	f.enableReminders(t, f.assigneeID, 7)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	firstDue := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	itemID := f.createItem(t, f.workspaceID, "Rescheduled item", &firstDue, &f.assigneeID, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 1 {
		t.Fatalf("first reminder count = %d, want 1", got)
	}

	secondDue := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	if _, err := f.db.ExecWrite(`UPDATE items SET due_date = ? WHERE id = ?`, secondDue, itemID); err != nil {
		t.Fatalf("reschedule item: %v", err)
	}

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 2 {
		t.Fatalf("reminder count after reschedule = %d, want 2", got)
	}
}

// TestDueDateReminderSchedulerAdvancesPastFullBatch is the regression test for
// batch starvation: with more due items than one batch, a later sweep must pick
// up the items the earlier sweep could not fit instead of re-selecting the
// already-reminded ones forever.
func TestDueDateReminderSchedulerAdvancesPastFullBatch(t *testing.T) {
	f := newReminderFixtureWith(t, 2, false)
	f.enableReminders(t, f.assigneeID, 7)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	dueDates := []time.Time{
		time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
	itemIDs := make([]int, len(dueDates))
	for i := range dueDates {
		itemIDs[i] = f.createItem(t, f.workspaceID, "Batch item", &dueDates[i], &f.assigneeID, &f.statusID)
	}

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemIDs[0]); got != 1 {
		t.Fatalf("first item reminder count = %d, want 1", got)
	}
	if got := f.reminderCount(t, itemIDs[2]); got != 0 {
		t.Fatalf("third item reminder count after first sweep = %d, want 0", got)
	}

	f.scheduler.RunOnce(context.Background(), now)
	for i, itemID := range itemIDs {
		if got := f.reminderCount(t, itemID); got != 1 {
			t.Errorf("item %d reminder count after second sweep = %d, want 1", i, got)
		}
	}
}

// TestDueDateReminderSchedulerDoesNotStarveBehindOutOfWindowItems proves the
// per-user lead window is applied before the batch limit: an item that is still
// outside a short-lead recipient's window must not consume the only batch slot
// and starve an item that is inside a longer-lead recipient's window.
func TestDueDateReminderSchedulerDoesNotStarveBehindOutOfWindowItems(t *testing.T) {
	f := newReminderFixtureWith(t, 1, false)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

	// Short lead: an item due 10-05 is outside this recipient's 1-day window but
	// sorts before the long-lead item below.
	f.enableReminders(t, f.assigneeID, 1)
	shortDue := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	shortItem := f.createItem(t, f.workspaceID, "Out of window", &shortDue, &f.assigneeID, &f.statusID)

	// Long lead: an item due 10-15 is inside this recipient's 30-day window.
	longLeadID, err := factory.NewTestFactory(f.db).CreateUser(&factory.CreateUserOpts{Email: "long-lead@example.com", Username: "long-lead", IsActive: true})
	if err != nil {
		t.Fatalf("create long-lead user: %v", err)
	}
	f.enableReminders(t, longLeadID, 30)
	longDue := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	longItem := f.createItem(t, f.workspaceID, "In window", &longDue, &longLeadID, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, shortItem); got != 0 {
		t.Errorf("out-of-window item reminder count = %d, want 0", got)
	}
	if got := f.reminderCount(t, longItem); got != 1 {
		t.Fatalf("in-window item reminder count = %d, want 1", got)
	}
}

// TestDueDateReminderSchedulerSkipsInactiveRecipient proves the consolidated
// path applies the same recipient eligibility as other notifications: an
// opted-in but inactive assignee is not notified.
func TestDueDateReminderSchedulerSkipsInactiveRecipient(t *testing.T) {
	f := newReminderFixture(t)
	inactiveID, err := factory.NewTestFactory(f.db).CreateUser(&factory.CreateUserOpts{Email: "inactive-reminder@example.com", Username: "inactive-reminder", IsActive: false})
	if err != nil {
		t.Fatalf("create inactive user: %v", err)
	}
	f.enableReminders(t, inactiveID, 3)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	itemID := f.createItem(t, f.workspaceID, "Inactive assignee", &due, &inactiveID, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, itemID); got != 0 {
		t.Fatalf("inactive recipient reminder count = %d, want 0", got)
	}
}

// TestDueDateReminderSchedulerAdvancesPastIneligibleRecipient proves a
// recipient filtered by the shared eligibility rules (here: inactive) still
// records the ledger, so it does not occupy a batch slot and starve a later
// eligible recipient.
func TestDueDateReminderSchedulerAdvancesPastIneligibleRecipient(t *testing.T) {
	f := newReminderFixtureWith(t, 1, false)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

	inactiveID, err := factory.NewTestFactory(f.db).CreateUser(&factory.CreateUserOpts{Email: "inactive-batch@example.com", Username: "inactive-batch", IsActive: false})
	if err != nil {
		t.Fatalf("create inactive user: %v", err)
	}
	f.enableReminders(t, inactiveID, 3)
	earlyDue := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	inactiveItem := f.createItem(t, f.workspaceID, "Inactive first", &earlyDue, &inactiveID, &f.statusID)

	f.enableReminders(t, f.assigneeID, 3)
	laterDue := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	activeItem := f.createItem(t, f.workspaceID, "Active second", &laterDue, &f.assigneeID, &f.statusID)

	// The first sweep only fits the earlier inactive item; the second must
	// reach the eligible item once the inactive candidate is excluded.
	f.scheduler.RunOnce(context.Background(), now)
	f.scheduler.RunOnce(context.Background(), now)

	if got := f.reminderCount(t, inactiveItem); got != 0 {
		t.Errorf("inactive recipient reminder count = %d, want 0", got)
	}
	if got := f.reminderCount(t, activeItem); got != 1 {
		t.Fatalf("eligible recipient reminder count = %d, want 1", got)
	}
}

// TestDueDateReminderSchedulerRequiresWorkspaceViewPermission proves the
// reminder reuses the shared recipient authorization: an opted-in assignee who
// cannot view the item's workspace is not notified, while a workspace member is.
func TestDueDateReminderSchedulerRequiresWorkspaceViewPermission(t *testing.T) {
	f := newReminderFixtureWith(t, 100, true)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	due := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	// The shared-workspace creator has item.view and receives the reminder.
	f.enableReminders(t, f.assigneeID, 3)
	memberItem := f.createItem(t, f.workspaceID, "Member item", &due, &f.assigneeID, &f.statusID)

	// Personal workspaces grant permissions only to their owner, so an item
	// assigned to an outsider there is not visible to them.
	ownerID := f.assigneeID
	personal, err := services.NewWorkspaceService(f.db).Create(context.Background(), services.CreateWorkspaceParams{
		Name:       "Permission Personal",
		Key:        "PERMPERSONAL",
		IsPersonal: true,
		OwnerID:    &ownerID,
		CreatorID:  ownerID,
	})
	if err != nil {
		t.Fatalf("create personal workspace: %v", err)
	}
	outsiderID, err := factory.NewTestFactory(f.db).CreateUser(&factory.CreateUserOpts{Email: "outsider-reminder@example.com", Username: "outsider-reminder", IsActive: true})
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	f.enableReminders(t, outsiderID, 3)
	outsiderItem := f.createItem(t, personal.Workspace.ID, "Outsider item", &due, &outsiderID, &f.statusID)

	f.scheduler.RunOnce(context.Background(), now)
	if got := f.reminderCount(t, memberItem); got != 1 {
		t.Errorf("workspace member reminder count = %d, want 1", got)
	}
	if got := f.reminderCount(t, outsiderItem); got != 0 {
		t.Fatalf("outsider reminder count = %d, want 0", got)
	}
}

// TestDueDateReminderSchedulerStartStopAreIdempotent proves repeated lifecycle
// calls are safe: a second Start must not spawn a second worker and a second
// Stop must not close an already-closed channel.
func TestDueDateReminderSchedulerStartStopAreIdempotent(t *testing.T) {
	s := scheduler.NewDueDateReminderScheduler(nil, nil, scheduler.DueDateReminderSchedulerConfig{
		TickInterval: time.Hour,
	})
	s.Start()
	s.Start()
	s.Stop()
	s.Stop()
}
