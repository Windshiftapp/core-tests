//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/restapi"
)

func TestDecodeBodyOrRespondRejectsOversizedBody(t *testing.T) {
	body := `{"value":"` + strings.Repeat("a", int(restapi.DefaultJSONRequestBodyLimit)) + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/example", strings.NewReader(body))
	req.ContentLength = -1

	var dst map[string]string
	if (&BaseHandler{}).DecodeBodyOrRespond(rec, req, &dst) {
		t.Fatal("DecodeBodyOrRespond accepted an oversized request body")
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
