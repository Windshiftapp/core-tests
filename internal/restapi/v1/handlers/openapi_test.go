package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAPISpecJSON_ServesValidJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rest/api/v1/openapi.json", nil)

	OpenAPISpecJSON(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type: got %q, want application/json", got)
	}
	if rr.Header().Get("Cache-Control") == "" {
		t.Error("expected Cache-Control header to be set")
	}
	body := rr.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("body is empty — embed may have failed")
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if _, ok := doc["openapi"]; !ok {
		t.Error("expected an `openapi` field in the spec document")
	}
}

func TestOpenAPISpecYAML_ServesContent(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rest/api/v1/openapi.yaml", nil)

	OpenAPISpecYAML(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/yaml" {
		t.Errorf("content-type: got %q, want application/yaml", got)
	}
	if rr.Body.Len() == 0 {
		t.Fatal("body is empty — embed may have failed")
	}
}
