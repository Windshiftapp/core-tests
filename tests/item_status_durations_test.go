package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestItemStatusDurationsEndpoint(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	targetWorkspaceID, _ := CreateTestWorkspace(t, server, "Status durations target", shortKey("SDT"))
	otherWorkspaceID, _ := CreateTestWorkspace(t, server, "Status durations other", shortKey("SDO"))
	LockDownWorkspace(t, server, targetWorkspaceID)
	LockDownWorkspace(t, server, otherWorkspaceID)
	targetItemID := CreateTestItem(t, server, targetWorkspaceID, "Status duration item")

	t.Run("returns the current status duration", func(t *testing.T) {
		resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/status-durations", targetItemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		var body struct {
			Statuses []struct {
				StatusID        int    `json:"status_id"`
				StatusName      string `json:"status_name"`
				DurationSeconds int64  `json:"duration_seconds"`
				IsCurrent       bool   `json:"is_current"`
			} `json:"statuses"`
		}
		DecodeJSON(t, resp, &body)
		if len(body.Statuses) != 1 {
			t.Fatalf("expected one status since creation, got %#v", body.Statuses)
		}
		if body.Statuses[0].StatusID == 0 || body.Statuses[0].StatusName == "" || !body.Statuses[0].IsCurrent {
			t.Fatalf("unexpected current status response: %#v", body.Statuses[0])
		}
		if body.Statuses[0].DurationSeconds < 0 {
			t.Fatalf("duration must not be negative: %d", body.Statuses[0].DurationSeconds)
		}
	})

	t.Run("conceals the item from a cross-workspace user", func(t *testing.T) {
		userID, username, password := CreateTestUserWithCredentials(t, server, "status_duration_outsider", "status_duration_outsider@test.com")
		AssignWorkspaceRole(t, server, userID, otherWorkspaceID, "Viewer")
		cookie := CreateBearerTokenForUser(t, server, username, password)
		resp := MakeAuthRequestWithToken(t, server, cookie, http.MethodGet, fmt.Sprintf("/items/%d/status-durations", targetItemID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}
