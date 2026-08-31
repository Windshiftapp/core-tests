package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
)

func TestPasskeyEnrollmentEndpointAllowlist(t *testing.T) {
	allowed := []string{
		"/api/auth/me",
		"/api/auth/logout",
		"/api/users/42/credentials/webauthn/register/start",
		"/api/users/42/credentials/webauthn/register/complete",
	}
	for _, path := range allowed {
		if !isPendingAuthEndpoint(path, auth.AuthPendingEnrollment) {
			t.Errorf("expected %q to be allowed for enrollment", path)
		}
	}

	denied := []string{
		"/api/workspaces",
		"/api/auth/refresh",
		"/api/users/42/credentials",
		"/rest/api/v1/workspaces",
	}
	for _, path := range denied {
		if isPendingAuthEndpoint(path, auth.AuthPendingEnrollment) {
			t.Errorf("expected %q to be denied", path)
		}
	}

	if isPendingAuthEndpoint("/api/users/42/credentials/webauthn/register/start", auth.AuthPendingPasskeyVerification) {
		t.Fatal("passkey verification session was allowed to register a replacement passkey")
	}
	if !isPendingAuthEndpoint("/api/auth/logout", auth.AuthPendingPasskeyVerification) {
		t.Fatal("passkey verification session could not log out")
	}
}

func TestPendingAuthResponseIsForbiddenWithoutInvalidatingSession(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	new(AuthMiddleware).handleAuthPendingError(response, request, "Passkey enrollment required")

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	var payload authErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Code != "AUTHENTICATION_PENDING" {
		t.Fatalf("code = %q, want AUTHENTICATION_PENDING", payload.Code)
	}
}
