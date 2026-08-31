package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/database"
	"windshift/internal/utils"
	wswebauthn "windshift/internal/webauthn"
)

func TestStartFIDOLoginDoesNotEnumerateAccountOrCredentialState(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "webauthn-enumeration.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	activeID := insertWebAuthnTestUser(t, db, "active@example.com", "active", true)
	insertWebAuthnTestUser(t, db, "no-passkey@example.com", "no-passkey", true)
	insertWebAuthnTestUser(t, db, "inactive@example.com", "inactive", false)
	credentialID := base64.RawURLEncoding.EncodeToString([]byte("enumeration-test-credential"))
	_, err = db.ExecWrite(`
		INSERT INTO webauthn_credentials (
			id, user_id, credential_name, public_key, attestation_type, aaguid,
			sign_count, clone_warning, transport, flags_user_present,
			flags_user_verified, flags_backup_eligible, flags_backup_state
		) VALUES (?, ?, ?, ?, ?, ?, 0, false, ?, true, true, false, false)
	`, credentialID, activeID, "Test passkey", []byte{1, 2, 3}, "none", []byte{}, `["internal"]`)
	if err != nil {
		t.Fatalf("insert WebAuthn credential: %v", err)
	}

	config, err := wswebauthn.NewConfig(wswebauthn.Options{
		RPID:          "localhost",
		RPName:        "Windshift Test",
		Origins:       []string{"http://localhost"},
		IsDevelopment: true,
	})
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	sessionManager := auth.NewSessionManager(db, false, false, nil, "webauthn-test-secret", "strict")
	handler := NewWebAuthnHandler(db, nil, sessionManager, config, utils.NewIPExtractor(false, nil))

	for _, identifier := range []string{"active", "no-passkey", "inactive", "unknown"} {
		t.Run(identifier, func(t *testing.T) {
			var firstCredentialIDs []string
			for attempt := 0; attempt < 2; attempt++ {
				body, _ := json.Marshal(FIDOLoginRequestNew{EmailOrUsername: identifier})
				request := httptest.NewRequest(http.MethodPost, "/api/auth/webauthn/login/start", bytes.NewReader(body))
				request.RemoteAddr = "198.51.100.40:5678"
				response := httptest.NewRecorder()
				handler.StartFIDOLoginNew(response, request)

				if response.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
				}
				var payload struct {
					SessionID string `json:"sessionId"`
					PublicKey struct {
						AllowCredentials []json.RawMessage `json:"allowCredentials"`
						Challenge        string            `json:"challenge"`
					} `json:"publicKey"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if payload.SessionID == "" || payload.PublicKey.Challenge == "" {
					t.Fatalf("response missing synthetic session/challenge: %+v", payload)
				}
				if len(payload.PublicKey.AllowCredentials) != maxCredentialsPerUser {
					t.Fatalf("allowCredentials count = %d, want %d", len(payload.PublicKey.AllowCredentials), maxCredentialsPerUser)
				}
				credentialIDs := make([]string, 0, len(payload.PublicKey.AllowCredentials))
				for _, rawDescriptor := range payload.PublicKey.AllowCredentials {
					var descriptor map[string]interface{}
					if err := json.Unmarshal(rawDescriptor, &descriptor); err != nil {
						t.Fatalf("decode credential descriptor: %v", err)
					}
					if _, exposed := descriptor["transports"]; exposed {
						t.Fatalf("credential descriptor exposed account-specific transport hints: %s", rawDescriptor)
					}
					credentialIDs = append(credentialIDs, descriptor["id"].(string))
				}
				if attempt == 0 {
					firstCredentialIDs = credentialIDs
				} else if !reflect.DeepEqual(credentialIDs, firstCredentialIDs) {
					t.Fatal("repeated starts exposed real credentials through unstable decoys")
				}
			}
		})
	}
}

func insertWebAuthnTestUser(t *testing.T, db database.Database, email, username string, active bool) int {
	t.Helper()
	result, err := db.ExecWrite(`
		INSERT INTO users (email, username, first_name, last_name, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, email, username, "WebAuthn", "Test", active)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	id, _ := result.LastInsertId()
	return int(id)
}
