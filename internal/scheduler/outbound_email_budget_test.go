//go:build test

package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/testutils"
)

type blockingReplyOutbox struct {
	canceled atomic.Bool
}

func (b *blockingReplyOutbox) ProcessPendingReplies(ctx context.Context, _ int) (int, error) {
	select {
	case <-ctx.Done():
		b.canceled.Store(true)
		return 0, ctx.Err()
	case <-time.After(10 * time.Second):
		return 0, nil
	}
}

func TestOutboundEmailSchedulerHonorsDrainBudget(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	scheduler := NewOutboundEmailScheduler(tdb.GetDatabase(), configuredSMTPSender{}, time.Minute, 10)
	scheduler.drainBudget = 100 * time.Millisecond
	outbox := &blockingReplyOutbox{}
	scheduler.SetEmailReplyOutbox(outbox)

	start := time.Now()
	scheduler.processOutboundEmails(context.Background())
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("drain took %v with a 100ms budget", elapsed)
	}
	if !outbox.canceled.Load() {
		t.Fatal("outbox send did not observe the drain deadline")
	}
}
