package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
)

func testWriteBatcherConfig() WriteBatcherConfig {
	return WriteBatcherConfig{
		FlushInterval:       time.Hour,
		FlushTimeout:        time.Second,
		ShutdownTimeout:     time.Second,
		MaxBatchSize:        100,
		MaxPending:          1000,
		RetryInitialBackoff: 10 * time.Millisecond,
		RetryMaxBackoff:     10 * time.Millisecond,
		RetryJitter:         -1,
		Name:                "test",
	}
}

func elapseWriteBatcherBackoff[T any](wb *WriteBatcher[T]) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.retryAt = time.Time{}
	if wb.retryTimer != nil {
		wb.retryTimer.Stop()
		wb.retryTimer = nil
	}
}

func TestWriteBatcherBoundsPendingWorkAcrossFlushFailure(t *testing.T) {
	fail := true
	config := testWriteBatcherConfig()
	config.MaxPending = 3
	wb := NewWriteBatcher(config, func(context.Context, []int) error {
		if fail {
			return errors.New("database unavailable")
		}
		return nil
	})

	for i := 0; i < 3; i++ {
		if !wb.Add(i) {
			t.Fatalf("item %d unexpectedly rejected", i)
		}
	}
	if wb.Add(4) {
		t.Fatal("item beyond MaxPending was accepted")
	}
	if err := wb.Flush(); err == nil {
		t.Fatal("failed flush returned nil")
	}

	stats := wb.Stats()
	if stats.Pending != 3 || stats.HighWaterMark != 3 || stats.ItemsDropped != 1 || stats.RetryCount != 1 {
		t.Fatalf("stats after failure = %+v", stats)
	}

	fail = false
	elapseWriteBatcherBackoff(wb)
	if err := wb.Flush(); err != nil {
		t.Fatalf("recovery flush: %v", err)
	}
	if stats := wb.Stats(); stats.Pending != 0 || stats.ItemsFlushed != 3 {
		t.Fatalf("stats after recovery = %+v", stats)
	}
}

type countedUpdate struct {
	key   string
	count int
	when  time.Time
}

