//go:build test

package scheduler

import (
	"context"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type availableBriefingClient struct{}

func (availableBriefingClient) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return &llm.CompletionResponse{
		Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "briefing body"}}},
	}, nil
}

func (availableBriefingClient) Health(context.Context) error { return nil }
func (availableBriefingClient) Available() bool              { return true }

func newBriefingSchedulerForTest(t *testing.T, fallback llm.Client) (*BriefingScheduler, database.Database) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	permissions, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL:             time.Minute,
		MaxCacheSize:    32,
		WarmupOnStartup: false,
		PreWarmActive:   false,
		BatchSize:       10,
	})
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })

	scheduler := NewBriefingScheduler(
		db,
		llm.NewConnectionManager(db, nil, fallback),
		permissions,
		services.NewTimePermissionService(db, permissions),
		services.NewUserReadService(db),
		llm.NewPromptStore(""),
	)
	return scheduler, db
}

func recentBriefingRuns(t *testing.T, db database.Database) []*models.SchedulerRun {
	t.Helper()
	runs, err := repository.NewSchedulerRunRepository(db).GetRecent(repository.RecentSchedulerRunsOpts{Scheduler: "briefing"})
	if err != nil {
		t.Fatalf("get recent scheduler runs: %v", err)
	}
	return runs
}

// TestBriefingSchedulerSkipsRunRecordWhenNoProviderAvailable pins that an idle
// tick caused by a missing AI provider does not leave a successful briefing run
// behind, which would make the diagnostics view report a generation that never
// happened.
func TestBriefingSchedulerSkipsRunRecordWhenNoProviderAvailable(t *testing.T) {
	scheduler, db := newBriefingSchedulerForTest(t, nil)

	scheduler.generateAllBriefings(make(chan struct{}))

	if runs := recentBriefingRuns(t, db); len(runs) != 0 {
		t.Fatalf("recorded %d briefing runs, want 0: %+v", len(runs), runs)
	}
}

// TestBriefingSchedulerRecordsRunWhenProviderAvailable guards the normal path:
// with a usable provider the tick is recorded.
func TestBriefingSchedulerRecordsRunWhenProviderAvailable(t *testing.T) {
	scheduler, db := newBriefingSchedulerForTest(t, availableBriefingClient{})

	scheduler.generateAllBriefings(make(chan struct{}))

	runs := recentBriefingRuns(t, db)
	if len(runs) != 1 {
		t.Fatalf("recorded %d briefing runs, want 1", len(runs))
	}
	if !runs[0].Success {
		t.Fatalf("briefing run success = false, error = %q", runs[0].ErrorMessage)
	}
	if runs[0].ItemsProcessed == nil || *runs[0].ItemsProcessed != 1 {
		t.Fatalf("items processed = %v, want 1", runs[0].ItemsProcessed)
	}
}
