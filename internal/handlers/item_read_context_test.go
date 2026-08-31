//go:build test

package handlers

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRespondItemReadErrorDoesNotWriteAfterClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/api/items", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()

	new(ItemHandler).respondItemReadError(recorder, req, fmt.Errorf("list items: %w", context.Canceled))

	if recorder.Body.Len() != 0 {
		t.Fatalf("response body = %q, want no write after cancellation", recorder.Body.String())
	}
	if recorder.Code != 200 {
		t.Fatalf("response status = %d, want untouched recorder status 200", recorder.Code)
	}
}

func TestRespondItemReadErrorContextMapsDriverCancellationToGatewayTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	req := httptest.NewRequest("GET", "/api/items", nil)
	recorder := httptest.NewRecorder()

	new(ItemHandler).respondItemReadErrorContext(ctx, recorder, req, fmt.Errorf("failed to query items: pq: canceling statement due to user request"))

	if recorder.Code != 504 {
		t.Fatalf("response status = %d, want 504", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "DATABASE_DEADLINE_EXCEEDED") {
		t.Fatalf("response body = %q, want database deadline code", recorder.Body.String())
	}
}
