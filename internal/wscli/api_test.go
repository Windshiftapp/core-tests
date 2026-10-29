//go:build test

package wscli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type observedAPIRequest struct {
	method      string
	path        string
	rawQuery    string
	authorize   string
	accept      string
	contentType string
	ifMatch     string
	firstHeader string
	body        string
}

func observeAPIRequest(t *testing.T, r *http.Request) observedAPIRequest {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return observedAPIRequest{
		method:      r.Method,
		path:        r.URL.Path,
		rawQuery:    r.URL.RawQuery,
		authorize:   r.Header.Get("Authorization"),
		accept:      r.Header.Get("Accept"),
		contentType: r.Header.Get("Content-Type"),
		ifMatch:     r.Header.Get("If-Match"),
		firstHeader: r.Header.Get("X-First"),
		body:        string(body),
	}
}

func TestAPICommandCallsV2EndpointAndPreservesResponse(t *testing.T) {
	requests := make(chan observedAPIRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observeAPIRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":42}}`)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items/42/history?page_size=7"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}
	if got, want := out.String(), `{"data":{"id":42}}`; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	got := <-requests
	if got.method != http.MethodGet || got.path != "/rest/api/v2/items/42/history" || got.rawQuery != "page_size=7" {
		t.Fatalf("request target = %s %s?%s", got.method, got.path, got.rawQuery)
	}
	if got.authorize != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got.authorize)
	}
	if got.accept != "application/json" {
		t.Fatalf("Accept = %q", got.accept)
	}
	if got.contentType != "" {
		t.Fatalf("bodyless GET Content-Type = %q, want empty", got.contentType)
	}
}

func TestAPICommandSendsMergePatchAndIncludesResponseHeaders(t *testing.T) {
	requests := make(chan observedAPIRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observeAPIRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"item-5"`)
		_, _ = io.WriteString(w, `{"data":{"title":"Updated"}}`)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"api", "/rest/api/v2/items/42",
		"-X", "patch",
		"-H", `If-Match: "item-4"`,
		"-H", "Accept: application/json, application/problem+json",
		"-d", `{"title":"Updated"}`,
		"--include",
	}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}

	got := <-requests
	if got.method != http.MethodPatch || got.path != "/rest/api/v2/items/42" {
		t.Fatalf("request target = %s %s", got.method, got.path)
	}
	if got.contentType != "application/merge-patch+json" {
		t.Fatalf("Content-Type = %q", got.contentType)
	}
	if got.ifMatch != `"item-4"` {
		t.Fatalf("If-Match = %q", got.ifMatch)
	}
	if got.accept != "application/json, application/problem+json" {
		t.Fatalf("Accept = %q", got.accept)
	}
	if got.body != `{"title":"Updated"}` {
		t.Fatalf("body = %q", got.body)
	}
	for _, want := range []string{"HTTP/1.1 200 OK\r\n", `Etag: "item-5"`, `{"data":{"title":"Updated"}}`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("included response missing %q:\n%s", want, out.String())
		}
	}
}

func TestAPICommandReadsBodyFromStdin(t *testing.T) {
	requests := make(chan observedAPIRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observeAPIRequest(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "items/bulk-update", "-X", "POST", "--input", "-"}, strings.NewReader(`{"ids":[1,2]}`), &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws api returned %d: %s", code, errOut.String())
	}
	got := <-requests
	if got.path != "/rest/api/v2/items/bulk-update" || got.body != `{"ids":[1,2]}` {
		t.Fatalf("request = %+v", got)
	}
	if got.contentType != "application/json" {
		t.Fatalf("Content-Type = %q", got.contentType)
	}
	if out.Len() != 0 {
		t.Fatalf("204 response stdout = %q, want empty", out.String())
	}
}

func TestAPICommandReturnsHTTPFailureAfterWritingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":{"code":"validation_failed","message":"invalid title"}}`)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items/42"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 1 {
		t.Fatalf("ws api returned %d, want 1", code)
	}
	if got, want := out.String(), `{"error":{"code":"validation_failed","message":"invalid title"}}`; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(errOut.String(), "API request failed: 422 Unprocessable Entity") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestAPICommandRejectsAbsoluteURLAndAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "absolute URL", args: []string{"api", "https://example.com/rest/api/v2/items"}, want: "must be a path"},
		{name: "REST API v1", args: []string{"api", "/rest/api/v1/items"}, want: "only /rest/api/v2"},
		{name: "session API v2", args: []string{"api", "/api/v2/items"}, want: "only /rest/api/v2"},
		{name: "path traversal", args: []string{"api", "/rest/api/v2/../../../api/v2/items"}, want: "dot path segments"},
		{name: "authorization header", args: []string{"api", "/items", "-H", "Authorization: Bearer other"}, want: "authorization is managed by ws"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(context.Background(), test.args, nil, &out, &errOut, map[string]string{
				"WS_URL":   "http://127.0.0.1:1",
				"WS_TOKEN": "secret-token",
			})
			if code != 1 || !strings.Contains(errOut.String(), test.want) {
				t.Fatalf("code=%d stderr=%q, want %q", code, errOut.String(), test.want)
			}
		})
	}
}

func TestAPICommandFlagsDoNotLeakBetweenRuns(t *testing.T) {
	requests := make(chan observedAPIRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observeAPIRequest(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	env := map[string]string{"WS_URL": server.URL, "WS_TOKEN": "secret-token"}

	for _, args := range [][]string{
		{"api", "/first", "-X", "POST", "-H", "X-First: yes", "-d", `{}`},
		{"api", "/second"},
	} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), args, nil, &out, &errOut, env); code != 0 {
			t.Fatalf("ws %s returned %d: %s", strings.Join(args, " "), code, errOut.String())
		}
	}
	<-requests
	second := <-requests
	if second.method != http.MethodGet || second.body != "" || second.firstHeader != "" {
		t.Fatalf("second request retained flags: %+v", second)
	}
}

func TestAPICommandDoesNotFollowRedirects(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		http.Redirect(w, r, "/api/v2/items", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"api", "/items"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 1 || !strings.Contains(errOut.String(), "API request failed: 302 Found") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	if got := <-requests; got != "/rest/api/v2/items" {
		t.Fatalf("request path = %q", got)
	}
	select {
	case got := <-requests:
		t.Fatalf("followed redirect to %q", got)
	default:
	}
}
