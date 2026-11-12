package jira

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

// TestCloudDownloadAttachmentRoutesThroughResolvedBase is the WI-2061
// regression: a Cloud attachment download must use the attachment-content
// endpoint under the client's resolved base (the gateway for scoped tokens)
// instead of the tenant-hosted content URL Jira returns.
func TestCloudDownloadAttachmentRoutesThroughResolvedBase(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Basic test-token" {
			t.Fatalf("Authorization = %q, want Basic test-token", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-bytes"))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		authHeader: "Basic test-token",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	// The tenant-hosted URL points at a host this test never starts; if the
	// client used it the download would fail instead of reaching the server.
	tenantURL := "https://tenant.example.invalid/rest/api/3/attachment/content/10001"
	body, contentType, err := client.DownloadAttachment(context.Background(), "10001", tenantURL)
	if err != nil {
		t.Fatalf("DownloadAttachment() error = %v", err)
	}
	defer func() { _ = body.Close() }()

	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(data) != "png-bytes" {
		t.Fatalf("body = %q, want png-bytes", data)
	}
	if len(paths) != 1 || paths[0] != "/rest/api/3/attachment/content/10001" {
		t.Fatalf("request paths = %v, want [/rest/api/3/attachment/content/10001]", paths)
	}
}

// TestCloudDownloadAttachmentFallsBackToContentURL covers attachments without
// an id: the client must still download from the supplied URL.
func TestCloudDownloadAttachmentFallsBackToContentURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("fallback"))
	}))
	t.Cleanup(server.Close)

	client := &cloudClient{
		baseURL:    server.URL + "/rest/api/3",
		authHeader: "Basic test-token",
		httpClient: server.Client(),
		limiter:    rate.NewLimiter(rate.Inf, 1),
	}

	body, _, err := client.DownloadAttachment(context.Background(), "", server.URL+"/legacy/content")
	if err != nil {
		t.Fatalf("DownloadAttachment() error = %v", err)
	}
	defer func() { _ = body.Close() }()
	data, _ := io.ReadAll(body)
	if string(data) != "fallback" {
		t.Fatalf("body = %q, want fallback", data)
	}
}
