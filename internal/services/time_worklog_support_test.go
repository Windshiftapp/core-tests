package services

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestParseWorklogTimesStrictClockAndDurationBounds(t *testing.T) {
	date := time.Date(2026, time.July, 14, 0, 0, 0, 0, time.UTC)

	for _, input := range []WorklogTimeInput{
		{StartTime: "25:00", EndTime: "26:00"},
		{StartTime: "12:60", EndTime: "13:00"},
		{DurationMinutes: MaxWorklogDurationMinutes + 1},
		{DurationMinutes: int(^uint(0) >> 1)},
	} {
		if _, _, _, err := ParseWorklogTimes(date, input); err == nil {
			t.Fatalf("ParseWorklogTimes(%+v) succeeded, want validation error", input)
		}
	}

	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{
		StartTime: "09:15",
		EndTime:   "10:45",
	})
	if err != nil {
		t.Fatalf("ParseWorklogTimes(valid): %v", err)
	}
	if minutes != 90 || end-start != int64(90*time.Minute/time.Second) {
		t.Fatalf("parsed result = (%d, %d seconds), want (90, 5400)", minutes, end-start)
	}
}

func TestParseWorklogTimesUsesCivilTimezoneAndStableDateKey(t *testing.T) {
	_, zurich, err := ResolveTimezone("Europe/Zurich")
	if err != nil {
		t.Fatalf("ResolveTimezone: %v", err)
	}
	date, err := ParseCivilDate("2026-07-14", zurich)
	if err != nil {
		t.Fatalf("ParseCivilDate: %v", err)
	}
	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{StartTime: "09:00", EndTime: "10:30"})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-07-14T07:00:00Z" {
		t.Fatalf("start UTC = %s, want 2026-07-14T07:00:00Z", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-07-14T08:30:00Z" {
		t.Fatalf("end UTC = %s, want 2026-07-14T08:30:00Z", got)
	}
	if minutes != 90 {
		t.Fatalf("duration = %d, want 90", minutes)
	}
	if got := time.Unix(WorklogDateUnix(date), 0).UTC().Format(time.RFC3339); got != "2026-07-14T00:00:00Z" {
		t.Fatalf("business date key = %s, want UTC midnight", got)
	}
}

func TestParseWorklogTimesDurationOnlyAnchorsAtCivilDateStart(t *testing.T) {
	_, zurich, err := ResolveTimezone("Europe/Zurich")
	if err != nil {
		t.Fatalf("ResolveTimezone: %v", err)
	}
	date, err := ParseCivilDate("2026-07-14", zurich)
	if err != nil {
		t.Fatalf("ParseCivilDate: %v", err)
	}

	minutes, start, end, err := ParseWorklogTimes(date, WorklogTimeInput{Duration: "90m"})
	if err != nil {
		t.Fatalf("ParseWorklogTimes: %v", err)
	}
	if minutes != 90 {
		t.Fatalf("duration = %d, want 90", minutes)
	}
	if got := time.Unix(start, 0).UTC().Format(time.RFC3339); got != "2026-07-13T22:00:00Z" {
		t.Fatalf("start = %s, want civil-date midnight", got)
	}
	if got := time.Unix(end, 0).UTC().Format(time.RFC3339); got != "2026-07-13T23:30:00Z" {
		t.Fatalf("end = %s, want 90 minutes after civil-date midnight", got)
	}
}

func TestParseWorklogTimesRejectsDSTGapAndFold(t *testing.T) {
	_, zurich, err := ResolveTimezone("Europe/Zurich")
	if err != nil {
		t.Fatalf("ResolveTimezone: %v", err)
	}
	for _, tc := range []struct {
		date  string
		clock string
		want  string
	}{
		{date: "2026-03-29", clock: "02:30", want: "does not exist"},
		{date: "2026-10-25", clock: "02:30", want: "ambiguous"},
	} {
		date, err := ParseCivilDate(tc.date, zurich)
		if err != nil {
			t.Fatalf("ParseCivilDate(%s): %v", tc.date, err)
		}
		_, _, _, err = ParseWorklogTimes(date, WorklogTimeInput{StartTime: tc.clock, EndTime: "04:00"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ParseWorklogTimes(%s %s) error = %v, want %q", tc.date, tc.clock, err, tc.want)
		}
	}
}

func TestResolveTimezoneRejectsNumericOffsetContract(t *testing.T) {
	if _, _, err := ResolveTimezone("+01:00"); err == nil {
		t.Fatal("ResolveTimezone accepted numeric offset, want IANA timezone validation error")
	}
}

func TestResolveTimezoneOrUTCFallsBackForInvalidStoredZone(t *testing.T) {
	for _, name := range []string{"", "Local", "Not/AZone", "+01:00"} {
		resolved, location := ResolveTimezoneOrUTC(name)
		if resolved != "UTC" || location != time.UTC {
			t.Fatalf("ResolveTimezoneOrUTC(%q) = (%q, %v), want (UTC, UTC)", name, resolved, location)
		}
	}

	resolved, location := ResolveTimezoneOrUTC("America/Los_Angeles")
	if resolved != "America/Los_Angeles" || location.String() != "America/Los_Angeles" {
		t.Fatalf("ResolveTimezoneOrUTC(valid) = (%q, %v)", resolved, location)
	}
}

func TestCivilDateRangeUTCUsesLocalMidnightsAcrossDST(t *testing.T) {
	_, location, err := ResolveTimezone("America/Los_Angeles")
	if err != nil {
		t.Fatalf("ResolveTimezone: %v", err)
	}

	tests := []struct {
		name      string
		date      string
		wantStart string
		wantEnd   string
		wantHours time.Duration
	}{
		{
			name:      "spring forward",
			date:      "2026-03-08",
			wantStart: "2026-03-08T08:00:00Z",
			wantEnd:   "2026-03-09T07:00:00Z",
			wantHours: 23 * time.Hour,
		},
		{
			name:      "fall back",
			date:      "2026-11-01",
			wantStart: "2026-11-01T07:00:00Z",
			wantEnd:   "2026-11-02T08:00:00Z",
			wantHours: 25 * time.Hour,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, err := CivilDateRangeUTC(test.date, test.date, location)
			if err != nil {
				t.Fatalf("CivilDateRangeUTC: %v", err)
			}
			if got := start.Format(time.RFC3339); got != test.wantStart {
				t.Fatalf("start = %s, want %s", got, test.wantStart)
			}
			if got := end.Format(time.RFC3339); got != test.wantEnd {
				t.Fatalf("end = %s, want %s", got, test.wantEnd)
			}
			if got := end.Sub(start); got != test.wantHours {
				t.Fatalf("range duration = %s, want %s", got, test.wantHours)
			}
		})
	}
}

