package tests

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/scheduler"
)

// TestOffboardingRecordsAssigneeRemovalFact guards the recorder-coverage fix:
// clearing a departing user's assignments must emit item facts, not just write
// item rows, so inline SLA evaluation sees the assignee change.
func TestOffboardingRecordsAssigneeRemovalFact(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Offboard", shortKey("SOF"))
	userID, _, _ := CreateTestUserWithCredentials(t, server, shortKey("sof"), shortKey("sof")+"@example.test")
	AssignWorkspaceRole(t, server, userID, workspaceID, "Editor")

	itemID := CreateTestItem(t, server, workspaceID, "Assigned item")

	assign := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/items/%d", itemID), map[string]any{"assignee_id": userID})
	AssertStatusCode(t, assign, http.StatusOK)
	_ = assign.Body.Close()

	before := itemUpdatedEventCount(t, server, itemID)

	offboard := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/users/%d", userID), nil)
	AssertStatusCode(t, offboard, http.StatusNoContent)
	_ = offboard.Body.Close()

	after := itemUpdatedEventCount(t, server, itemID)
	if after <= before {
		t.Fatalf("offboarding recorded no item.updated fact: before=%d after=%d", before, after)
	}

	var assignee sql.NullInt64
	if err := server.DB().QueryRow(`SELECT assignee_id FROM items WHERE id = ?`, itemID).Scan(&assignee); err != nil {
		t.Fatalf("load item assignee: %v", err)
	}
	if assignee.Valid {
		t.Fatalf("assignee still set after offboarding: %d", assignee.Int64)
	}
}

func itemUpdatedEventCount(t *testing.T, server *TestServer, itemID int) int {
	t.Helper()
	var count int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'item' AND aggregate_id = ? AND event_type = 'item.updated'`,
		itemID,
	).Scan(&count); err != nil {
		t.Fatalf("count item.updated events: %v", err)
	}
	return count
}

// TestCFVCleanupEnqueuesSLARecalcForItems guards the custom-field cleanup fix:
// when the scheduler strips a custom field value from item rows outside the
// recorder, it must enqueue an SLA recalculation for each changed item so a
// metric that references the field is re-evaluated.
func TestCFVCleanupEnqueuesSLARecalcForItems(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "SLA CFV Cleanup", shortKey("SCFV"))

	// Give the workspace SLA configuration so the recalc gate passes.
	calResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/calendars", workspaceID), map[string]any{"name": "Cleanup hours", "timezone": "UTC"})
	AssertStatusCode(t, calResp, http.StatusCreated)
	var calendar models.WorkingCalendar
	DecodeJSON(t, calResp, &calendar)
	_ = calResp.Body.Close()

	metricPayload := map[string]any{
		"name":           "Cleanup metric",
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
	metricResp := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/workspaces/%d/sla/metrics", workspaceID), metricPayload)
	AssertStatusCode(t, metricResp, http.StatusCreated)
	_ = metricResp.Body.Close()

	fieldID := CreateTestCustomField(t, server, "SLA Cleanup Field "+ts(), "text", "")
	fieldKey := strconv.Itoa(fieldID)

	const itemCount = 3
	itemIDs := make([]int, 0, itemCount)
	for i := range itemCount {
		itemID := createItemWithCFV(t, server, workspaceID, fmt.Sprintf("Cleanup item %d %s", i, ts()),
			map[string]interface{}{fieldKey: fmt.Sprintf("v%d", i)})
		itemIDs = append(itemIDs, itemID)
	}

	if err := scheduler.EnqueueFieldCleanup(server.DB(), fieldID); err != nil {
		t.Fatalf("EnqueueFieldCleanup: %v", err)
	}
	runCFVCleanupOnce(t, server)

	for _, itemID := range itemIDs {
		var count int
		if err := server.DB().QueryRow(
			`SELECT COUNT(*) FROM sla_jobs WHERE kind = 'recalc_item' AND item_id = ?`, itemID,
		).Scan(&count); err != nil {
			t.Fatalf("count recalc_item jobs for item %d: %v", itemID, err)
		}
		if count != 1 {
			t.Fatalf("recalc_item jobs for cleaned item %d = %d, want 1", itemID, count)
		}
	}
}