func TestWriteBatcherCoalescesStableKeysBeforeFlush(t *testing.T) {
	var flushed []countedUpdate
	config := testWriteBatcherConfig()
	wb := NewCoalescingWriteBatcher(
		config,
		func(update countedUpdate) string { return update.key },
		func(existing, incoming countedUpdate) countedUpdate {
			existing.count += incoming.count
			if incoming.when.After(existing.when) {
				existing.when = incoming.when
			}
			return existing
		},
		func(_ context.Context, updates []countedUpdate) error {
			flushed = append(flushed, updates...)
			return nil
		},
	)

	now := time.Now()
	for i := 0; i < 3; i++ {
		if !wb.Add(countedUpdate{key: "same", count: 1, when: now.Add(time.Duration(i) * time.Second)}) {
			t.Fatal("coalesced update rejected")
		}
	}
	if !wb.Add(countedUpdate{key: "other", count: 4, when: now}) {
		t.Fatal("second key rejected")
	}
	if err := wb.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if len(flushed) != 2 {
		t.Fatalf("flush received %d entries, want 2: %+v", len(flushed), flushed)
	}
	if flushed[0].key != "same" || flushed[0].count != 3 || !flushed[0].when.Equal(now.Add(2*time.Second)) {
		t.Fatalf("merged update = %+v", flushed[0])
	}
	stats := wb.Stats()
	if stats.ItemsBuffered != 4 || stats.ItemsCoalesced != 2 || stats.ItemsFlushed != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestWriteBatcherFlushesControlledBatchSizes(t *testing.T) {
	config := testWriteBatcherConfig()
	config.MaxBatchSize = 2
	config.MaxPending = 5
	var sizes []int
	wb := NewWriteBatcher(config, func(_ context.Context, items []int) error {
		sizes = append(sizes, len(items))
		return nil
	})
	for i := 0; i < 5; i++ {
		if !wb.Add(i) {
			t.Fatalf("add %d rejected", i)
		}
	}
	for wb.Stats().Pending > 0 {
		if err := wb.Flush(); err != nil {
			t.Fatalf("flush: %v", err)
		}
	}
	if got, want := fmt.Sprint(sizes), "[2 2 1]"; got != want {
		t.Fatalf("batch sizes = %s, want %s", got, want)
	}
}

func TestWriteBatcherBacksOffAfterFailure(t *testing.T) {
	config := testWriteBatcherConfig()
	var attempts atomic.Int64
	wb := NewWriteBatcher(config, func(context.Context, []int) error {
		if attempts.Add(1) == 1 {
			return errors.New("database unavailable")
		}
		return nil
	})
	wb.Add(1)
	if err := wb.Flush(); err == nil {
		t.Fatal("first flush unexpectedly succeeded")
	}
	if err := wb.Flush(); !errors.Is(err, ErrWriteBatcherBackoff) {
		t.Fatalf("immediate retry = %v, want backoff", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts during backoff = %d, want 1", attempts.Load())
	}
	elapseWriteBatcherBackoff(wb)
	if err := wb.Flush(); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
}

func TestWriteBatcherRetryBackoffGrowsAndCaps(t *testing.T) {
	config := testWriteBatcherConfig()
	config.RetryInitialBackoff = 10 * time.Millisecond
	config.RetryMaxBackoff = 40 * time.Millisecond
	wb := NewWriteBatcher(config, func(context.Context, []int) error { return nil })

	wb.failures = 1
	if delay := wb.retryDelayLocked(); delay != 10*time.Millisecond {
		t.Fatalf("first delay = %v", delay)
	}
	wb.failures = 2
	if delay := wb.retryDelayLocked(); delay != 20*time.Millisecond {
		t.Fatalf("second delay = %v", delay)
	}
	wb.failures = 3
	if delay := wb.retryDelayLocked(); delay != 40*time.Millisecond {
		t.Fatalf("third delay = %v", delay)
	}
	wb.failures = 8
	if delay := wb.retryDelayLocked(); delay != 40*time.Millisecond {
		t.Fatalf("capped delay = %v", delay)
	}
}

func TestWriteBatcherAutomaticallyRecoversWithinBound(t *testing.T) {
	config := testWriteBatcherConfig()
	config.FlushInterval = time.Hour
	config.MaxBatchSize = 4
	config.MaxPending = 16
	var attempts atomic.Int64
	recovered := make(chan struct{})
	var recoveredOnce sync.Once
	wb := NewWriteBatcher(config, func(context.Context, []int) error {
		if attempts.Add(1) <= 2 {
			return errors.New("database unavailable")
		}
		recoveredOnce.Do(func() { close(recovered) })
		return nil
	})
	wb.Start()
	for i := 0; i < 100; i++ {
		wb.Add(i)
	}

	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("write batcher did not recover after scheduled retries")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := wb.StopContext(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	stats := wb.Stats()
	if stats.Pending != 0 || stats.HighWaterMark > int64(config.MaxPending) || stats.ItemsDropped == 0 {
		t.Fatalf("recovery stats = %+v", stats)
	}
}

func TestWriteBatcherMergesUpdatesThatArriveDuringFailedFlush(t *testing.T) {
	config := testWriteBatcherConfig()
	flushStarted := make(chan struct{})
	releaseFailure := make(chan struct{})
	var attempts atomic.Int64
	var recovered []countedUpdate
	wb := NewCoalescingWriteBatcher(
		config,
		func(update countedUpdate) string { return update.key },
		func(existing, incoming countedUpdate) countedUpdate {
			existing.count += incoming.count
			if incoming.when.After(existing.when) {
				existing.when = incoming.when
			}
			return existing
		},
		func(_ context.Context, updates []countedUpdate) error {
			if attempts.Add(1) == 1 {
				close(flushStarted)
				<-releaseFailure
				return errors.New("database unavailable")
			}
			recovered = append(recovered, updates...)
			return nil
		},
	)

	now := time.Now()
	wb.Add(countedUpdate{key: "same", count: 1, when: now})
	flushDone := make(chan error, 1)
	go func() { flushDone <- wb.Flush() }()
	<-flushStarted
	wb.Add(countedUpdate{key: "same", count: 2, when: now.Add(time.Second)})
	close(releaseFailure)
	if err := <-flushDone; err == nil {
		t.Fatal("failed flush returned nil")
	}
	elapseWriteBatcherBackoff(wb)
	if err := wb.Flush(); err != nil {
		t.Fatalf("recovery flush: %v", err)
	}
	if len(recovered) != 1 || recovered[0].count != 3 || !recovered[0].when.Equal(now.Add(time.Second)) {
		t.Fatalf("recovered updates = %+v", recovered)
	}
}

func TestWriteBatcherExpiresStaleWorkAndAdmitsFreshWork(t *testing.T) {
	config := testWriteBatcherConfig()
	config.MaxPending = 1
	config.MaxRetryAge = 5 * time.Millisecond
	wb := NewWriteBatcher(config, func(context.Context, []int) error { return nil })
	if !wb.Add(1) {
		t.Fatal("first item rejected")
	}
	wb.mu.Lock()
	wb.buffer[0].lastUpdatedAt = time.Now().Add(-config.MaxRetryAge - time.Second)
	wb.mu.Unlock()
	if !wb.Add(2) {
		t.Fatal("fresh item rejected after stale item should expire")
	}
	stats := wb.Stats()
	if stats.Pending != 1 || stats.ItemsExpired != 1 || stats.ItemsDropped != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestWriteBatcherStopHonorsContextDeadline(t *testing.T) {
	config := testWriteBatcherConfig()
	wb := NewWriteBatcher(config, func(ctx context.Context, _ []int) error {
		<-ctx.Done()
		return ctx.Err()
	})
	wb.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err := wb.StopContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopContext error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
		t.Fatalf("StopContext took %v", elapsed)
	}
	if stats := wb.Stats(); stats.Pending != 1 {
		t.Fatalf("pending after canceled shutdown = %d, want 1", stats.Pending)
	}
}

func TestWriteBatcherConcurrentAddFlushStop(t *testing.T) {
	config := testWriteBatcherConfig()
	config.MaxBatchSize = 8
	config.MaxPending = 64
	wb := NewCoalescingWriteBatcher(
		config,
		func(value int) string { return fmt.Sprint(value % 50) },
		func(_ int, incoming int) int { return incoming },
		func(context.Context, []int) error { return nil },
	)
	wb.Start()

	start := make(chan struct{})
	started := make(chan struct{}, 20)
	release := make(chan struct{})
	var producers sync.WaitGroup
	for producer := 0; producer < 20; producer++ {
		producers.Add(1)
		go func(base int) {
			defer producers.Done()
			<-start
			wb.Add(base * 200)
			started <- struct{}{}
			<-release
			for i := 1; i < 200; i++ {
				wb.Add(base*200 + i)
				if i%25 == 0 {
					_ = wb.Flush()
				}
			}
		}(producer)
	}
	close(start)
	for range 20 {
		<-started
	}

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stopDone <- wb.StopContext(ctx)
	}()
	close(release)
	producers.Wait()
	if err := <-stopDone; err != nil {
		t.Fatalf("stop: %v", err)
	}
	stats := wb.Stats()
	if stats.Pending != 0 || stats.HighWaterMark > int64(config.MaxPending) {
		t.Fatalf("stats after concurrent use = %+v", stats)
	}
}

func TestActivityTrackerBoundsQueueAndShadowMapsDuringDatabaseOutage(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "activity-outage.db"))
	if err != nil {
		t.Fatalf("new SQLite database: %v", err)
	}
	tracker, err := NewActivityTracker(db, DefaultActivityTrackerConfig())
	if err != nil {
		t.Fatalf("new activity tracker: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close SQLite database: %v", err)
	}

	var producers sync.WaitGroup
	for producer := 0; producer < 10; producer++ {
		producers.Add(1)
		go func(base int) {
			defer producers.Done()
			for i := 0; i < 500; i++ {
				id := base*500 + i + 1
				_ = tracker.TrackWorkspaceVisit(1, id)
				_ = tracker.TrackItemActivity(1, id, ActivityView)
			}
		}(producer)
	}
	producers.Wait()

	visitStats := tracker.visitBatcher.Stats()
	activityStats := tracker.activityBatcher.Stats()
	tracker.pendingMu.RLock()
	pendingVisits := len(tracker.pendingWorkspaceVisits)
	pendingActivities := len(tracker.pendingItemActivities)
	tracker.pendingMu.RUnlock()
	maxRetained := visitStats.MaxPending + tracker.visitBatcher.config.MaxBatchSize
	if visitStats.Pending > visitStats.MaxPending || pendingVisits > maxRetained || visitStats.ItemsDropped == 0 {
		t.Fatalf("workspace outage bounds: batcher=%+v shadow=%d max=%d", visitStats, pendingVisits, maxRetained)
	}
	maxRetained = activityStats.MaxPending + tracker.activityBatcher.config.MaxBatchSize
	if activityStats.Pending > activityStats.MaxPending || pendingActivities > maxRetained || activityStats.ItemsDropped == 0 {
		t.Fatalf("activity outage bounds: batcher=%+v shadow=%d max=%d", activityStats, pendingActivities, maxRetained)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tracker.visitBatcher.StopContext(ctx)
	_ = tracker.activityBatcher.StopContext(ctx)
	_ = tracker.cache.Close()
}

func TestTokenTrackerFlushesOneUpdatePerUniqueToken(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "token-coalescing.db"))
	if err != nil {
		t.Fatalf("new SQLite database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecWrite(`
		CREATE TABLE api_tokens (
			id INTEGER PRIMARY KEY,
			last_used_at DATETIME,
			updated_at DATETIME
		)
	`); err != nil {
		t.Fatalf("create token table: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO api_tokens (id) VALUES (1), (2)`); err != nil {
		t.Fatalf("insert tokens: %v", err)
	}

	tracker := NewTokenTracker(db, DefaultTokenTrackerConfig())
	for i := 0; i < 100; i++ {
		tracker.RecordTokenUse(1)
		tracker.RecordTokenUse(2)
	}
	if stats := tracker.batcher.Stats(); stats.Pending != 2 || stats.ItemsCoalesced != 198 {
		t.Fatalf("pre-flush stats = %+v", stats)
	}
	if err := tracker.FlushPendingUpdates(); err != nil {
		t.Fatalf("flush token updates: %v", err)
	}
	if stats := tracker.batcher.Stats(); stats.ItemsFlushed != 2 || stats.Pending != 0 {
		t.Fatalf("post-flush stats = %+v", stats)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close token tracker: %v", err)
	}
}

