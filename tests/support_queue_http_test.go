package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// supportQueueFixture seeds one workspace whose ticket population pins every
// queue's expected count, all created through the production HTTP API.
type supportQueueFixture struct {
	workspaceID int
	workspaceKe string
	orphan      int // no team, no assignee
	teamTicket  int // team-owned, unclaimed
	mine        int // assigned to the acting admin
	overdue     int // past due date, unowned
	done        int // completed, unowned
	adminID     int
}

func setupSupportQueueFixture(t *testing.T, server *TestServer, name string) supportQueueFixture {
	t.Helper()

	emailFixtureSeq++
	key := fmt.Sprintf("SQ%d", emailFixtureSeq)
	workspaceID, workspaceKey := CreateTestWorkspace(t, server, name+" WS", key)
	configSetID := GetDefaultConfigurationSet(t, server)
	AssociateWorkspaceWithConfigSet(t, server, workspaceID, configSetID)

	var adminID int
	if err := server.DB().QueryRow(`SELECT id FROM users WHERE username = 'admin' LIMIT 1`).Scan(&adminID); err != nil {
		t.Fatalf("load admin user: %v", err)
	}

	createTicket := func(title string, fields map[string]interface{}) int {
		payload := map[string]interface{}{
			"title":        title,
			"workspace_id": workspaceID,
		}
		for k, v := range fields {
			payload[k] = v
		}
		resp := MakeAuthRequest(t, server, http.MethodPost, "/items", payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var result map[string]interface{}
		DecodeJSON(t, resp, &result)
		return ExtractIDFromResponse(t, result)
	}

	teamResp := MakeAuthRequest(t, server, http.MethodPost, "/teams", map[string]interface{}{"name": name + " Team"})
	AssertStatusCode(t, teamResp, http.StatusCreated)
	var team struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, teamResp, &team)
	teamResp.Body.Close()

	itemTypes := GetItemTypes(t, server, configSetID)

	fix := supportQueueFixture{
		workspaceID: workspaceID,
		workspaceKe: workspaceKey,
		adminID:     adminID,
	}
	fix.orphan = createTicket("Queue orphan", map[string]interface{}{"item_type_id": itemTypes["Task"]})
	fix.teamTicket = createTicket("Queue team ticket", map[string]interface{}{"item_type_id": itemTypes["Task"], "team_id": team.ID})
	fix.mine = createTicket("Queue mine", map[string]interface{}{"item_type_id": itemTypes["Task"], "assignee_id": adminID})
	fix.overdue = createTicket("Queue overdue", map[string]interface{}{
		"item_type_id": itemTypes["Task"],
		"due_date":     "2026-01-01T00:00:00Z",
	})
	fix.done = createTicket("Queue done", map[string]interface{}{"item_type_id": itemTypes["Task"]})

	// Complete the done ticket through the production update path.
	statusResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/workspaces/%d/statuses", workspaceID), nil)
	var statuses []map[string]interface{}
	DecodeJSON(t, statusResp, &statuses)
	statusResp.Body.Close()
	doneStatus := 0
	for _, s := range statuses {
		if s["name"] == "Done" {
			doneStatus = int(s["id"].(float64))
		}
	}
	if doneStatus == 0 {
		t.Fatal("workspace has no Done status")
	}
	patch := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/items/%d/transition", fix.done), map[string]interface{}{
		"to_status_id": doneStatus,
	})
	defer patch.Body.Close()
	AssertStatusCode(t, patch, http.StatusOK)

	return fix
}

func fetchQueues(t *testing.T, server *TestServer, workspaceKey string) []map[string]interface{} {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%s/queues", workspaceKey), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var queues []map[string]interface{}
	DecodeJSON(t, resp, &queues)
	return queues
}

func queueCount(t *testing.T, queues []map[string]interface{}, key string) int {
	t.Helper()
	for _, q := range queues {
		if q["key"] == key {
			count, ok := q["count"].(float64)
			if !ok {
				t.Fatalf("queue %s count = %#v, want a number", key, q["count"])
			}
			return int(count)
		}
	}
	t.Fatalf("queue %s missing from catalog", key)
	return 0
}