func TestCivilDateRangeUTCRejectsInvalidRange(t *testing.T) {
	_, location, err := ResolveTimezone("Europe/Zurich")
	if err != nil {
		t.Fatalf("ResolveTimezone: %v", err)
	}

	for _, test := range []struct {
		name  string
		start string
		end   string
	}{
		{name: "invalid start", start: "2026-02-30", end: "2026-03-01"},
		{name: "invalid end", start: "2026-03-01", end: "tomorrow"},
		{name: "reversed", start: "2026-03-02", end: "2026-03-01"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := CivilDateRangeUTC(test.start, test.end, location); err == nil {
				t.Fatal("CivilDateRangeUTC succeeded, want validation error")
			}
		})
	}
}

func TestRedactInaccessibleWorklogItemsFailsClosedAndCachesWorkspaceChecks(t *testing.T) {
	item1, item2, workspaceID := 1, 2, 7
	worklogs := []models.Worklog{
		{ItemID: &item1, ItemTitle: "secret one", WorkspaceID: &workspaceID, WorkspaceKey: "SEC", WorkspaceItemNumber: 1},
		{ItemID: &item2, ItemTitle: "secret two", WorkspaceID: &workspaceID, WorkspaceKey: "SEC", WorkspaceItemNumber: 2},
	}
	checks := 0
	redacted := RedactInaccessibleWorklogItems(worklogs, func(int) (bool, error) {
		checks++
		return false, errors.New("permission backend unavailable")
	})

	if checks != 1 {
		t.Fatalf("workspace checks = %d, want 1", checks)
	}
	for i, worklog := range redacted {
		if worklog.ItemID != nil || worklog.WorkspaceID != nil || worklog.ItemTitle != "" || worklog.WorkspaceKey != "" || worklog.WorkspaceItemNumber != 0 {
			t.Fatalf("worklog %d retained item metadata: %+v", i, worklog)
		}
	}
	if worklogs[0].ItemID == nil || worklogs[0].ItemTitle == "" {
		t.Fatal("input slice was mutated")
	}
}

func TestResolveAccessibleWorklogItemUsesSameGateForIDAndKey(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "worklogs.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	workspaceResult, err := db.ExecWrite(`INSERT INTO workspaces (name, key, description, active) VALUES ('Secret', 'SEC', '', true)`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	workspaceID, err := workspaceResult.LastInsertId()
	if err != nil {
		t.Fatalf("workspace LastInsertId: %v", err)
	}
	itemID64, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: int(workspaceID),
		Title:       "Restricted",
	})
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	itemID := int(itemID64)

	// The production path assigns the workspace item number; build the
	// workspace-key reference from the actual row so the key gate is tested
	// against a real number.
	var itemNumber int
	if err := db.QueryRow(`SELECT workspace_item_number FROM items WHERE id = ?`, itemID).Scan(&itemNumber); err != nil {
		t.Fatalf("load workspace item number: %v", err)
	}
	itemKey := fmt.Sprintf("SEC-%d", itemNumber)

	deny := func(int) (bool, error) { return false, nil }
	if _, err := ResolveAccessibleWorklogItem(db, itemID, "", deny); !errors.Is(err, ErrWorklogItemNotFound) {
		t.Fatalf("numeric denied error = %v, want ErrWorklogItemNotFound", err)
	}
	if _, err := ResolveAccessibleWorklogItem(db, 0, itemKey, deny); !errors.Is(err, ErrWorklogItemNotFound) {
		t.Fatalf("key denied error = %v, want ErrWorklogItemNotFound", err)
	}

	allow := func(id int) (bool, error) { return id == int(workspaceID), nil }
	for _, reference := range []struct {
		id  int
		key string
	}{{id: itemID}, {key: itemKey}} {
		resolved, err := ResolveAccessibleWorklogItem(db, reference.id, reference.key, allow)
		if err != nil {
			t.Fatalf("ResolveAccessibleWorklogItem(%+v): %v", reference, err)
		}
		if resolved != itemID {
			t.Fatalf("resolved ID = %d, want %d", resolved, itemID)
		}
	}
}
