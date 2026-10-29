//go:build test

package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"windshift/internal/models"
)

// TestSLATestClockAdvancesEvaluationInstant proves the server test-clock seam:
// with the due-work loop disabled, advancing the clock past the goal flips the
// derived breach state on a read, so browser display never depends on
// background work.
func TestSLATestClockAdvancesEvaluationInstant(t *testing.T) {
	t.Setenv("WINDSHIFT_E2E_TEST_HOOKS", "1")
	server, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Clock Workspace", shortKey("SCW"))

	calResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), map[string]any{"name": "Clock hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, calResp, &calendar)
	_ = calResp.Body.Close()

	metricPayload := map[string]any{
		"name":           "Clock response",
		"display_format": "time",
		"is_active":      true,
		"conditions": []map[string]any{{
			"phase": "start", "position": 0, "condition_type": "created",
			"config": map[string]any{},
		}},
		"goals": []map[string]any{{
			"position": 0, "ql_query": "1 = 1", "import_status": "native",
			"targets": []map[string]any{{
				"position": 0, "target_ms": 3_600_000, "calendar_id": calendar.ID,
			}},
		}},
	}
	resp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	itemID := CreateTestItem(t, server, workspaceID, "Clock item")

	readState := func() models.DerivedSLACycle {
		t.Helper()
		stateResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/sla", itemID), nil)
		AssertStatusCode(t, stateResp, http.StatusOK)
		var states []models.ItemSLA
		DecodeJSON(t, stateResp, &states)
		_ = stateResp.Body.Close()
		if len(states) != 1 || states[0].Ongoing == nil {
			t.Fatalf("item SLA states = %#v", states)
		}
		return *states[0].Ongoing
	}

	if readState().Breached {
		t.Fatal("cycle reported breached before the clock advanced")
	}

	clockResp := MakeAuthRequest(t, server, http.MethodPost, "/test/sla/clock", map[string]any{"advance_ms": int64((2 * time.Hour) / time.Millisecond)})
	AssertStatusCode(t, clockResp, http.StatusOK)
	var clockState map[string]string
	DecodeJSON(t, clockResp, &clockState)
	_ = clockResp.Body.Close()
	if clockState["now"] == "" {
		t.Fatalf("clock response = %#v", clockState)
	}

	if !readState().Breached {
		t.Fatal("cycle not reported breached after advancing past the goal")
	}
}
