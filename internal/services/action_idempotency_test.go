//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
)

func TestDurableActionIdempotencyKeyIsStablePerNode(t *testing.T) {
	ctx := &models.ExecutionContext{
		DurableEventKey: "event-42",
		Action:          &models.Action{ID: 7},
	}
	first := durableActionIdempotencyKey(ctx, 11)
	second := durableActionIdempotencyKey(ctx, 11)
	otherNode := durableActionIdempotencyKey(ctx, 12)
	if first == "" || first != second {
		t.Fatalf("same durable target keys = %q and %q, want one stable non-empty key", first, second)
	}
	if first == otherNode {
		t.Fatalf("different nodes shared idempotency key %q", first)
	}
	ctx.DurableEventKey = ""
	if got := durableActionIdempotencyKey(ctx, 11); got != "" {
		t.Fatalf("non-durable action idempotency key = %q, want empty", got)
	}
}
