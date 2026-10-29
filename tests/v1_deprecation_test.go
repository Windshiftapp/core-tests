package tests

import (
	"net/http"
	"testing"
)

func TestV1ResponsesAdvertiseV2Successor(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.BaseURL+"/rest/api/v1/openapi.json", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	response, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("request v1 OpenAPI: %v", err)
	}
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	if got := response.Header.Get("Deprecation"); got != "true" {
		t.Fatalf("Deprecation = %q, want true", got)
	}
	if got := response.Header.Get("Link"); got != `</rest/api/v2/openapi.json>; rel="successor-version"` {
		t.Fatalf("Link = %q", got)
	}
}
