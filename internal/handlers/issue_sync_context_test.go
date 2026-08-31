//go:build test

package handlers

import (
	"context"
	"testing"
)

func TestIssueSyncContextSurvivesRequestCancellation(t *testing.T) {
	requestContext, cancelRequest := context.WithCancel(context.Background())
	context, cancelSync := issueSyncContext(requestContext)
	t.Cleanup(cancelSync)

	cancelRequest()
	if err := context.Err(); err != nil {
		t.Fatalf("background sync context inherited request cancellation: %v", err)
	}
	if _, ok := context.Deadline(); !ok {
		t.Fatal("background sync context has no deadline")
	}
}
