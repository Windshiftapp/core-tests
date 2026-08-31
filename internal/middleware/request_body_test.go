//go:build test

package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/restapi"
)

func TestLimitJSONRequestBodyRejectsKnownOversizedBodies(t *testing.T) {
	const limit = int64(16)

	for _, contentType := range []string{"application/json", "application/problem+json"} {
		t.Run(contentType, func(t *testing.T) {
			called := false
			handler := LimitJSONRequestBody(limit)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))

			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/example", strings.NewReader(strings.Repeat("x", int(limit)+1)))
			r.Header.Set("Content-Type", contentType)

			handler.ServeHTTP(rec, r)

			if called {
				t.Fatal("next handler was called for an oversized request")
			}
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
			var response restapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if response.Code != restapi.ErrCodeRequestTooLarge {
				t.Fatalf("code = %q, want %q", response.Code, restapi.ErrCodeRequestTooLarge)
			}
		})
	}
}

func TestLimitJSONRequestBodyCapsStreamingBodies(t *testing.T) {
	const limit = int64(16)
	var readErr error
	handler := LimitJSONRequestBody(limit)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/example", strings.NewReader(strings.Repeat("x", int(limit)+1)))
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = -1

	handler.ServeHTTP(rec, r)

	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Fatalf("read error = %v, want *http.MaxBytesError", readErr)
	}
}

func TestLimitJSONRequestBodyAllowsBodyAtLimit(t *testing.T) {
	const limit = int64(16)
	called := false
	handler := LimitJSONRequestBody(limit)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(body) != int(limit) {
			t.Fatalf("body length = %d, want %d", len(body), limit)
		}
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/example", strings.NewReader(strings.Repeat("x", int(limit))))
	r.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(rec, r)

	if !called {
		t.Fatal("next handler was not called for a request at the limit")
	}
}

func TestLimitJSONRequestBodyPreservesExemptAndNonJSONRequests(t *testing.T) {
	const limit = int64(16)

	tests := []struct {
		name        string
		path        string
		contentType string
	}{
		{name: "exempt JSON path", path: "/api/llm-proxy/12/complete", contentType: "application/json"},
		{name: "multipart upload", path: "/api/attachments", contentType: "multipart/form-data; boundary=test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var readBytes int
			handler := LimitJSONRequestBody(limit, "/api/llm-proxy/")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				readBytes = len(body)
			}))

			body := strings.Repeat("x", int(limit)+1)
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(body))
			r.Header.Set("Content-Type", tt.contentType)

			handler.ServeHTTP(rec, r)

			if readBytes != len(body) {
				t.Fatalf("read %d bytes, want %d", readBytes, len(body))
			}
		})
	}
}
