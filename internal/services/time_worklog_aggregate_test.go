//go:build test

package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestSplitWorklogMinutesByDay(t *testing.T) {
	_, utc, err := ResolveTimezone("UTC")
	if err != nil {
		t.Fatalf("resolve UTC: %v", err)
	}
	_, denver, err := ResolveTimezone("America/Denver")
	if err != nil {
		t.Fatalf("resolve Denver: %v", err)
	}
	_, newYork, err := ResolveTimezone("America/New_York")
	if err != nil {
		t.Fatalf("resolve New York: %v", err)
	}

	sum := func(slices []struct {
		Day     string
		Minutes int64
	}) int64 {
		var total int64
		for _, s := range slices {
			total += s.Minutes
		}
		return total
	}

	cases := []struct {
		name      string
		start     int64
		end       int64
		location  *time.Location
		wantDays  []string
		wantSum   int64
	}{
		{
			name:     "intraDaySingleSlice",
			start:    time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).Unix(),
			end:      time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC).Unix(),
			location: utc,
			wantDays: []string{"2026-09-01"},
			wantSum:  60,
		},
		{
			name:     "spansLocalMidnightSplitsIntoTwoDays",
			start:    time.Date(2026, 9, 1, 22, 0, 0, 0, time.UTC).Unix(),
			end:      time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC).Unix(),
			location: utc,
			wantDays: []string{"2026-09-01", "2026-09-02"},
			wantSum:  240,
		},
		{
			name:     "endExactlyAtMidnightContributesNothingToNextDay",
			start:    time.Date(2026, 9, 1, 22, 0, 0, 0, time.UTC).Unix(),
			end:      time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC).Unix(),
			location: utc,
			wantDays: []string{"2026-09-01"},
			wantSum:  120,
		},
		{
			name:     "timezoneShiftsTheDayBoundary",
			start:    time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC).Unix(),
			end:      time.Date(2026, 9, 2, 4, 0, 0, 0, time.UTC).Unix(),
			location: denver, // local 14:00 -> 22:00, same civil day
			wantDays: []string{"2026-09-01"},
			wantSum:  480,
		},
		{
			name:     "dstSpringForwardKeepsElapsedMinutes",
			start:    time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC).Unix(), // 01:00 EST
			end:      time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC).Unix(), // 03:30 EDT
			location: newYork,
			wantDays: []string{"2026-03-08"},
			wantSum:  90,
		},
		{
			name:     "emptyWhenEndNotAfterStart",
			start:    1000,
			end:      1000,
			location: utc,
			wantDays: nil,
			wantSum:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slices := SplitWorklogMinutesByDay(tc.start, tc.end, tc.location)

			if got := sum(slices); got != tc.wantSum {
				t.Fatalf("summed minutes = %d, want %d (slices=%+v)", got, tc.wantSum, slices)
			}
			gotDays := make([]string, 0, len(slices))
			for _, s := range slices {
				gotDays = append(gotDays, s.Day)
			}
			if len(gotDays) != len(tc.wantDays) {
				t.Fatalf("days = %v, want %v", gotDays, tc.wantDays)
			}
			for i := range tc.wantDays {
				if gotDays[i] != tc.wantDays[i] {
					t.Fatalf("days = %v, want %v", gotDays, tc.wantDays)
				}
			}
		})
	}
}