func TestActivityTrackerFlushesOneUpdatePerStableKey(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "activity-coalescing.db"))
	if err != nil {
		t.Fatalf("new SQLite database: %v", err)
	}
	defer func() { _ = db.Close() }()
	statements := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE workspaces (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE items (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE user_workspace_visits (
			user_id INTEGER NOT NULL,
			workspace_id INTEGER NOT NULL,
			last_visited_at DATETIME NOT NULL,
			visit_count INTEGER NOT NULL,
			expires_at DATETIME,
			updated_at DATETIME,
			PRIMARY KEY (user_id, workspace_id)
		)`,
		`CREATE TABLE user_item_activities (
			user_id INTEGER NOT NULL,
			item_id INTEGER NOT NULL,
			activity_type TEXT NOT NULL,
			last_activity_at DATETIME NOT NULL,
			activity_count INTEGER NOT NULL,
			expires_at DATETIME,
			updated_at DATETIME,
			PRIMARY KEY (user_id, item_id, activity_type)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecWrite(statement); err != nil {
			t.Fatalf("create activity table: %v", err)
		}
	}
	if _, err := db.ExecWrite(`INSERT INTO users (id) VALUES (1)`); err != nil {
		t.Fatalf("seed activity user: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO workspaces (id) VALUES (2)`); err != nil {
		t.Fatalf("seed activity workspace: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO items (id) VALUES (3)`); err != nil {
		t.Fatalf("seed activity item: %v", err)
	}

	tracker, err := NewActivityTracker(db, DefaultActivityTrackerConfig())
	if err != nil {
		t.Fatalf("new activity tracker: %v", err)
	}
	for i := 0; i < 100; i++ {
		if err := tracker.TrackWorkspaceVisit(1, 2); err != nil {
			t.Fatalf("track workspace visit: %v", err)
		}
		if err := tracker.TrackItemActivity(1, 3, ActivityView); err != nil {
			t.Fatalf("track item activity: %v", err)
		}
	}
	if stats := tracker.visitBatcher.Stats(); stats.Pending != 1 || stats.ItemsCoalesced != 99 {
		t.Fatalf("workspace pre-flush stats = %+v", stats)
	}
	if stats := tracker.activityBatcher.Stats(); stats.Pending != 1 || stats.ItemsCoalesced != 99 {
		t.Fatalf("activity pre-flush stats = %+v", stats)
	}
	pending, err := tracker.GetUserActivity(1)
	if err != nil {
		t.Fatalf("get pending activity: %v", err)
	}
	if len(pending.WorkspaceVisits) != 1 || pending.WorkspaceVisits[0].WorkspaceID != 2 || pending.WorkspaceVisits[0].VisitCount != 100 {
		t.Fatalf("pending workspace visits = %+v, want workspace 2 with 100 visits", pending.WorkspaceVisits)
	}
	if err := tracker.FlushPendingActivities(); err != nil {
		t.Fatalf("flush activity updates: %v", err)
	}
	var visitCount, activityCount int
	if err := db.QueryRow(`SELECT visit_count FROM user_workspace_visits WHERE user_id = 1 AND workspace_id = 2`).Scan(&visitCount); err != nil {
		t.Fatalf("query workspace visit count: %v", err)
	}
	if err := db.QueryRow(`SELECT activity_count FROM user_item_activities WHERE user_id = 1 AND item_id = 3 AND activity_type = 'view'`).Scan(&activityCount); err != nil {
		t.Fatalf("query item activity count: %v", err)
	}
	if visitCount != 100 || activityCount != 100 {
		t.Fatalf("persisted counts: visit=%d activity=%d", visitCount, activityCount)
	}
	if err := tracker.TrackItemActivity(1, 999, ActivityView); err != nil {
		t.Fatalf("track deleted item activity: %v", err)
	}
	if err := tracker.TrackWorkspaceVisit(1, 999); err != nil {
		t.Fatalf("track deleted workspace visit: %v", err)
	}
	if err := tracker.FlushPendingActivities(); err != nil {
		t.Fatalf("flush deleted entity activity: %v", err)
	}
	var staleItemCount, staleWorkspaceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_item_activities WHERE item_id = 999`).Scan(&staleItemCount); err != nil {
		t.Fatalf("query deleted item activity: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_workspace_visits WHERE workspace_id = 999`).Scan(&staleWorkspaceCount); err != nil {
		t.Fatalf("query deleted workspace visit: %v", err)
	}
	if staleItemCount != 0 || staleWorkspaceCount != 0 {
		t.Fatalf("persisted deleted entity activity: items=%d workspaces=%d", staleItemCount, staleWorkspaceCount)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close activity tracker: %v", err)
	}
}

func TestTrackerCoalescersPreserveLatestTimestampAndCounts(t *testing.T) {
	now := time.Now()
	token := mergeTokenUpdates(
		tokenUpdateEntry{TokenID: 1, LastUsedAt: now},
		tokenUpdateEntry{TokenID: 1, LastUsedAt: now.Add(time.Second)},
	)
	if !token.LastUsedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("token timestamp = %v", token.LastUsedAt)
	}

	visit := mergeWorkspaceVisits(
		WorkspaceVisit{UserID: 1, WorkspaceID: 2, VisitedAt: now, VisitCount: 2},
		WorkspaceVisit{UserID: 1, WorkspaceID: 2, VisitedAt: now.Add(time.Second), VisitCount: 3},
	)
	if visit.VisitCount != 5 || !visit.VisitedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("workspace visit = %+v", visit)
	}

	activity := mergeItemActivities(
		ItemActivity{UserID: 1, ItemID: 2, ActivityType: ActivityView, ActivityAt: now, ActivityCount: 4},
		ItemActivity{UserID: 1, ItemID: 2, ActivityType: ActivityView, ActivityAt: now.Add(time.Second), ActivityCount: 5},
	)
	if activity.ActivityCount != 9 || !activity.ActivityAt.Equal(now.Add(time.Second)) {
		t.Fatalf("item activity = %+v", activity)
	}
}
