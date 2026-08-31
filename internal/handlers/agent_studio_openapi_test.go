//go:build test

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestAgentStudioOpenAPIYAMLIsValidAndDocumentsScopedSurfaces(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/agent-studio/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	AgentStudioOpenAPIYAML(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/yaml; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(rr.Body.Bytes())
	if err != nil {
		t.Fatalf("parse Agent Studio OpenAPI: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate Agent Studio OpenAPI: %v", err)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "/api" {
		t.Fatalf("servers = %+v, want /api session surface", doc.Servers)
	}
	for _, path := range []string{
		"/workspaces/{workspaceId}/agent-profiles",
		"/workspaces/{workspaceId}/agent-profiles/{id}/test",
		"/workspaces/{workspaceId}/agent-runner-pools/{poolId}/tokens",
		"/workspaces/{workspaceId}/agent-sessions",
		"/ai/chat",
		"/agent-runs/{id}/cancel",
		"/admin/audit-logs/{id}/agent-transcript",
	} {
		if doc.Paths.Find(path) == nil {
			t.Errorf("missing documented path %s", path)
		}
	}
	if doc.Components.SecuritySchemes["SessionCookie"] == nil ||
		doc.Components.SecuritySchemes["SessionHeader"] == nil {
		t.Fatal("session cookie and session header security schemes must both be documented")
	}
}