func TestTimeWorklogService_Aggregate(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	if _, err := tdb.Exec(`
		INSERT INTO customer_organisations (id, name) VALUES (700, 'Aggregate Customer')
	`); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO time_projects (id, customer_id, name, status) VALUES (800, 700, 'Aggregate Project', 'Active')
	`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := tdb.Exec(`
		INSERT INTO time_projects (id, customer_id, name, status) VALUES (801, 700, 'Other Project', 'Active')
	`); err != nil {
		t.Fatalf("seed other project: %v", err)
	}
	insertWorklog := func(start, end int64, minutes, userID, projectID int) {
		t.Helper()
		if _, err := tdb.Exec(`
			INSERT INTO time_worklogs (project_id, customer_id, user_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
			VALUES (?, 700, ?, 'desc', ?, ?, ?, ?, 0, 0)
		`, projectID, userID, start, start, end, minutes); err != nil {
			t.Fatalf("seed worklog: %v", err)
		}
	}

	// Two entries for user 1 on separate days, one spanning midnight into the
	// second day, plus one entry on an inaccessible project that must be
	// excluded by the accessible-project scope.
	day1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	insertWorklog(day1.Unix(), day1.Add(60*time.Minute).Unix(), 60, data.UserID, 800)
	insertWorklog(day1.Add(24*time.Hour).Unix(), day1.Add(25*time.Hour).Unix(), 60, data.UserID, 800)
	// Crosses midnight 09-02 -> 09-03 (UTC).
	insertWorklog(day1.Add(36*time.Hour).Unix(), day1.Add(38*time.Hour).Unix(), 120, data.UserID, 800)
	insertWorklog(day1.Unix(), day1.Add(30*time.Minute).Unix(), 30, data.UserID, 801)

	service := NewTimeWorklogService(tdb.GetDatabase())
	aggregate, err := service.Aggregate(context.Background(), repository.WorklogDetailFilter{
		AccessibleProjectIDs: []int{800},
	}, "UTC")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(aggregate.Totals) != 1 {
		t.Fatalf("totals groups = %d, want 1: %+v", len(aggregate.Totals), aggregate.Totals)
	}
	total := aggregate.Totals[0]
	if total.DurationMinutes != 240 || total.Entries != 3 {
		t.Fatalf("totals = %+v, want 240 minutes across 3 entries", total)
	}
	if total.UserID != data.UserID || total.ProjectID != 800 || total.CustomerID != 700 {
		t.Fatalf("totals grouping = %+v", total)
	}

	// Daily: day1 60m; day2 60m + the full 120m of the entry that ends
	// exactly at the 09-03 midnight (half-open: it contributes nothing to
	// 09-03).
	byDay := map[string]int64{}
	for _, d := range aggregate.Daily {
		byDay[d.Day] += d.Minutes
	}
	want := map[string]int64{"2026-09-01": 60, "2026-09-02": 180}
	if len(byDay) != len(want) {
		t.Fatalf("daily days = %v, want %v", byDay, want)
	}
	for day, minutes := range want {
		if byDay[day] != minutes {
			t.Fatalf("day %s minutes = %d, want %d (all=%v)", day, byDay[day], minutes, byDay)
		}
	}
	var dailySum int64
	for _, d := range aggregate.Daily {
		dailySum += d.Minutes
	}
	if dailySum != 240 {
		t.Fatalf("daily minutes total = %d, want 240", dailySum)
	}

	t.Run("emptyScopeYieldsEmptyGroups", func(t *testing.T) {
		empty, err := service.Aggregate(context.Background(), repository.WorklogDetailFilter{
			AccessibleProjectIDs: []int{},
		}, "UTC")
		if err != nil {
			t.Fatalf("Aggregate empty scope: %v", err)
		}
		if empty.Daily == nil || empty.Totals == nil || len(empty.Daily) != 0 || len(empty.Totals) != 0 {
			t.Fatalf("empty scope aggregate = %+v", empty)
		}
	})

	t.Run("invalidTimezoneIsRejected", func(t *testing.T) {
		if _, err := service.Aggregate(context.Background(), repository.WorklogDetailFilter{
			AccessibleProjectIDs: []int{800},
		}, "Mars/Olympus"); err == nil {
			t.Fatal("expected invalid timezone error")
		}
	})

}

// TestTimeWorklogService_AggregateCancellationStopsTheScan proves a canceled
// context aborts the aggregation instead of running to completion
// (WI-1598).
func TestTimeWorklogService_AggregateCancellationStopsTheScan(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)

	if _, err := tdb.Exec(`INSERT INTO customer_organisations (id, name) VALUES (700, 'Aggregate Customer')`); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := tdb.Exec(`INSERT INTO time_projects (id, customer_id, name, status) VALUES (800, 700, 'Aggregate Project', 'Active')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if _, err := tdb.Exec(`
			INSERT INTO time_worklogs (project_id, customer_id, user_id, description, date, start_time, end_time, duration_minutes, created_at, updated_at)
			VALUES (800, 700, ?, 'desc', ?, ?, ?, 60, 0, 0)
		`, data.UserID, start.Unix(), start.Unix(), start.Add(time.Hour).Unix()); err != nil {
			t.Fatalf("seed worklog: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	// The first row cancels the stream: the reduction returns
	// context.Canceled and the service propagates it.
	cancel()
	service := NewTimeWorklogService(tdb.GetDatabase())
	_, err := service.Aggregate(ctx, repository.WorklogDetailFilter{
		AccessibleProjectIDs: []int{800},
	}, "UTC")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Aggregate err = %v, want context.Canceled", err)
	}
}
