//go:build test

package v2

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
)

func TestOptionalDistinguishesOmittedNullAndValue(t *testing.T) {
	t.Parallel()
	type patch struct {
		Name Optional[string] `json:"name"`
	}
	tests := []struct {
		name  string
		body  string
		set   bool
		null  bool
		value string
	}{
		{name: "omitted", body: `{}`},
		{name: "null", body: `{"name":null}`, set: true, null: true},
		{name: "value", body: `{"name":"new"}`, set: true, value: "new"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got patch
			if err := json.Unmarshal([]byte(test.body), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Name.Set != test.set || got.Name.Null != test.null || got.Name.Value != test.value {
				t.Fatalf("optional = %#v", got.Name)
			}
		})
	}
}

type fixedCSRF bool

func (f fixedCSRF) Allows(*http.Request) bool { return bool(f) }

func TestTransportPolicyFailuresUseCanonicalErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		middleware Middleware
		status     int
		code       string
		retryAfter string
	}{
		{name: "csrf", middleware: requireCSRF(fixedCSRF(false)), status: 403, code: "csrf_failed"},
		{name: "concurrency", middleware: limitConcurrency(fakeConcurrency{allow: false}), status: 429, code: "rate_limited", retryAfter: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Header.Set("X-Request-ID", "req_policy")
			ctx := context.WithValue(request.Context(), contextkeys.User, &models.User{ID: 42})
			response := httptest.NewRecorder()
			called := false
			handler := test.middleware(func(http.ResponseWriter, *http.Request) error {
				called = true
				return nil
			})
			Adapt(handler).ServeHTTP(response, request.WithContext(ctx))

			if called {
				t.Fatal("policy called the protected handler")
			}
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			var body errorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error.Code != test.code || body.RequestID != "req_policy" {
				t.Fatalf("body = %#v", body)
			}
			if response.Header().Get("Retry-After") != test.retryAfter {
				t.Fatalf("Retry-After = %q, want %q", response.Header().Get("Retry-After"), test.retryAfter)
			}
		})
	}
}

func TestCORSReturnsCanonicalDenialAndHandlesPreflight(t *testing.T) {
	t.Parallel()
	policy := NewCORS([]string{"https://app.example.test"}, false, true)
	handler := Adapt(policy(func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))

	denied := httptest.NewRequest(http.MethodGet, "/", nil)
	denied.Header.Set("Origin", "https://evil.example.test")
	denied.Header.Set("X-Request-ID", "req_cors")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denial status = %d", deniedResponse.Code)
	}
	var body errorBody
	if err := json.Unmarshal(deniedResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode denial: %v", err)
	}
	if body.Error.Code != "cors_origin_not_allowed" || body.RequestID != "req_cors" {
		t.Fatalf("denial body = %#v", body)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/", nil)
	preflight.Header.Set("Origin", "https://app.example.test")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflightResponse.Code)
	}
	if preflightResponse.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("credentialed preflight header is missing")
	}
}

func TestDecodeJSONIsStrict(t *testing.T) {
	t.Parallel()
	type requestBody struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name        string
		contentType string
		body        string
		patch       bool
		status      int
		code        string
	}{
		{name: "valid with charset", contentType: "application/json; charset=utf-8", body: `{"name":"ok"}`},
		{name: "unknown field", contentType: "application/json", body: `{"extra":true}`, status: 400, code: "invalid_request"},
		{name: "trailing value", contentType: "application/json", body: `{"name":"ok"} {}`, status: 400, code: "invalid_request"},
		{name: "wrong media type", contentType: "text/plain", body: `{"name":"ok"}`, status: 415, code: "unsupported_media_type"},
		{name: "patch media type", contentType: "application/merge-patch+json", body: `{"name":"ok"}`, patch: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			var target requestBody
			err := decodeJSON(response, request, &target, test.patch)
			if test.status == 0 {
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				return
			}
			apiErr, ok := err.(*Error)
			if !ok || apiErr.Status != test.status || apiErr.Code != test.code {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestPaginationRejectsInvalidAndOverflowingValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
	}{
		{name: "zero page", query: "page=0"},
		{name: "oversized page", query: "page_size=1001"},
		{name: "unsupported sort", query: "sort=created_at"},
		{name: "overflow", query: "page=" + strconv.Itoa(math.MaxInt) + "&page_size=100"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/?"+test.query, nil)
			_, err := ParsePagination(request, map[string]bool{"name": true}, "name")
			if err == nil {
				t.Fatal("expected pagination error")
			}
		})
	}
}

func TestPaginationAcceptsFullBoardPageSize(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/?page_size=1000", nil)
	page, err := ParsePage(request)
	if err != nil {
		t.Fatalf("page_size=1000 rejected: %v", err)
	}
	if page.PageSize != 1000 || page.Offset != 0 {
		t.Fatalf("page = %+v", page)
	}
}
