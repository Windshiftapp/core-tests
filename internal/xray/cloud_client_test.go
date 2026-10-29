package xray

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCloudClientAuthenticatesAndReadsManualSteps(t *testing.T) {
	t.Parallel()

	var authenticateCalls, graphQLCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/authenticate":
			authenticateCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("authenticate method = %s", r.Method)
			}
			var credentials map[string]string
			if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
				t.Fatalf("decode credentials: %v", err)
			}
			if credentials["client_id"] != "client-id" || credentials["client_secret"] != "client-secret" {
				t.Fatalf("credentials = %#v", credentials)
			}
			_, _ = w.Write([]byte(`"bearer-token"`))
		case "/api/v2/graphql":
			graphQLCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("GraphQL method = %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer bearer-token" {
				t.Fatalf("Authorization = %q", got)
			}
			var request graphQLRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode GraphQL request: %v", err)
			}
			if got := request.Variables["issueIds"]; !reflect.DeepEqual(got, []any{"10001", "10002"}) {
				t.Fatalf("issueIds = %#v", got)
			}
			_, _ = w.Write([]byte(`{
				"data": {
					"getTests": {
						"results": [{
							"issueId": "10001",
							"testType": {"name": "Manual", "kind": "0"},
							"steps": [
								{"action": "Open sign in", "data": "alice@example.com", "result": "The form is shown"},
								{"action": "Submit", "data": "", "result": "The dashboard opens"}
							]
						}]
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newCloudClientWithBaseURL(server.URL, "client-id", "client-secret", server.Client())
	tests, err := client.GetTests(context.Background(), []string{"10001", "10002"})
	if err != nil {
		t.Fatalf("GetTests() error = %v", err)
	}
	if authenticateCalls != 1 || graphQLCalls != 1 {
		t.Fatalf("calls authenticate=%d graphql=%d", authenticateCalls, graphQLCalls)
	}
	want := []Test{{
		IssueID:      "10001",
		TestTypeName: "Manual",
		Steps: []Step{
			{Action: "Open sign in", Data: "alice@example.com", Expected: "The form is shown"},
			{Action: "Submit", Expected: "The dashboard opens"},
		},
	}}
	if !reflect.DeepEqual(tests, want) {
		t.Fatalf("GetTests() = %#v, want %#v", tests, want)
	}
}

func TestCloudClientBatchesAtOneHundredTests(t *testing.T) {
	t.Parallel()

	graphQLCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/authenticate":
			_, _ = w.Write([]byte(`"token"`))
		case "/api/v2/graphql":
			graphQLCalls++
			_, _ = w.Write([]byte(`{"data":{"getTests":{"results":[]}}}`))
		}
	}))
	defer server.Close()

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = string(rune(i + 1))
	}
	client := newCloudClientWithBaseURL(server.URL, "id", "secret", server.Client())
	if _, err := client.GetTests(context.Background(), ids); err != nil {
		t.Fatalf("GetTests() error = %v", err)
	}
	if graphQLCalls != 2 {
		t.Fatalf("GraphQL calls = %d, want 2", graphQLCalls)
	}
}

func TestCloudClientRetriesRateLimitedGraphQLRequest(t *testing.T) {
	t.Parallel()

	graphQLCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/authenticate":
			_, _ = w.Write([]byte(`"token"`))
		case "/api/v2/graphql":
			graphQLCalls++
			if graphQLCalls == 1 {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"slow down"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"getTests":{"results":[]}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newCloudClientWithBaseURL(server.URL, "id", "secret", server.Client())
	var waited []time.Duration
	client.wait = func(_ context.Context, delay time.Duration) error {
		waited = append(waited, delay)
		return nil
	}

	if _, err := client.GetTests(context.Background(), []string{"10001"}); err != nil {
		t.Fatalf("GetTests() error = %v", err)
	}
	if graphQLCalls != 2 {
		t.Fatalf("GraphQL calls = %d, want 2", graphQLCalls)
	}
	if !reflect.DeepEqual(waited, []time.Duration{7 * time.Second}) {
		t.Fatalf("retry delays = %#v, want [7s]", waited)
	}
}

func TestCloudClientReportsAuthenticationFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/authenticate" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`invalid client credentials`))
	}))
	defer server.Close()

	client := newCloudClientWithBaseURL(server.URL, "id", "bad-secret", server.Client())
	err := client.Validate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("Validate() error = %v, want status 401", err)
	}
}
