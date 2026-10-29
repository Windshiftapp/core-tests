//go:build test

package v2

import (
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
)

func TestMergeWorklogPatchPreservesOmittedClockTimes(t *testing.T) {
	location, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	start := time.Date(2026, 9, 5, 9, 15, 0, 0, location)
	end := time.Date(2026, 9, 5, 10, 45, 0, 0, location)
	itemID := 42
	current := &models.Worklog{
		ProjectID:    7,
		ItemID:       &itemID,
		Description:  "before",
		Date:         services.WorklogDateUnix(start),
		StartTime:    start.Unix(),
		EndTime:      end.Unix(),
		DurationMins: 90,
	}

	tests := []struct {
		name  string
		patch worklogPatchRequest
	}{
		{name: "empty", patch: worklogPatchRequest{}},
		{name: "description", patch: worklogPatchRequest{Description: Optional[string]{Set: true, Value: "after"}}},
		{name: "project", patch: worklogPatchRequest{ProjectID: Optional[int]{Set: true, Value: 8}}},
		{name: "item", patch: worklogPatchRequest{ItemID: Optional[int]{Set: true, Value: 99}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := mergeWorklogPatch(current, tt.patch, "Europe/Zurich")
			if !input.PreserveStored {
				t.Fatal("stored interval was not marked for exact preservation")
			}
			if input.StoredStartUnix != start.Unix() || input.StoredEndUnix != end.Unix() || input.DurationMinutes != 90 {
				t.Fatalf("stored interval = %d-%d (%d), want %d-%d (90)", input.StoredStartUnix, input.StoredEndUnix, input.DurationMinutes, start.Unix(), end.Unix())
			}
		})
	}
}

func TestMergeWorklogPatchDefinesTemporalEdits(t *testing.T) {
	location, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	start := time.Date(2026, 9, 5, 9, 15, 0, 0, location)
	end := time.Date(2026, 9, 5, 10, 45, 0, 0, location)
	current := &models.Worklog{
		ProjectID:    7,
		Date:         services.WorklogDateUnix(start),
		StartTime:    start.Unix(),
		EndTime:      end.Unix(),
		DurationMins: 90,
	}

	tests := []struct {
		name             string
		patch            worklogPatchRequest
		wantDate         string
		wantStart        string
		wantEnd          string
		wantDurationMins int
		wantPreserved    bool
	}{
		{
			name: "date keeps local clocks", patch: worklogPatchRequest{Date: Optional[string]{Set: true, Value: "2026-09-06"}},
			wantDate: "2026-09-06",
		},
		{
			name: "start keeps existing end", patch: worklogPatchRequest{StartTime: Optional[string]{Set: true, Value: "08:30"}},
			wantDate: "2026-09-05", wantStart: "08:30",
		},
		{
			name: "end keeps existing start", patch: worklogPatchRequest{EndTime: Optional[string]{Set: true, Value: "11:00"}},
			wantDate: "2026-09-05", wantEnd: "11:00",
		},
		{
			name: "timezone preserves the instant", patch: worklogPatchRequest{Timezone: Optional[string]{Set: true, Value: "UTC"}},
			wantDate: "2026-09-05", wantDurationMins: 90, wantPreserved: true,
		},
		{
			name: "duration remains an explicit duration edit", patch: worklogPatchRequest{DurationMinutes: Optional[int]{Set: true, Value: 120}},
			wantDate: "2026-09-05", wantDurationMins: 120,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := mergeWorklogPatch(current, tt.patch, "Europe/Zurich")
			if input.Date != tt.wantDate || input.StartTime != tt.wantStart || input.EndTime != tt.wantEnd || input.DurationMinutes != tt.wantDurationMins || input.PreserveStored != tt.wantPreserved {
				t.Fatalf("temporal input = date:%s start:%s end:%s duration:%d preserved:%t, want date:%s start:%s end:%s duration:%d preserved:%t",
					input.Date, input.StartTime, input.EndTime, input.DurationMinutes, input.PreserveStored,
					tt.wantDate, tt.wantStart, tt.wantEnd, tt.wantDurationMins, tt.wantPreserved)
			}
			if !tt.wantPreserved && tt.wantDurationMins == 0 && (input.StoredStartUnix != start.Unix() || input.StoredEndUnix != end.Unix()) {
				t.Fatalf("stored clock fallback = %d-%d, want %d-%d", input.StoredStartUnix, input.StoredEndUnix, start.Unix(), end.Unix())
			}
		})
	}
}
