//go:build test

package wscli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runWorklogAdd(t *testing.T, serverURL string, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	commandArgs := append([]string{"time", "worklog", "add"}, args...)
	code := Run(context.Background(), commandArgs, nil, &out, &errOut, map[string]string{
		"WS_URL":   serverURL,
		"WS_TOKEN": "test-token",
	})
	return code, errOut.String()
}

func TestTimeWorklogAddResolvesItemKeyBeforeCreate(t *testing.T) {
	var paths []string
	var createBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/workspaces/WI/items/42":
			_, _ = io.WriteString(w, `{"data":{"id":77}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/v2/time/worklogs":
			createBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"data":{"id":9,"project_name":"Core","date":"2026-09-05","duration_minutes":60}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	code, stderr := runWorklogAdd(t, server.URL,
		"--project-id", "3",
		"--description", "Review",
		"--date", "2026-09-05",
		"--duration", "1h",
		"--item-key", "WI-42",
	)

	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got, want := strings.Join(paths, ", "), "GET /rest/api/v2/workspaces/WI/items/42, POST /rest/api/v2/time/worklogs"; got != want {
		t.Fatalf("requests = %q, want %q", got, want)
	}
	if got, want := string(createBody), `{"project_id":3,"description":"Review","date":"2026-09-05","duration":"1h","item_id":77}`; got != want {
		t.Fatalf("create body = %s, want %s", got, want)
	}
}

func TestTimeWorklogAddDoesNotCreateWhenItemKeyCannotBeResolved(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var createRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					createRequests++
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"item not found"}}`)
			}))
			t.Cleanup(server.Close)

			code, _ := runWorklogAdd(t, server.URL,
				"--project-id", "3",
				"--description", "Review",
				"--date", "2026-09-05",
				"--duration", "1h",
				"--item-key", "WI-404",
			)

			if code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			if createRequests != 0 {
				t.Fatalf("create requests = %d, want 0", createRequests)
			}
		})
	}
}

func TestTimeWorklogAddRejectsBothItemIdentifiers(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	code, stderr := runWorklogAdd(t, server.URL,
		"--project-id", "3",
		"--description", "Review",
		"--date", "2026-09-05",
		"--duration", "1h",
		"--item-id", "77",
		"--item-key", "WI-42",
	)

	if code != 1 || !strings.Contains(stderr, "item-id") || !strings.Contains(stderr, "item-key") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestTimeWorklogAddSendsOnlyItemID(t *testing.T) {
	var body map[string]any
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":9}}`)
	}))
	t.Cleanup(server.Close)

	code, stderr := runWorklogAdd(t, server.URL,
		"--project-id", "3",
		"--description", "Review",
		"--date", "2026-09-05",
		"--duration", "1h",
		"--item-id", "77",
	)

	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if len(paths) != 1 || paths[0] != "/rest/api/v2/time/worklogs" {
		t.Fatalf("paths = %v", paths)
	}
	if got := body["item_id"]; got != float64(77) {
		t.Fatalf("item_id = %#v, want 77", got)
	}
	if _, ok := body["item_key"]; ok {
		t.Fatalf("unexpected item_key in body: %v", body)
	}
}
