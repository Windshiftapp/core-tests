package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
)

func TestExecuteActionRejectsUnauthenticatedRequestsBeforeResourceLookup(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/1/actions/1/execute", nil)

	(&ActionsHandler{}).ExecuteAction(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestSanitizeCapabilitiesForWorkspaceFailsClosedOnMalformedHTTPConfig(t *testing.T) {
	t.Parallel()

	original := &models.ActionCapability{
		CapabilityType: models.CapabilityHTTPClient,
		Config:         `{"default_headers":{"Authorization":"plaintext"}`,
	}
	got := sanitizeCapabilitiesForWorkspace([]*models.ActionCapability{original})
	if got[0].Config != "{}" {
		t.Fatalf("sanitized config = %q, want {}", got[0].Config)
	}
	if original.Config == "{}" {
		t.Fatal("sanitizer mutated repository model in place")
	}
}

func TestSanitizeCapabilitiesForWorkspaceRedactsLegacySecretsAndCredentialIDs(t *testing.T) {
	t.Parallel()

	config := models.HTTPClientConfig{
		AllowedURLPatterns: []string{"https://example.test/**"},
		DefaultHeaders: map[string]string{
			"Accept":        "application/json",
			"Authorization": "plaintext",
			"X-Signature":   "plaintext-signature",
		},
		Auth: &models.HTTPAuthRef{
			CredentialID: 42,
			Placement:    "header",
			HeaderName:   "Authorization",
			Scheme:       "Bearer",
		},
		SecretHeaderRefs: map[string]int{"X-API-Key": 99},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got := sanitizeCapabilitiesForWorkspace([]*models.ActionCapability{{
		CapabilityType: models.CapabilityHTTPClient,
		Config:         string(raw),
	}})
	var sanitized models.HTTPClientConfig
	if err := json.Unmarshal([]byte(got[0].Config), &sanitized); err != nil {
		t.Fatalf("unmarshal sanitized config: %v", err)
	}
	if len(sanitized.DefaultHeaders) != 1 || sanitized.DefaultHeaders["Accept"] != "[REDACTED]" {
		t.Fatalf("default headers = %#v, want one redacted Accept value", sanitized.DefaultHeaders)
	}
	if sanitized.Auth == nil || sanitized.Auth.CredentialID != 0 {
		t.Fatalf("auth = %#v, want redacted credential ID", sanitized.Auth)
	}
	if sanitized.Auth.Scheme != "" {
		t.Fatalf("auth scheme = %q, want redacted", sanitized.Auth.Scheme)
	}
	if sanitized.SecretHeaderRefs["X-API-Key"] != 1 {
		t.Fatalf("secret refs = %#v, want presence sentinel", sanitized.SecretHeaderRefs)
	}
}

func TestSanitizeCapabilitiesForWorkspaceRedactsDockerEnvironmentValues(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(models.DockerEnvironmentConfig{
		Image: "example.test/worker:latest",
		EnvVars: map[string]string{
			"API_TOKEN": "plaintext-token",
			"REGION":    "eu-central-1",
		},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got := sanitizeCapabilitiesForWorkspace([]*models.ActionCapability{{
		CapabilityType: models.CapabilityDockerEnvironment,
		Config:         string(raw),
	}})
	var sanitized models.DockerEnvironmentConfig
	if err := json.Unmarshal([]byte(got[0].Config), &sanitized); err != nil {
		t.Fatalf("unmarshal sanitized config: %v", err)
	}
	for key, value := range sanitized.EnvVars {
		if value != "[REDACTED]" {
			t.Fatalf("environment value %s = %q, want redacted", key, value)
		}
	}
}
