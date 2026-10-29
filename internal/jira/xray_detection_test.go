package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"windshift/internal/xray"

	"golang.org/x/time/rate"
)

func TestCloudClientDetectXrayTestIssueTypesUsesOwnedProperty(t *testing.T) {
	t.Parallel()

	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/issuetype/10001/properties/com.xpandit.xray.issuetype.test":
			// A real Xray Cloud installation currently returns the property's
			// empty-object value directly. The 200 response and Xray-owned key
			// are the ownership marker.
			_, _ = w.Write([]byte(`{}`))
		case "/rest/api/3/issuetype/10004/properties/com.xpandit.xray.issuetype.test":
			// Keep compatibility with Jira responses that wrap the value.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"key":   XrayTestIssueTypeProperty,
				"value": true,
			})
		case "/rest/api/3/issuetype/10002/properties/com.xpandit.xray.issuetype.test":
			http.NotFound(w, r)
		case "/rest/api/3/issuetype/10003/properties/com.xpandit.xray.issuetype.test":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"key":   XrayTestIssueTypeProperty,
				"value": false,
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	got, err := client.DetectXrayTestIssueTypes(context.Background(), []JiraIssueType{
		{ID: "10001", Name: "Renamed acceptance scenario"},
		{ID: "10002", Name: "Test Case"},
		{ID: "10003", Name: "Test"},
		{ID: "10004", Name: "Another renamed Xray type"},
	})
	if err != nil {
		t.Fatalf("DetectXrayTestIssueTypes() error = %v", err)
	}
	if want := []string{"10001", "10004"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectXrayTestIssueTypes() = %#v, want %#v", got, want)
	}
	for idx, method := range methods {
		if method != http.MethodGet {
			t.Fatalf("request %d method = %s, want GET", idx, method)
		}
	}
}

func TestDataCenterClientListsXrayTestsThroughRavenWithoutIssueTypeNames(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer data-center-pat" {
			t.Fatalf("Authorization = %q, want existing Jira credential", got)
		}
		if r.URL.Path != "/rest/raven/2.0/api/test" {
			t.Fatalf("path = %s, want Raven test export", r.URL.Path)
		}
		if got := r.URL.Query().Get("jql"); got != `project = "QA" AND statusCategory != Done ORDER BY created ASC` {
			t.Fatalf("jql = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"key": "QA-7"},
			{"key": "QA-11"},
		})
	}))
	defer server.Close()

	client := &dataCenterClient{
		xrayURL:    server.URL + "/rest/raven/2.0/api",
		authHeader: "Bearer data-center-pat",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	got, err := client.ListXrayTestKeys(context.Background(), "QA", nil, true)
	if err != nil {
		t.Fatalf("ListXrayTestKeys() error = %v", err)
	}
	if want := []string{"QA-7", "QA-11"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListXrayTestKeys() = %#v, want %#v", got, want)
	}
}

func TestDataCenterClientReadsOrderedXrayManualSteps(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/raven/2.0/api/test/QA-7/step" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id": 2, "index": 2, "step": "Submit", "data": "", "result": "Dashboard opens"},
			{"id": 1, "index": 1, "step": "Open sign in", "data": "alice@example.com", "result": "Form is shown"}
		]`))
	}))
	defer server.Close()

	client := &dataCenterClient{
		xrayURL:    server.URL + "/rest/raven/2.0/api",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}
	got, err := client.GetXrayTestSteps(context.Background(), "QA-7")
	if err != nil {
		t.Fatalf("GetXrayTestSteps() error = %v", err)
	}
	want := []xray.Step{
		{Action: "Open sign in", Data: "alice@example.com", Expected: "Form is shown"},
		{Action: "Submit", Expected: "Dashboard opens"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetXrayTestSteps() = %#v, want %#v", got, want)
	}
}
