//go:build test

package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"windshift/internal/models"
)

type configuredSMTPSender struct{}

func (configuredSMTPSender) SendBatchedNotificationsContext(context.Context, string, string, []models.Notification) error {
	return nil
}

func (configuredSMTPSender) IsSMTPConfigured() bool { return true }

// fakeReplyOutbox delivers up to `limit` of its pending rows per call and
// records how many rows each call was offered.
type fakeReplyOutbox struct {
	mu      sync.Mutex
	pending int
	calls   int
	limits  []int
	err     error
}

func (f *fakeReplyOutbox) ProcessPendingReplies(_ context.Context, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.limits = append(f.limits, limit)
	n := min(f.pending, limit)
	f.pending -= n
	return n, f.err
}

func (f *fakeReplyOutbox) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeReplyOutbox) pendingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending
}

// fakeMentionOutbox is the mention-queue equivalent.
type fakeMentionOutbox struct {
	mu      sync.Mutex
	pending int
	calls   int
}

func (f *fakeMentionOutbox) ProcessPendingMentions(_ context.Context, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	n := min(f.pending, limit)
	f.pending -= n
	return n, nil
}

func (f *fakeMentionOutbox) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestOutboundEmailSchedulerDrainsMultipleBatchesPerTick verifies the
// throughput fix: one tick keeps draining until the queue is empty instead of
// stopping after a single batch.
func TestOutboundEmailSchedulerDrainsMultipleBatchesPerTick(t *testing.T) {
	db := newSchedulerRunsDB(t)
	reply := &fakeReplyOutbox{pending: 250}
	os := NewOutboundEmailScheduler(db, configuredSMTPSender{}, time.Minute, 100)
	os.SetEmailReplyOutbox(reply)

	os.processOutboundEmails(context.Background())

	if reply.callCount() != 3 {
		t.Fatalf("reply outbox calls = %d, want 3 (100+100+50)", reply.callCount())
	}
	if reply.pending != 0 {
		t.Fatalf("pending = %d, want 0", reply.pending)
	}

	var processed int
	if err := db.QueryRow(
		`SELECT items_processed FROM scheduler_runs WHERE scheduler_name = ? ORDER BY id DESC LIMIT 1`,
		outboundEmailSchedulerName,
	).Scan(&processed); err != nil {
		t.Fatalf("load scheduler run: %v", err)
	}
	if processed != 250 {
		t.Fatalf("items_processed = %d, want 250", processed)
	}
}

// TestOutboundEmailSchedulerRespectsConfiguredBatchSize pins the configurable
// per-pass limit.
func TestOutboundEmailSchedulerRespectsConfiguredBatchSize(t *testing.T) {
	db := newSchedulerRunsDB(t)
	reply := &fakeReplyOutbox{pending: 25}
	os := NewOutboundEmailScheduler(db, configuredSMTPSender{}, time.Minute, 10)
	os.SetEmailReplyOutbox(reply)

	os.processOutboundEmails(context.Background())

	if reply.callCount() != 3 {
		t.Fatalf("reply outbox calls = %d, want 3 (10+10+5)", reply.callCount())
	}
	for i, limit := range reply.limits {
		if limit != 10 {
			t.Fatalf("call %d limit = %d, want 10", i, limit)
		}
	}
}

// TestOutboundEmailSchedulerDrainsBothQueues verifies mentions drain in the
// same tick as replies.
func TestOutboundEmailSchedulerDrainsBothQueues(t *testing.T) {
	db := newSchedulerRunsDB(t)
	reply := &fakeReplyOutbox{pending: 120}
	mention := &fakeMentionOutbox{pending: 130}
	os := NewOutboundEmailScheduler(db, configuredSMTPSender{}, time.Minute, 100)
	os.SetEmailReplyOutbox(reply)
	os.SetMentionEmailOutbox(mention)

	os.processOutboundEmails(context.Background())

	if reply.pending != 0 || mention.pending != 0 {
		t.Fatalf("pending = (reply %d, mention %d), want both 0", reply.pending, mention.pending)
	}
	if reply.callCount() != 2 || mention.callCount() != 2 {
		t.Fatalf("calls = (reply %d, mention %d), want both 2", reply.callCount(), mention.callCount())
	}
}

