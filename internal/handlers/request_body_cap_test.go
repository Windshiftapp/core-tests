//go:build test

package handlers

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

func TestDecodeJSON_OversizedBodyReturns413(t *testing.T) {
	type requestBody struct {
		Value string `json:"value"`
	}

	body := `{"value":"` + strings.Repeat("a", int(restapi.DefaultJSONRequestBodyLimit)) + `"}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/example", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// Exercise the streaming path rather than relying on Content-Length.
	r.ContentLength = -1

	_, ok := decodeJSON[requestBody](rec, r)
	if ok {
		t.Fatal("decodeJSON accepted an oversized request body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %q)", rec.Code, rec.Body.String())
	}

	var response restapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != restapi.ErrCodeRequestTooLarge {
		t.Fatalf("code = %q, want %q", response.Code, restapi.ErrCodeRequestTooLarge)
	}
}

func TestDecodeJSONAcceptsOneMiBPageContent(t *testing.T) {
	type requestBody struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}

	body := `{"title":"Large page","content":"` + strings.Repeat("a", 1<<20) + `"}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/workspaces/1/pages/1", strings.NewReader(body))
	r.ContentLength = -1

	got, ok := decodeJSON[requestBody](rec, r)
	if !ok {
		t.Fatalf("decodeJSON rejected a 1 MiB page payload: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if len(got.Content) != 1<<20 {
		t.Fatalf("content length = %d, want %d", len(got.Content), 1<<20)
	}
}

func TestIsRequestBodyTooLarge(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 1024)))
	r.Body = http.MaxBytesReader(rec, r.Body, 16)

	_, err := io.ReadAll(r.Body)
	if err == nil {
		t.Fatal("expected MaxBytesReader to error on oversized body")
	}
	if !isRequestBodyTooLarge(err) {
		t.Fatalf("isRequestBodyTooLarge(%v) = false, want true", err)
	}

	if isRequestBodyTooLarge(errors.New("some other error")) {
		t.Fatal("isRequestBodyTooLarge should be false for unrelated errors")
	}
	if isRequestBodyTooLarge(io.EOF) {
		t.Fatal("isRequestBodyTooLarge should be false for io.EOF")
	}
}

// The OAuth /token endpoint caps the body before parsing, so an oversized
// request is rejected with 413 before any client/DB work — the handler can be
// exercised with zero dependencies.
func TestOAuthToken_OversizedBodyReturns413(t *testing.T) {
	h := &OAuthHandler{}

	big := "grant_type=authorization_code&pad=" + strings.Repeat("a", 70<<10)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader(big))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	h.Token(rec, r)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestOAuthToken_SmallBodyNotCapped(t *testing.T) {
	h := &OAuthHandler{}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/oauth/token", strings.NewReader("grant_type="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	h.Token(rec, r)

	// A small, well-formed-but-empty grant_type request must not trip the cap;
	// it should fall through to the normal 400 "grant_type is required" path.
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("small body unexpectedly returned 413")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
}