// TestSupportQueuesCatalogAndMembership verifies the stable queue catalog and
// that each preset counts exactly the tickets it claims.
func TestSupportQueuesCatalogAndMembership(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupSupportQueueFixture(t, server, "Queue catalog")

	queues := fetchQueues(t, server, fix.workspaceKe)

	wantOrder := []string{"unassigned", "team-owned", "assigned-to-me", "waiting", "overdue", "recently-updated", "sla-at-risk"}
	if len(queues) != len(wantOrder) {
		t.Fatalf("queue catalog length = %d, want %d", len(queues), len(wantOrder))
	}
	for i, want := range wantOrder {
		if queues[i]["key"] != want {
			t.Fatalf("queue[%d] key = %v, want %q (catalog order is a stable contract)", i, queues[i]["key"], want)
		}
		if ql, _ := queues[i]["ql"].(string); ql == "" {
			t.Fatalf("queue %s exposes no ql definition", want)
		}
	}

	// Membership: the orphan is the only fully unowned open ticket; the team
	// ticket and my ticket left the unassigned queue; the completed ticket is
	// excluded everywhere.
	if got := queueCount(t, queues, "unassigned"); got != 2 { // orphan + overdue
		t.Fatalf("unassigned count = %d, want 2", got)
	}
	if got := queueCount(t, queues, "team-owned"); got != 1 {
		t.Fatalf("team-owned count = %d, want 1", got)
	}
	if got := queueCount(t, queues, "assigned-to-me"); got != 1 {
		t.Fatalf("assigned-to-me count = %d, want 1", got)
	}
	if got := queueCount(t, queues, "overdue"); got != 1 {
		t.Fatalf("overdue count = %d, want 1", got)
	}
	// The brand-new tickets were all touched moments ago; the completed one
	// stays out.
	if got := queueCount(t, queues, "recently-updated"); got != 4 {
		t.Fatalf("recently-updated count = %d, want 4 (open tickets only)", got)
	}
	// No SLA is configured in this workspace, so the SLA-derived queues are
	// empty rather than wrong.
	if got := queueCount(t, queues, "waiting"); got != 0 {
		t.Fatalf("waiting count = %d, want 0 without SLA configuration", got)
	}
	if got := queueCount(t, queues, "sla-at-risk"); got != 0 {
		t.Fatalf("sla-at-risk count = %d, want 0 without SLA configuration", got)
	}

	// The queue count and the generic list pipeline agree for the same CQL:
	// the unassigned queue's rows are exactly the tickets the list returns.
	var unassignedQL string
	for _, q := range queues {
		if q["key"] == "unassigned" {
			unassignedQL, _ = q["ql"].(string)
		}
	}
	listResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/items?workspace_id=%d&ql=%s", fix.workspaceID, url.QueryEscape(unassignedQL)), nil)
	defer listResp.Body.Close()
	AssertStatusCode(t, listResp, http.StatusOK)
	var list struct {
		Data       []map[string]interface{} `json:"data"`
		Pagination struct {
			TotalItems int `json:"total_items"`
		} `json:"pagination"`
	}
	DecodeJSON(t, listResp, &list)
	if list.Pagination.TotalItems != 2 {
		t.Fatalf("unassigned list total = %d, want 2", list.Pagination.TotalItems)
	}
	if len(list.Data) != 2 {
		t.Fatalf("unassigned list rows = %d, want 2", len(list.Data))
	}
	seen := map[int]bool{}
	for _, item := range list.Data {
		id := int(item["id"].(float64))
		seen[id] = true
	}
	if !seen[fix.orphan] || !seen[fix.overdue] {
		t.Fatalf("unassigned list = %v, want orphan %d and overdue %d", seen, fix.orphan, fix.overdue)
	}
}

// TestSupportQueuesRequiresWorkspaceAccess verifies the denial contract: the
// catalog is existence-masked from users without workspace access and for
// unknown workspace keys.
func TestSupportQueuesRequiresWorkspaceAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fix := setupSupportQueueFixture(t, server, "Queue denial")

	otherUserID, otherUsername, otherPassword := CreateTestUserWithCredentials(
		t, server, "queue_other", "queue-other@test.com",
	)
	AssignWorkspaceRole(t, server, otherUserID, fix.workspaceID, "Viewer")
	otherToken, _ := CreateAuthCredentialsForUser(t, server, otherUsername, otherPassword)

	// A viewer may read the catalog.
	viewerResp := MakeAuthRequestWithToken(t, server, otherToken, http.MethodGet, fmt.Sprintf("/v2/workspaces/%s/queues", fix.workspaceKe), nil)
	defer viewerResp.Body.Close()
	AssertStatusCode(t, viewerResp, http.StatusOK)

	// A user with no workspace relationship gets the masked 404.
	_, strangerUsername, strangerPassword := CreateTestUserWithCredentials(t, server, "queue_stranger", "queue-stranger@test.com")
	strangerToken, _ := CreateAuthCredentialsForUser(t, server, strangerUsername, strangerPassword)
	strangerResp := MakeAuthRequestWithToken(t, server, strangerToken, http.MethodGet, fmt.Sprintf("/v2/workspaces/%s/queues", fix.workspaceKe), nil)
	defer strangerResp.Body.Close()
	AssertStatusCode(t, strangerResp, http.StatusNotFound)

	// Unknown workspace keys are indistinguishable from denials.
	missing := MakeAuthRequest(t, server, http.MethodGet, "/v2/workspaces/NOSUCHWS/queues", nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}
