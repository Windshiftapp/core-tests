//go:build test

package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// supportMetricsRound2Fixture seeds one workspace with a portal customer and
// exposes helpers for tickets, support events, and SLA cycles.
type supportMetricsRound2Fixture struct {
	db          database.Database
	app         *ItemApplicationService
	workspaceID int
	userID      int
	customerID  int
	metricID    int
}

func newSupportMetricsRound2Fixture(t *testing.T) *supportMetricsRound2Fixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permissions, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	app := NewItemApplicationService(db, permissions,
		NewItemCreationService(db, permissions),
		NewItemUpdateApplicationService(db, permissions),
		NewItemDeletionApplicationService(db, permissions),
	)

	var customerID int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email) VALUES ('Metrics Customer', 'metrics-customer@example.test')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}
	var metricID int
	if err := db.QueryRow(`
		INSERT INTO sla_metrics (workspace_id, name) VALUES (?, 'First response') RETURNING id`,
		data.WorkspaceID).Scan(&metricID); err != nil {
		t.Fatalf("insert sla metric: %v", err)
	}
	return &supportMetricsRound2Fixture{
		db: db, app: app, workspaceID: data.WorkspaceID, userID: data.UserID,
		customerID: customerID, metricID: metricID,
	}
}

var round2ItemNumber int

