package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

func TestCloudClientGetProjectScreenConfigurationResolvesDefaultsAndFlattensTabs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/3/issuetypescreenscheme/project":
			if got := r.URL.Query().Get("projectId"); got != "10000" {
				t.Fatalf("projectId = %q, want 10000", got)
			}
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"total": 1,
				"values": [{
					"issueTypeScreenScheme": {"id": "9"},
					"projectIds": ["10000"]
				}]
			}`))
		case r.URL.Path == "/rest/api/3/issuetypescreenscheme/mapping":
			if got := r.URL.Query().Get("issueTypeScreenSchemeId"); got != "9" {
				t.Fatalf("issueTypeScreenSchemeId = %q, want 9", got)
			}
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"total": 1,
				"values": [{
					"issueTypeId": "default",
					"issueTypeScreenSchemeId": "9",
					"screenSchemeId": "55"
				}]
			}`))
		case r.URL.Path == "/rest/api/3/screenscheme":
			if got := r.URL.Query().Get("id"); got != "55" {
				t.Fatalf("screen scheme id = %q, want 55", got)
			}
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"total": 1,
				"values": [{
					"id": 55,
					"name": "Default scheme",
					"screens": {"default": 100, "create": 101, "edit": 102}
				}]
			}`))
		case r.URL.Path == "/rest/api/3/screens":
			id := r.URL.Query().Get("id")
			_, _ = w.Write([]byte(`{
				"isLast": true,
				"total": 1,
				"values": [{"id": ` + id + `, "name": "Screen ` + id + `", "description": "Source"}]
			}`))
		case strings.HasSuffix(r.URL.Path, "/tabs"):
			if got := r.URL.Query().Get("projectKey"); got != "APP" {
				t.Fatalf("tabs projectKey = %q, want APP", got)
			}
			screenID := strings.Split(r.URL.Path, "/")[5]
			if screenID == "100" {
				_, _ = w.Write([]byte(`[{"id": 1, "name": "Main"}, {"id": 2, "name": "More"}]`))
			} else {
				_, _ = w.Write([]byte(`[{"id": 1, "name": "Main"}]`))
			}
		case strings.Contains(r.URL.Path, "/tabs/") && strings.HasSuffix(r.URL.Path, "/fields"):
			if got := r.URL.Query().Get("projectKey"); got != "APP" {
				t.Fatalf("fields projectKey = %q, want APP", got)
			}
			parts := strings.Split(r.URL.Path, "/")
			screenID, tabID := parts[5], parts[7]
			fields := []cloudScreenField{{ID: "summary", Name: "Summary"}}
			if screenID == "100" && tabID == "2" {
				fields = []cloudScreenField{{ID: "customfield_7", Name: "Impact"}}
			}
			if err := json.NewEncoder(w).Encode(fields); err != nil {
				t.Fatalf("encode fields: %v", err)
			}
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	config, err := client.GetProjectScreenConfiguration(
		context.Background(),
		"10000",
		"APP",
		[]string{"bug", "task"},
	)
	if err != nil {
		t.Fatalf("GetProjectScreenConfiguration() error = %v", err)
	}
	for _, issueTypeID := range []string{"bug", "task"} {
		got := config.IssueTypeScreens[issueTypeID]
		want := JiraIssueTypeScreens{
			CreateScreenID: "101",
			EditScreenID:   "102",
			ViewScreenID:   "100",
		}
		if got != want {
			t.Fatalf("screens for %s = %#v, want %#v", issueTypeID, got, want)
		}
	}
	if len(config.Screens) != 3 {
		t.Fatalf("screen count = %d, want 3", len(config.Screens))
	}
	if config.Screens[0].ID != "100" || config.Screens[0].TabCount != 2 {
		t.Fatalf("first screen = %#v", config.Screens[0])
	}
	if got := config.Screens[0].Fields; len(got) != 2 ||
		got[0].ID != "summary" || got[1].ID != "customfield_7" {
		t.Fatalf("flattened fields = %#v", got)
	}
}
