//go:build test

package repository

import (
	"context"
	"testing"
	"time"

	"windshift/internal/testutils"
)

func statusID(id int) *int { return &id }

func TestAccumulateStatusDurationsUsesCreationAsBaselineAndAggregatesReentry(t *testing.T) {
	createdAt := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	calculatedAt := createdAt.Add(6 * time.Hour)
	transitions := []itemStatusTransition{
		{newStatusID: statusID(1), changedAt: createdAt},
		{oldStatusID: statusID(1), newStatusID: statusID(2), changedAt: createdAt.Add(time.Hour)},
		{oldStatusID: statusID(2), newStatusID: statusID(1), changedAt: createdAt.Add(3 * time.Hour)},
		{oldStatusID: statusID(1), newStatusID: statusID(2), changedAt: createdAt.Add(4 * time.Hour)},
	}

	got := accumulateStatusDurations(createdAt, statusID(2), transitions, map[int]string{
		1: "Open",
		2: "In Progress",
	}, calculatedAt)

	if len(got.Statuses) != 2 {
		t.Fatalf("expected 2 aggregated statuses, got %d", len(got.Statuses))
	}
	open := got.Statuses[0]
	if open.StatusID != 1 || open.StatusName != "Open" || open.DurationSeconds != int64((2*time.Hour)/time.Second) {
		t.Fatalf("unexpected Open duration: %#v", open)
	}
	if !open.FirstEnteredAt.Equal(createdAt) || !open.LastEnteredAt.Equal(createdAt.Add(3*time.Hour)) {
		t.Fatalf("unexpected Open entry timestamps: %#v", open)
	}
	if open.IsCurrent {
		t.Fatal("Open must not be marked current")
	}
	inProgress := got.Statuses[1]
	if inProgress.StatusID != 2 || inProgress.DurationSeconds != int64((4*time.Hour)/time.Second) || !inProgress.IsCurrent {
		t.Fatalf("unexpected In Progress duration: %#v", inProgress)
	}
}

func TestGetStatusDurationsWithoutHistoryCountsFromCreation(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	testData := setupRepositoryTestData(t, tdb)
	createdAt := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	calculatedAt := createdAt.Add(9 * time.Hour)
	if _, err := tdb.DB.Exec("UPDATE items SET created_at = ?, updated_at = ? WHERE id = ?", createdAt, createdAt, testData.ItemID); err != nil {
		t.Fatalf("set deterministic item timestamps: %v", err)
	}

	got, err := NewItemRepository(tdb.GetDatabase()).GetStatusDurations(context.Background(), testData.ItemID, calculatedAt)
	if err != nil {
		t.Fatalf("GetStatusDurations: %v", err)
	}
	if len(got.Statuses) != 1 {
		t.Fatalf("expected current status only, got %#v", got.Statuses)
	}
	status := got.Statuses[0]
	if status.StatusID != testData.StatusID || status.StatusName == "" || !status.IsCurrent {
		t.Fatalf("unexpected current status: %#v", status)
	}
	if status.DurationSeconds != int64((9*time.Hour)/time.Second) {
		t.Fatalf("expected duration from creation, got %d seconds", status.DurationSeconds)
	}
}
