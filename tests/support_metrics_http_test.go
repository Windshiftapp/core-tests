//go:build test

package tests

import (
	"encoding/json"
	"io"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type supportMetricsDoc struct {
	Summary struct {
		Created       int64 `json:"created"`
		Resolved      int64 `json:"resolved"`
		Reopened      int64 `json:"reopened"`
		Backlog       int64 `json:"backlog"`
		Unassigned    int64 `json:"unassigned"`
		FirstResponse struct {
			P50Ms float64 `json:"p50_ms"`
			P90Ms float64 `json:"p90_ms"`
			Count int64   `json:"count"`
		} `json:"first_response"`
		Resolution struct {
			Count int64 `json:"count"`
		} `json:"resolution"`
		SLA *struct {
			Completed    int64   `json:"completed"`
			Breached     int64   `json:"breached"`
			CompliantPct float64 `json:"compliant_pct"`
		} `json:"sla"`
	} `json:"summary"`
	Buckets []struct {
		BucketStart string `json:"bucket_start"`
		Created     int64  `json:"created"`
		Resolved    int64  `json:"resolved"`
		BacklogEnd  int64  `json:"backlog_end"`
	} `json:"buckets"`
	Segments []struct {
		Key      string `json:"key"`
		Label    string `json:"label"`
		Created  int64  `json:"created"`
		Backlog  int64  `json:"backlog"`
		Reopened int64  `json:"reopened"`
	} `json:"segments"`
	Queues []struct {
		Name  string `json:"name"`
		Key   string `json:"key"`
		Count int64  `json:"count"`
	} `json:"queues"`
}

func fetchSupportMetrics(t *testing.T, server *TestServer, queryString string) (*supportMetricsDoc, *http.Response) {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, "/v2/support/metrics/aggregate"+queryString, nil)
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("aggregate request failed: %d - %s", resp.StatusCode, string(body))
	}
	doc := &supportMetricsDoc{}
	if err := json.NewDecoder(resp.Body).Decode(doc); err != nil {
		t.Fatalf("decode aggregate document: %v", err)
	}
	return doc, resp
}

// TestSupportMetricsAggregateEndToEnd drives a real portal ticket through
// first response and resolution and asserts the aggregate document.
func TestSupportMetricsAggregateEndToEnd(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	workspaceID, _ := CreateTestWorkspace(t, server, "Support Metrics WS", shortKey("SMWS"))
	portalSlug, channelID := SetupPortalChannel(t, server, workspaceID)
	_, portalToken := CreatePortalCustomerWithSession(t, server, channelID, "Metrics Customer", fmt.Sprintf("metrics-%d@example.com", time.Now().UnixNano()))
	ticketID := SubmitPortalRequest(t, server, portalSlug, portalToken, "Printer on fire")

	// First response: a public agent reply through the production API.
	reply := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", ticketID), map[string]interface{}{
		"content": "We are on it", "is_private": false,
	})
	AssertStatusCode(t, reply, http.StatusCreated)

	// Private notes and portal replies must not add first responses.
	private := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/comments", ticketID), map[string]interface{}{
		"content": "internal note", "is_private": true,
	})
	AssertStatusCode(t, private, http.StatusCreated)

	// Resolve the ticket through the transition endpoint.
	statuses := lookupDefaultStatuses(t, server, workspaceID)
	transition := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/items/%d/transition", ticketID), map[string]interface{}{
		"to_status_id": statuses.Done,
	})
	AssertStatusCode(t, transition, http.StatusOK)

	from := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")
	doc, resp := fetchSupportMetrics(t, server, fmt.Sprintf("?workspace_id=%d&from=%s&to=%s", workspaceID, from, to))
	AssertStatusCode(t, resp, http.StatusOK)

	if doc.Summary.Created != 1 {
		t.Errorf("summary.created = %d, want 1", doc.Summary.Created)
	}
	if doc.Summary.Resolved != 1 {
		t.Errorf("summary.resolved = %d, want 1", doc.Summary.Resolved)
	}
	if doc.Summary.Backlog != 0 {
		t.Errorf("summary.backlog = %d, want 0 (ticket resolved)", doc.Summary.Backlog)
	}
	if doc.Summary.FirstResponse.Count != 1 {
		t.Errorf("first_response.count = %d, want 1 (private note excluded)", doc.Summary.FirstResponse.Count)
	}
	if doc.Summary.Resolution.Count != 1 {
		t.Errorf("resolution.count = %d, want 1", doc.Summary.Resolution.Count)
	}
	if doc.Summary.SLA == nil {
		t.Errorf("summary.sla missing, want an explicit zero-value SLA block")
	}
	if len(doc.Queues) == 0 {
		t.Errorf("queues empty for workspace scope, want the built-in queue catalog")
	}

	today := time.Now().Format("2006-01-02")
	var todayBucket *struct {
		BucketStart string `json:"bucket_start"`
		Created     int64  `json:"created"`
		Resolved    int64  `json:"resolved"`
		BacklogEnd  int64  `json:"backlog_end"`
	}
	for i := range doc.Buckets {
		if doc.Buckets[i].BucketStart == today {
			todayBucket = &doc.Buckets[i]
		}
	}
	if todayBucket == nil {
		t.Fatalf("no bucket for %s in %#v", today, doc.Buckets)
	}
	if todayBucket.Created != 1 || todayBucket.Resolved != 1 {
		t.Errorf("today bucket = %#v, want created=1 resolved=1", *todayBucket)
	}
	if todayBucket.BacklogEnd != 0 {
		t.Errorf("today bucket backlog_end = %d, want 0 (created and resolved same day)", todayBucket.BacklogEnd)
	}

	// Segmentation by channel: the portal channel owns the ticket.
	segDoc, segResp := fetchSupportMetrics(t, server, fmt.Sprintf("?workspace_id=%d&from=%s&to=%s&segment=channel", workspaceID, from, to))
	AssertStatusCode(t, segResp, http.StatusOK)
	if len(segDoc.Segments) == 0 {
		t.Fatalf("segments empty for segment=channel, want the portal channel")
	}
	found := false
	for _, segment := range segDoc.Segments {
		if segment.Created == 1 && segment.Label != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("channel segment with created=1 and a label not found in %#v", segDoc.Segments)
	}

	// Collection scope: an open-tickets-only saved search excludes the
	// resolved ticket from the created counts.
	collectionResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]interface{}{
		"name": "Open support tickets", "workspace_id": workspaceID, "ql_query": "statusCompleted = false",
	})
	AssertStatusCode(t, collectionResp, http.StatusCreated)
	var collectionResult struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, collectionResp, &collectionResult)
	colDoc, colResp := fetchSupportMetrics(t, server, fmt.Sprintf("?collection_id=%d&from=%s&to=%s", collectionResult.Data.ID, from, to))
	AssertStatusCode(t, colResp, http.StatusOK)
	if colDoc.Summary.Created != 0 {
		t.Errorf("collection-scoped created = %d, want 0 (resolved ticket filtered by QL)", colDoc.Summary.Created)
	}
	if colDoc.Summary.Backlog != 0 {
		t.Errorf("collection-scoped backlog = %d, want 0", colDoc.Summary.Backlog)
	}
}

