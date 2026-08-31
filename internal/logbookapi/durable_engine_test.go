//go:build test

package logbookapi

import (
	"context"
	"testing"
	"time"

	"windshift/internal/logbook"
	"windshift/internal/testutils"
)

func TestServerStartsSharedLogbookEventEngineAndStopsWithinContext(t *testing.T) {
	if !testutils.IsPostgres() {
		t.Skip("logbook sidecar uses PostgreSQL")
	}
	tdb := testutils.CreateTestDB(t, true)
	server, err := NewServer(tdb.GetDatabase(), ServerConfig{
		StoragePath: t.TempDir(), MainServerSecret: "durable-logbook-test-secret",
		ActivateDurableActions: true,
	}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	var consumers int
	if err := tdb.QueryRow(`
		SELECT COUNT(*) FROM domain_event_consumers
		WHERE consumer_key IN (?, ?) AND is_active = true
	`, logbook.DurableLogbookCompatibilityConsumerKey, logbook.DurableLogbookActionConsumerKey).Scan(&consumers); err != nil {
		t.Fatalf("count active logbook consumers: %v", err)
	}
	if consumers != 2 {
		t.Fatalf("active logbook consumers = %d, want 2", consumers)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
