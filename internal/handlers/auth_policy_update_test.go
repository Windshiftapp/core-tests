//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/testutils"
)

func TestUpdateAuthPolicyStillRequiresAdminPasskeyForPasskeyOnly(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()

	adminID := insertPolicyTestUser(t, db, "passkey-admin@example.com", "passkey-admin")
	var adminPermissionID int
	if err := db.QueryRow(`SELECT id FROM permissions WHERE permission_key = 'system.admin'`).Scan(&adminPermissionID); err != nil {
		t.Fatalf("find system.admin permission: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO user_global_permissions (user_id, permission_id) VALUES (?, ?)`, adminID, adminPermissionID); err != nil {
		t.Fatalf("grant system.admin: %v", err)
	}

	handler := NewAuthPolicyHandlerWithFallback(db, false, logger.NewAuditor(db))
	response := updateAuthPolicy(t, handler, AuthPolicyPasskeyOnly, false)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("admin without passkey status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var apiError struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode admin without passkey response: %v; body=%s", err, response.Body.String())
	}
	const wantError = "Cannot enable this policy: some administrators do not have passkeys enrolled. Enable --enable-fallback flag or ensure all admins have passkeys."
	if apiError.Error != wantError {
		t.Fatalf("admin without passkey error = %q, want %q", apiError.Error, wantError)
	}

	if _, err := db.ExecWrite(`
		INSERT INTO webauthn_credentials (id, user_id, credential_name, public_key)
		VALUES (?, ?, ?, ?)
	`, "auth-policy-admin-passkey", adminID, "Admin passkey", []byte{1, 2, 3}); err != nil {
		t.Fatalf("insert admin passkey: %v", err)
	}

	response = updateAuthPolicy(t, handler, AuthPolicyPasskeyOnly, false)
	if response.Code != http.StatusOK {
		t.Fatalf("admin with passkey status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
}

func insertPolicyTestUser(t *testing.T, db database.Database, email, username string) int {
	t.Helper()
	return testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, password_hash, is_active, email_verified, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, true, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, email, username, "Policy", "Admin", "unused")
}

func updateAuthPolicy(t *testing.T, handler *AuthPolicyHandler, policy AuthPolicy, previewMode bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"policy":       policy,
		"preview_mode": previewMode,
	})
	if err != nil {
		t.Fatalf("marshal auth policy: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/admin/auth-policy", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.UpdateAuthPolicy(response, request)
	return response
}