// TestSupportMetricsAggregatePermissionAndValidation covers the fail-closed
// scope contract and request validation.
func TestSupportMetricsAggregatePermissionAndValidation(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Support Perms WS", shortKey("SMPW"))
	outsiderID, _, outsiderPass := CreateTestUserWithCredentials(t, server, "smpoutsider", "smpoutsider@test.local")
	otherWS, _ := CreateTestWorkspace(t, server, "Support Other WS", shortKey("SMOW"))
	AssignWorkspaceRole(t, server, outsiderID, otherWS, "Viewer")
	// The everyone-viewer fallback closes once any member holds an explicit
	// role on the workspace (permission_cache_builder.go); seed that before
	// the outsider's first request fills its accessible-workspace snapshot.
	memberID, _, _ := CreateTestUserWithCredentials(t, server, "smpmember", "smpmember@test.local")
	AssignWorkspaceRole(t, server, memberID, wsID, "Viewer")
	_, outsiderToken := CreateAuthCredentialsForUser(t, server, "smpoutsider", outsiderPass)

	// A workspace the caller cannot see is 404, not 403 (no existence leak).
	denied := MakeBearerRequestWithToken(t, server, outsiderToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/support/metrics/aggregate?workspace_id=%d", wsID), nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusNotFound)

	// Same request for a workspace the caller can see works.
	allowed := MakeBearerRequestWithToken(t, server, outsiderToken, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/support/metrics/aggregate?workspace_id=%d", otherWS), nil)
	defer allowed.Body.Close()
	AssertStatusCode(t, allowed, http.StatusOK)

	cases := []struct {
		name   string
		query  string
		status int
	}{
		{"no scope", "", http.StatusBadRequest},
		{"both scopes", fmt.Sprintf("?workspace_id=%d&collection_id=1", wsID), http.StatusBadRequest},
		{"unknown workspace", "?workspace_id=99999999", http.StatusNotFound},
		{"reversed range", fmt.Sprintf("?workspace_id=%d&from=2030-01-10&to=2030-01-01", wsID), http.StatusBadRequest},
		{"range too long", fmt.Sprintf("?workspace_id=%d&from=2020-01-01&to=2030-01-01", wsID), http.StatusBadRequest},
		{"bad bucket", fmt.Sprintf("?workspace_id=%d&bucket=hour", wsID), http.StatusBadRequest},
		{"bad segment", fmt.Sprintf("?workspace_id=%d&segment=mood", wsID), http.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resp := MakeAuthRequest(t, server, http.MethodGet, "/v2/support/metrics/aggregate"+testCase.query, nil)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, testCase.status)
		})
	}
}