func (f *supportMetricsRound2Fixture) insertItem(t *testing.T, createdAt time.Time, assigneeID *int) int {
	t.Helper()
	round2ItemNumber++
	var id int
	if err := f.db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description,
			creator_portal_customer_id, assignee_id, frac_index, created_at, updated_at)
		VALUES (?, ?, 'ticket', '', ?, ?, ?, ?, ?) RETURNING id
	`, f.workspaceID, round2ItemNumber, f.customerID, assigneeID,
		testutils.NextTestFracIndex(), createdAt, createdAt).Scan(&id); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	return id
}

func (f *supportMetricsRound2Fixture) insertResolvedEvent(t *testing.T, itemID int, at time.Time) {
	t.Helper()
	f.insertSupportEvent(t, itemID, models.SupportEventResolved, at)
}

func (f *supportMetricsRound2Fixture) insertReopenedEvent(t *testing.T, itemID int, at time.Time) {
	t.Helper()
	f.insertSupportEvent(t, itemID, models.SupportEventReopened, at)
}

func (f *supportMetricsRound2Fixture) insertSupportEvent(t *testing.T, itemID int, kind string, at time.Time) {
	t.Helper()
	if _, err := f.db.Exec(`
		INSERT INTO item_support_events (workspace_id, item_id, kind, occurred_at)
		VALUES (?, ?, ?, ?)
	`, f.workspaceID, itemID, kind, at); err != nil {
		t.Fatalf("insert support event: %v", err)
	}
}

func (f *supportMetricsRound2Fixture) insertCompletedCycle(t *testing.T, itemID int, cycleNo int, stoppedAt time.Time, breached bool) {
	t.Helper()
	var breachedAt any
	if breached {
		breachedAt = stoppedAt
	}
	if _, err := f.db.Exec(`
		INSERT INTO item_sla_cycles
			(item_id, metric_id, cycle_no, status, started_at, stopped_at, breached_at,
			 goal_duration_ms, elapsed_ms, remaining_ms, last_calculated_at,
			 calendar_snapshot, goal_query_snapshot)
		VALUES (?, ?, ?, 'completed', ?, ?, ?, 3600000, 0, 0, ?, '{}', '')
	`, itemID, f.metricID, cycleNo, stoppedAt.Add(-time.Hour), stoppedAt, breachedAt, stoppedAt); err != nil {
		t.Fatalf("insert completed cycle: %v", err)
	}
}

func (f *supportMetricsRound2Fixture) insertOngoingCycle(t *testing.T, itemID int, cycleNo int, nextDeadline time.Time) {
	t.Helper()
	if _, err := f.db.Exec(`
		INSERT INTO item_sla_cycles
			(item_id, metric_id, cycle_no, status, started_at, next_deadline_at,
			 goal_duration_ms, elapsed_ms, remaining_ms, last_calculated_at,
			 calendar_snapshot, goal_query_snapshot)
		VALUES (?, ?, ?, 'ongoing', ?, ?, 3600000, 1000, 3599000, ?, '{}', '')
	`, itemID, f.metricID, cycleNo, nextDeadline.Add(-time.Hour), nextDeadline, time.Now().UTC()); err != nil {
		t.Fatalf("insert ongoing cycle: %v", err)
	}
}

func (f *supportMetricsRound2Fixture) query(from, to time.Time, segment string) (*models.SupportMetricsAggregate, error) {
	return f.app.AggregateSupportMetrics(context.Background(), SupportMetricsQuery{
		UserID:       f.userID,
		WorkspaceIDs: []int{f.workspaceID},
		From:         from,
		To:           to,
		Location:     time.UTC,
		Bucket:       SupportBucketDay,
		Segment:      segment,
	})
}

// TestSupportMetricsSLACompliantCyclesNotBreachedInSegments pins finding 4:
// the per-segment breach count must apply the same breached_at predicate as
// the summary, so compliant completed cycles are not reported as breaches.
func TestSupportMetricsSLACompliantCyclesNotBreachedInSegments(t *testing.T) {
	f := newSupportMetricsRound2Fixture(t)
	now := time.Now().UTC()

	compliant := f.insertItem(t, now.Add(-2*time.Hour), nil)
	breached := f.insertItem(t, now.Add(-2*time.Hour), nil)
	f.insertCompletedCycle(t, compliant, 1, now.Add(-time.Hour), false)
	f.insertCompletedCycle(t, breached, 1, now.Add(-time.Hour), true)

	doc, err := f.query(now.Add(-24*time.Hour), now.Add(24*time.Hour), SupportSegmentWorkspace)
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}
	if doc.Summary.SLA.Completed != 2 || doc.Summary.SLA.Breached != 1 {
		t.Fatalf("summary SLA = %+v, want completed=2 breached=1", doc.Summary.SLA)
	}
	key := fmt.Sprintf("%d", f.workspaceID)
	var segment *models.SupportMetricsSegment
	for i := range doc.Segments {
		if doc.Segments[i].Key == key {
			segment = &doc.Segments[i]
			break
		}
	}
	if segment == nil {
		t.Fatalf("segments = %#v, want workspace %s present", doc.Segments, key)
	}
	if segment.SLABreached != 1 {
		t.Fatalf("segment SLABreached = %d, want 1 (compliant cycle must not count)", segment.SLABreached)
	}
}

// TestSupportMetricsOngoingBreachUsesNow pins finding 5: an ongoing cycle whose
// deadline is still in the future must not count as breached just because the
// report window ends after the deadline.
func TestSupportMetricsOngoingBreachUsesNow(t *testing.T) {
	f := newSupportMetricsRound2Fixture(t)
	now := time.Now().UTC()

	future := f.insertItem(t, now.Add(-2*time.Hour), nil)
	f.insertOngoingCycle(t, future, 1, now.Add(2*time.Hour))
	overdue := f.insertItem(t, now.Add(-2*time.Hour), nil)
	f.insertOngoingCycle(t, overdue, 1, now.Add(-2*time.Hour))

	// The window ends tomorrow, after the future deadline. Only the overdue
	// cycle is breached as of now.
	doc, err := f.query(now.Add(-24*time.Hour), now.Add(24*time.Hour), SupportSegmentNone)
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}
	if doc.Summary.SLA.OngoingBreached != 1 {
		t.Fatalf("OngoingBreached = %d, want 1 (future deadline must not count)", doc.Summary.SLA.OngoingBreached)
	}
}

// TestSupportMetricsBacklogTrendIncludesOpeningBalance pins finding 6: tickets
// already open before the window must seed the running backlog instead of
// starting at zero.
func TestSupportMetricsBacklogTrendIncludesOpeningBalance(t *testing.T) {
	f := newSupportMetricsRound2Fixture(t)
	now := time.Now().UTC()
	from := now.Add(-48 * time.Hour)
	to := now

	items := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		items = append(items, f.insertItem(t, now.Add(-72*time.Hour), nil))
	}
	f.insertResolvedEvent(t, items[0], now.Add(-24*time.Hour))

	doc, err := f.query(from, to, SupportSegmentNone)
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}
	if len(doc.Buckets) == 0 {
		t.Fatal("no buckets returned")
	}
	last := doc.Buckets[len(doc.Buckets)-1]
	if last.BacklogEnd != 2 {
		t.Fatalf("last bucket BacklogEnd = %d, want 2 (3 opening - 1 resolved)", last.BacklogEnd)
	}
}

// TestSupportMetricsBacklogTrendCountsReopenedTicketsOnce pins the reopen case
// in the opening balance: a ticket resolved and reopened before the window is
// open at From and must not be subtracted twice for its two events.
func TestSupportMetricsBacklogTrendCountsReopenedTicketsOnce(t *testing.T) {
	f := newSupportMetricsRound2Fixture(t)
	now := time.Now().UTC()
	from := now.Add(-48 * time.Hour)
	to := now

	closed := f.insertItem(t, now.Add(-72*time.Hour), nil)
	reopened := f.insertItem(t, now.Add(-72*time.Hour), nil)
	f.insertResolvedEvent(t, closed, now.Add(-60*time.Hour))
	f.insertResolvedEvent(t, reopened, now.Add(-60*time.Hour))
	f.insertReopenedEvent(t, reopened, now.Add(-54*time.Hour))

	doc, err := f.query(from, to, SupportSegmentNone)
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}
	if len(doc.Buckets) == 0 {
		t.Fatal("no buckets returned")
	}
	last := doc.Buckets[len(doc.Buckets)-1]
	if last.BacklogEnd != 1 {
		t.Fatalf("last bucket BacklogEnd = %d, want 1 (one closed, one reopened)", last.BacklogEnd)
	}
}

// TestSupportMetricsSegmentKeepsBacklogOnlySegment pins finding 7: a segment
// with only pre-window backlog and no in-window creations must still appear.
func TestSupportMetricsSegmentKeepsBacklogOnlySegment(t *testing.T) {
	f := newSupportMetricsRound2Fixture(t)
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now

	f.insertItem(t, now.Add(-72*time.Hour), nil)
	f.insertItem(t, now.Add(-72*time.Hour), nil)

	doc, err := f.query(from, to, SupportSegmentWorkspace)
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}
	key := fmt.Sprintf("%d", f.workspaceID)
	for _, segment := range doc.Segments {
		if segment.Key == key {
			if segment.Backlog != 2 {
				t.Fatalf("segment backlog = %d, want 2", segment.Backlog)
			}
			return
		}
	}
	t.Fatalf("segments = %#v, want backlog-only workspace %s present", doc.Segments, key)
}

// TestSupportMetricsUnsetSegmentKeepsBacklog pins finding 8: tickets without a
// value for the chosen dimension keep their backlog in the unset segment.
func TestSupportMetricsUnsetSegmentKeepsBacklog(t *testing.T) {
	f := newSupportMetricsRound2Fixture(t)
	now := time.Now().UTC()

	// No assignee: the agent segment value is NULL.
	f.insertItem(t, now.Add(-2*time.Hour), nil)

	doc, err := f.query(now.Add(-24*time.Hour), now.Add(24*time.Hour), SupportSegmentAgent)
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}
	for _, segment := range doc.Segments {
		if segment.Key == "" {
			if segment.Backlog != 1 {
				t.Fatalf("unset segment backlog = %d, want 1", segment.Backlog)
			}
			return
		}
	}
	t.Fatalf("segments = %#v, want an unset (empty-key) segment", doc.Segments)
}
