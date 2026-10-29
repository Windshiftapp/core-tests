//go:build test

package data

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLoadAgentRunsDecodesV2Page(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []AgentRun
	}{
		{
			name: "latest runs with more history",
			body: `{"data":{"runs":[{"id":12,"status":"failed","job_kind":"coding","queued_at":"2026-09-06T09:00:00Z","started_at":"2026-09-06T09:01:00Z","ended_at":"2026-09-06T09:02:00Z","error":"\u001b[31mfailed\u001b[0m"},{"id":11,"status":"queued","job_kind":"coding","queued_at":"2026-09-06T08:00:00Z","started_at":null,"ended_at":null}],"has_more":true,"next_cursor":"older-runs"}}`,
			want: []AgentRun{
				{ID: 12, Status: "failed", JobKind: "coding", QueuedAt: "2026-09-06T09:00:00Z", StartedAt: "2026-09-06T09:01:00Z", EndedAt: "2026-09-06T09:02:00Z", Error: "failed"},
				{ID: 11, Status: "queued", JobKind: "coding", QueuedAt: "2026-09-06T08:00:00Z"},
			},
		},
		{name: "empty history", body: `{"data":{"runs":[],"has_more":false}}`, want: []AgentRun{}},
		{name: "null history", body: `{"data":{"runs":null,"has_more":false}}`, want: []AgentRun{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/rest/api/v2/items/840/agent-runs?page_size=10" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			client := NewClient(server.URL)
			client.SetBearerToken("test-token")
			got := LoadAgentRuns(client, 840, 7)()
			want := AgentRunsLoadedMsg{ItemID: 840, RequestID: 7, Runs: tc.want}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("message = %#v, want %#v", got, want)
			}
		})
	}
}

func TestLoadAgentRunsPreservesDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"Access denied"}}`)
	}))
	t.Cleanup(server.Close)
	got := LoadAgentRuns(NewClient(server.URL), 840, 7)()
	want := ErrorMsg{Operation: OpAgentRuns, ItemID: 840, RequestID: 7, Err: `API error: 403 Forbidden - {"error":{"code":"forbidden","message":"Access denied"}}`}
	if got != want {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
}
