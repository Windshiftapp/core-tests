package services

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"windshift/internal/database"
)

type analyticsDeliveryFixture struct {
	db          database.Database
	service     *AnalyticsService
	workspaceID int
	userID      int
	openStatus  int
	doneStatus  int
}

func newAnalyticsDeliveryFixture(t *testing.T) analyticsDeliveryFixture {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "analytics-delivery.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(query string, args ...interface{}) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("fixture insert: %v\nquery: %s", err, query)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return int(id)
	}

	workspaceID := insertID(`
		INSERT INTO workspaces (name, key, description, active, is_personal)
		VALUES ('Analytics Delivery', 'ADL', '', true, false)
	`)
	userID := insertID(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('analytics@example.test', 'analytics-test', 'Analytics', 'Test')
	`)
	openCategory := insertID(`
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Analytics To Do', '#64748b', '', false)
	`)
	doneCategory := insertID(`
		INSERT INTO status_categories (name, color, description, is_completed)
		VALUES ('Analytics Complete', '#22c55e', '', true)
	`)
	openStatus := insertID(`
		INSERT INTO statuses (name, description, category_id)
		VALUES ('Analytics Open', '', ?)
	`, openCategory)
	doneStatus := insertID(`
		INSERT INTO statuses (name, description, category_id)
		VALUES ('Analytics Done', '', ?)
	`, doneCategory)

	return analyticsDeliveryFixture{
		db: db, service: NewAnalyticsService(db), workspaceID: workspaceID,
		userID: userID, openStatus: openStatus, doneStatus: doneStatus,
	}
}

func (f analyticsDeliveryFixture) insertItem(
	t *testing.T,
	number int,
	title string,
	statusID int,
	createdAt time.Time,
) int {
	t.Helper()
	// CreateItem assigns the workspace item number; keep the parameter so the
	// call sites document the intended sequencing.
	itemID, err := CreateItem(f.db, ItemCreationParams{
		WorkspaceID: f.workspaceID,
		Title:       title,
		StatusID:    &statusID,
		CreatedAt:   &createdAt,
		UpdatedAt:   &createdAt,
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	return int(itemID)
}

func (f analyticsDeliveryFixture) insertStatusEvent(
	t *testing.T,
	itemID int,
	at time.Time,
	oldStatusID *int,
	newStatusID int,
) {
	t.Helper()
	var oldValue interface{}
	if oldStatusID != nil {
		oldValue = *oldStatusID
	}
	if _, err := f.db.ExecWrite(`
		INSERT INTO item_history (
			item_id, user_id, changed_at, field_name, old_value, new_value
		)
		VALUES (?, ?, ?, 'status_id', ?, ?)
	`, itemID, f.userID, at, oldValue, newStatusID); err != nil {
		t.Fatalf("insert status event: %v", err)
	}
}

func analyticsTestDate(value string) time.Time {
	return mustParseDate(value)
}

func mustParseDate(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestAnalyticsDeliveryTimeStopsAtFirstCompletion(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	createdAt := analyticsTestDate("2026-01-01")
	itemID := fixture.insertItem(t, 1, "Reopened delivery", fixture.doneStatus, createdAt)

	fixture.insertStatusEvent(t, itemID, createdAt, nil, fixture.openStatus)
	fixture.insertStatusEvent(t, itemID, analyticsTestDate("2026-01-11"), &fixture.openStatus, fixture.doneStatus)
	fixture.insertStatusEvent(t, itemID, analyticsTestDate("2026-01-20"), &fixture.doneStatus, fixture.openStatus)
	fixture.insertStatusEvent(t, itemID, analyticsTestDate("2026-02-10"), &fixture.openStatus, fixture.doneStatus)

	params := ResolveDatasetParams{
		WorkspaceID: fixture.workspaceID,
		StartDate:   analyticsTestDate("2026-01-01"),
		EndDate:     analyticsTestDate("2026-03-31"),
	}
	fixture.service.now = func() time.Time { return analyticsTestDate("2026-04-01") }
	first, err := fixture.service.GetAnalytics(params)
	if err != nil {
		t.Fatalf("GetAnalytics: %v", err)
	}
	fixture.service.now = func() time.Time { return analyticsTestDate("2027-04-01") }
	later, err := fixture.service.GetAnalytics(params)
	if err != nil {
		t.Fatalf("GetAnalytics one year later: %v", err)
	}

	if first.DeliveryTime.TotalItemsAnalyzed != 1 {
		t.Fatalf("delivery samples = %d, want 1", first.DeliveryTime.TotalItemsAnalyzed)
	}
	if first.SchemaVersion != 2 {
		t.Fatalf("schema version = %d, want 2", first.SchemaVersion)
	}
	if first.DeliveryTime.MedianDays != 10 {
		t.Fatalf("median delivery = %.2f days, want 10", first.DeliveryTime.MedianDays)
	}
	if later.DeliveryTime.MedianDays != first.DeliveryTime.MedianDays {
		t.Fatalf(
			"delivery time changed with wall clock: before=%.2f after=%.2f",
			first.DeliveryTime.MedianDays,
			later.DeliveryTime.MedianDays,
		)
	}
	if first.Throughput.TotalCompleted != 1 {
		t.Fatalf("first-completion throughput = %d, want 1", first.Throughput.TotalCompleted)
	}
	if first.DeliveryTime.SlowestItems[0].CompletedDate != "2026-01-11" {
		t.Fatalf("completion date = %q, want first completion", first.DeliveryTime.SlowestItems[0].CompletedDate)
	}
}

func TestAnalyticsHealthAndAgingIgnoreCompletedWork(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	oldItem := fixture.insertItem(
		t, 1, "Needs attention", fixture.openStatus, analyticsTestDate("2026-01-01"),
	)
	if _, err := fixture.db.ExecWrite(`
		UPDATE items
		SET due_date = '2026-03-01', assignee_id = NULL, priority_id = NULL,
		    story_points = NULL, estimate_minutes = NULL,
		    last_active_at = '2026-01-02'
		WHERE id = ?
	`, oldItem); err != nil {
		t.Fatalf("configure attention item: %v", err)
	}

	recentItem := fixture.insertItem(
		t, 2, "Recent work", fixture.openStatus, analyticsTestDate("2026-03-30"),
	)
	if _, err := fixture.db.ExecWrite(`
		UPDATE items
		SET assignee_id = ?, story_points = 3, last_active_at = '2026-03-31'
		WHERE id = ?
	`, fixture.userID, recentItem); err != nil {
		t.Fatalf("configure recent item: %v", err)
	}

	doneItem := fixture.insertItem(
		t, 3, "Already done", fixture.doneStatus, analyticsTestDate("2025-01-01"),
	)
	fixture.insertStatusEvent(t, doneItem, analyticsTestDate("2025-01-01"), nil, fixture.doneStatus)

	fixture.service.now = func() time.Time { return analyticsTestDate("2026-04-01") }
	result, err := fixture.service.GetAnalytics(ResolveDatasetParams{
		WorkspaceID: fixture.workspaceID,
		StartDate:   analyticsTestDate("2026-01-01"),
		EndDate:     analyticsTestDate("2026-04-01"),
	})
	if err != nil {
		t.Fatalf("GetAnalytics: %v", err)
	}

	if result.Health.UnfinishedItems != 2 {
		t.Fatalf("unfinished = %d, want 2", result.Health.UnfinishedItems)
	}
	if result.Health.Overdue != 1 || result.Health.Stale != 1 {
		t.Fatalf("overdue/stale = %d/%d, want 1/1", result.Health.Overdue, result.Health.Stale)
	}
	if result.Health.Unassigned != 1 {
		t.Fatalf("unassigned = %d, want 1", result.Health.Unassigned)
	}
	if result.AgingWIP.TotalItems != 2 {
		t.Fatalf("aging WIP total = %d, want 2", result.AgingWIP.TotalItems)
	}
	if result.AgingWIP.Buckets[4].ItemCount != 1 {
		t.Fatalf("61+ day items = %d, want 1", result.AgingWIP.Buckets[4].ItemCount)
	}
	if len(result.Health.AttentionItems) == 0 || result.Health.AttentionItems[0].ID != oldItem {
		t.Fatalf("top attention item = %+v, want item %d", result.Health.AttentionItems, oldItem)
	}
}

func TestAnalyticsHealthUsesConfiguredWorkItemStalenessThreshold(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	itemID := fixture.insertItem(
		t, 1, "Threshold-sensitive work", fixture.openStatus, analyticsTestDate("2026-03-01"),
	)
	if _, err := fixture.db.ExecWrite(`
		UPDATE items SET last_active_at = '2026-03-12' WHERE id = ?
	`, itemID); err != nil {
		t.Fatalf("configure item activity: %v", err)
	}
	fixture.service.now = func() time.Time { return analyticsTestDate("2026-04-01") }

	getHealth := func() WorkHealthResult {
		t.Helper()
		result, err := fixture.service.GetAnalytics(ResolveDatasetParams{
			WorkspaceID: fixture.workspaceID,
			StartDate:   analyticsTestDate("2026-03-01"),
			EndDate:     analyticsTestDate("2026-04-01"),
		})
		if err != nil {
			t.Fatalf("GetAnalytics: %v", err)
		}
		return result.Health
	}

	defaultHealth := getHealth()
	if defaultHealth.StaleAfterDays != 30 || defaultHealth.Stale != 0 {
		t.Fatalf("default health = %+v, want threshold 30 and no stale items", defaultHealth)
	}

	if _, err := NewWorkItemStalenessService(fixture.db).Update(14); err != nil {
		t.Fatalf("Update staleness threshold: %v", err)
	}
	configuredHealth := getHealth()
	if configuredHealth.StaleAfterDays != 14 || configuredHealth.Stale != 1 {
		t.Fatalf("configured health = %+v, want threshold 14 and one stale item", configuredHealth)
	}
}

func TestAnalyticsReportsMissingCompletionHistoryHonestly(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	fixture.insertItem(
		t, 1, "Imported completed item", fixture.doneStatus, analyticsTestDate("2026-01-01"),
	)
	fixture.service.now = func() time.Time { return analyticsTestDate("2026-04-01") }

	result, err := fixture.service.GetAnalytics(ResolveDatasetParams{
		WorkspaceID: fixture.workspaceID,
		StartDate:   analyticsTestDate("2026-01-01"),
		EndDate:     analyticsTestDate("2026-04-01"),
	})
	if err != nil {
		t.Fatalf("GetAnalytics: %v", err)
	}

	if result.DeliveryTime.TotalItemsAnalyzed != 0 {
		t.Fatalf("delivery samples = %d, want 0", result.DeliveryTime.TotalItemsAnalyzed)
	}
	if result.DeliveryTime.MissingHistoryItems != 1 {
		t.Fatalf("missing history = %d, want 1", result.DeliveryTime.MissingHistoryItems)
	}
	if result.DeliveryTime.DataQuality.Reason != "no_completed_items" {
		t.Fatalf("data quality reason = %q", result.DeliveryTime.DataQuality.Reason)
	}
}

func TestAnalyticsBatchesDatasetsBeyondParameterLimitChunk(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	createdAt := analyticsTestDate("2026-01-01")
	itemCount := analyticsIDBatchSize + 5
	for number := 1; number <= itemCount; number++ {
		fixture.insertItem(t, number, "Batched item", fixture.openStatus, createdAt)
	}
	fixture.service.now = func() time.Time { return analyticsTestDate("2026-04-01") }

	result, err := fixture.service.GetAnalytics(ResolveDatasetParams{
		WorkspaceID: fixture.workspaceID,
		StartDate:   analyticsTestDate("2026-01-01"),
		EndDate:     analyticsTestDate("2026-04-01"),
	})
	if err != nil {
		t.Fatalf("GetAnalytics for %d items: %v", itemCount, err)
	}
	if result.Dataset.TotalItems != itemCount {
		t.Fatalf("dataset total = %d, want %d", result.Dataset.TotalItems, itemCount)
	}
	if result.Health.UnfinishedItems != itemCount {
		t.Fatalf("unfinished total = %d, want %d", result.Health.UnfinishedItems, itemCount)
	}
}

func TestAnalyticsServiceRejectsUnboundedRange(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	_, err := fixture.service.GetAnalytics(ResolveDatasetParams{
		WorkspaceID: fixture.workspaceID,
		StartDate:   analyticsTestDate("2025-01-01"),
		EndDate:     analyticsTestDate("2026-01-02"),
	})
	if err == nil {
		t.Fatal("GetAnalytics accepted a range longer than 366 days")
	}
}

func TestAnalyticsServiceHonorsCanceledRequest(t *testing.T) {
	fixture := newAnalyticsDeliveryFixture(t)
	fixture.insertItem(
		t, 1, "Canceled request", fixture.openStatus, analyticsTestDate("2026-01-01"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fixture.service.GetAnalyticsContext(ctx, ResolveDatasetParams{
		WorkspaceID: fixture.workspaceID,
		StartDate:   analyticsTestDate("2026-01-01"),
		EndDate:     analyticsTestDate("2026-02-01"),
	})
	if err == nil {
		t.Fatal("GetAnalyticsContext ignored a canceled request")
	}
}
