package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/models"
)

func TestValidateScheduleRequestRejectsInvalidTimezone(t *testing.T) {
	recorder := httptest.NewRecorder()
	if validateScheduleRequest(recorder, httptest.NewRequest("POST", "/", nil), models.OnCallScheduleRequest{Name: "Primary", Timezone: "+01:00"}) {
		t.Fatal("validateScheduleRequest accepted a numeric offset")
	}
	if !strings.Contains(recorder.Body.String(), "invalid IANA timezone") {
		t.Fatalf("response = %s, want invalid IANA timezone", recorder.Body.String())
	}
}

func TestValidateLayerRequestRejectsInvalidTemporalConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  models.OnCallScheduleLayerRequest
		want string
	}{
		{name: "start date", req: validLayerRequest("not-a-date", nil, "09:00"), want: "start_date"},
		{name: "end date", req: validLayerRequest("2026-07-14", onCallStringPointer("2026-07-13"), "09:00"), want: "end_date"},
		{name: "handoff", req: validLayerRequest("2026-07-14", nil, "25:00"), want: "handoff_time"},
		{name: "custom interval", req: models.OnCallScheduleLayerRequest{Name: "Primary", RotationType: "custom", StartDate: "2026-07-14", HandoffTime: "09:00"}, want: "rotation_interval_days"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			if validateLayerRequest(recorder, httptest.NewRequest("POST", "/", nil), tc.req) {
				t.Fatalf("validateLayerRequest accepted %+v", tc.req)
			}
			if !strings.Contains(recorder.Body.String(), tc.want) {
				t.Fatalf("response = %s, want %q", recorder.Body.String(), tc.want)
			}
		})
	}
}

func validLayerRequest(start string, end *string, handoff string) models.OnCallScheduleLayerRequest {
	return models.OnCallScheduleLayerRequest{Name: "Primary", RotationType: "daily", StartDate: start, EndDate: end, HandoffTime: handoff}
}

func onCallStringPointer(value string) *string { return &value }