// TestOutboundEmailSchedulerStopsAtDrainBudget guards against an unbounded
// hot loop when a queue never reports fewer than one full batch.
func TestOutboundEmailSchedulerStopsAtDrainBudget(t *testing.T) {
	db := newSchedulerRunsDB(t)
	reply := &fakeReplyOutbox{pending: 1_000_000}
	os := NewOutboundEmailScheduler(db, configuredSMTPSender{}, time.Minute, 100)
	os.SetEmailReplyOutbox(reply)

	// The first now() call is the tick start, the second is the first loop
	// check (still inside the budget), and every later call is past it.
	base := time.Unix(1_700_000_000, 0)
	calls := 0
	os.now = func() time.Time {
		calls++
		if calls <= 2 {
			return base
		}
		return base.Add(os.drainBudget + time.Second)
	}

	os.processOutboundEmails(context.Background())

	if reply.callCount() != 1 {
		t.Fatalf("reply outbox calls = %d, want 1 before the budget expires", reply.callCount())
	}
}

// TestOutboundEmailSchedulerRecordsFailureAndKeepsDraining verifies one failed
// batch does not abort the drain of later batches.
func TestOutboundEmailSchedulerRecordsFailureAndKeepsDraining(t *testing.T) {
	db := newSchedulerRunsDB(t)
	reply := &fakeReplyOutbox{pending: 150, err: errors.New("synthetic SMTP failure")}
	os := NewOutboundEmailScheduler(db, configuredSMTPSender{}, time.Minute, 100)
	os.SetEmailReplyOutbox(reply)

	os.processOutboundEmails(context.Background())

	// The fake reports a full batch worth of work on the first call even though
	// it errors, so the loop continues; the second call drains the rest.
	if reply.callCount() < 2 {
		t.Fatalf("reply outbox calls = %d, want >= 2", reply.callCount())
	}

	var success bool
	if err := db.QueryRow(
		`SELECT success FROM scheduler_runs WHERE scheduler_name = ? ORDER BY id DESC LIMIT 1`,
		outboundEmailSchedulerName,
	).Scan(&success); err != nil {
		t.Fatalf("load scheduler run: %v", err)
	}
	if success {
		t.Fatal("scheduler run recorded success despite an SMTP failure")
	}
}

// TestOutboundEmailSchedulerSkipsWhenSMTPUnconfigured keeps the drain a no-op
// (and avoids querying the outbox) when SMTP is not configured.
func TestOutboundEmailSchedulerSkipsWhenSMTPUnconfigured(t *testing.T) {
	db := newSchedulerRunsDB(t)
	reply := &fakeReplyOutbox{pending: 10}
	os := NewOutboundEmailScheduler(db, noopSMTPSender{}, time.Minute, 100)
	os.SetEmailReplyOutbox(reply)

	os.processOutboundEmails(context.Background())

	if reply.callCount() != 0 {
		t.Fatalf("reply outbox calls = %d, want 0 when SMTP is unconfigured", reply.callCount())
	}
}

// TestNewOutboundEmailSchedulerDefaults pins the fallback behavior for a zero
// interval/batch size.
func TestNewOutboundEmailSchedulerDefaults(t *testing.T) {
	os := NewOutboundEmailScheduler(nil, configuredSMTPSender{}, 0, 0)
	if os.interval != defaultOutboxInterval {
		t.Fatalf("interval = %s, want %s", os.interval, defaultOutboxInterval)
	}
	if os.batchSize != defaultOutboxBatchSize {
		t.Fatalf("batch size = %d, want %d", os.batchSize, defaultOutboxBatchSize)
	}
}

// TestOutboundEmailSchedulerCanRestartAfterStop mirrors the notification
// scheduler lifecycle contract.
func TestOutboundEmailSchedulerCanRestartAfterStop(t *testing.T) {
	os := NewOutboundEmailScheduler(nil, noopSMTPSender{}, time.Hour, 10)
	if os.ticker != nil {
		t.Fatal("constructor should not start the ticker before Start")
	}

	os.Start()
	os.Stop()
	if os.ticker != nil {
		t.Fatal("Stop should clear the stopped ticker")
	}

	os.Start()
	os.Stop()
}

// TestOutboundEmailSchedulerFiresOnTick proves the ticker actually drains the
// outbox at the configured cadence.
func TestOutboundEmailSchedulerFiresOnTick(t *testing.T) {
	reply := &fakeReplyOutbox{pending: 1}
	os := NewOutboundEmailScheduler(nil, configuredSMTPSender{}, 5*time.Millisecond, 10)
	os.runRepo = nil // no DB in this lifecycle test
	os.SetEmailReplyOutbox(reply)

	os.Start()
	defer os.Stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for time.Now().Before(deadline) && reply.pendingCount() != 0 {
		<-tick.C
	}
	if reply.pendingCount() != 0 {
		t.Fatalf("pending = %d, want 0 after a tick", reply.pendingCount())
	}
}
