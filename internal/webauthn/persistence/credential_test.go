//go:build test

package persistence

import (
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"

	"windshift/internal/testutils"
)

func TestCredentialStoresRoundTripTheirFixedSchemas(t *testing.T) {
	db := testutils.CreateTestDB(t, true)
	defer func() { _ = db.Close() }()

	var userID int
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, "webauthn-user@example.com", "webauthn-user", "WebAuthn", "User").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var customerID int
	if err := db.QueryRow(`
		INSERT INTO portal_customers (name, email)
		VALUES (?, ?)
		RETURNING id
	`, "WebAuthn Customer", "webauthn-customer@example.com").Scan(&customerID); err != nil {
		t.Fatalf("insert portal customer: %v", err)
	}

	internalStore := NewInternalCredentialStore(db)
	portalStore := NewPortalCredentialStore(db)
	internalCredential := testCredential([]byte{1, 2, 3})
	portalCredential := testCredential([]byte{4, 5, 6})

	if err := internalStore.SaveCredential(userID, "Internal laptop", internalCredential); err != nil {
		t.Fatalf("save internal credential: %v", err)
	}
	if err := portalStore.SaveCredential(customerID, "Portal phone", portalCredential); err != nil {
		t.Fatalf("save portal credential: %v", err)
	}

	gotInternal, err := internalStore.GetCredentials(userID)
	if err != nil {
		t.Fatalf("get internal credentials: %v", err)
	}
	assertCredential(t, gotInternal, internalCredential)
	gotPortal, err := portalStore.GetCredentials(customerID)
	if err != nil {
		t.Fatalf("get portal credentials: %v", err)
	}
	assertCredential(t, gotPortal, portalCredential)

	internalRecords, err := internalStore.GetCredentialRecords(userID)
	if err != nil {
		t.Fatalf("get internal credential records: %v", err)
	}
	if len(internalRecords) != 1 || internalRecords[0].CredentialName != "Internal laptop" {
		t.Fatalf("internal records = %+v, want one named record", internalRecords)
	}
	portalRecords, err := portalStore.GetCredentialRecords(customerID)
	if err != nil {
		t.Fatalf("get portal credential records: %v", err)
	}
	if len(portalRecords) != 1 || portalRecords[0].CredentialName != "Portal phone" {
		t.Fatalf("portal records = %+v, want one named record", portalRecords)
	}

	if exists, err := internalStore.CheckCredentialExists(internalCredential.ID); err != nil || !exists {
		t.Fatalf("internal credential existence = %v, err=%v; want true", exists, err)
	}
	if exists, err := portalStore.CheckCredentialExists(portalCredential.ID); err != nil || !exists {
		t.Fatalf("portal credential existence = %v, err=%v; want true", exists, err)
	}
	if owner, err := internalStore.LookupOwnerByCredentialID("AQID"); err != nil || owner != userID {
		t.Fatalf("internal credential owner = %d, err=%v; want %d", owner, err, userID)
	}
	if owner, err := portalStore.LookupOwnerByCredentialID("BAUG"); err != nil || owner != customerID {
		t.Fatalf("portal credential owner = %d, err=%v; want %d", owner, err, customerID)
	}

	if err := internalStore.UpdateCredentialCounter(internalCredential.ID, 9, true); err != nil {
		t.Fatalf("update internal credential counter: %v", err)
	}
	updated, err := internalStore.GetCredentialRecords(userID)
	if err != nil {
		t.Fatalf("get updated internal credential: %v", err)
	}
	if updated[0].SignCount != 9 || !updated[0].CloneWarning || updated[0].LastUsedAt == nil {
		t.Fatalf("updated internal record = %+v, want counter, clone warning, and last-used timestamp", updated[0])
	}

	if count, err := portalStore.CountCredentials(customerID); err != nil || count != 1 {
		t.Fatalf("portal credential count = %d, err=%v; want 1", count, err)
	}
	if err := portalStore.DeleteCredential("BAUG"); err != nil {
		t.Fatalf("delete portal credential: %v", err)
	}
	if exists, err := portalStore.CheckCredentialExists(portalCredential.ID); err != nil || exists {
		t.Fatalf("deleted portal credential existence = %v, err=%v; want false", exists, err)
	}
}

func testCredential(id []byte) *webauthnlib.Credential {
	return &webauthnlib.Credential{
		ID:              id,
		PublicKey:       []byte("public-key"),
		AttestationType: "none",
		Transport:       []protocol.AuthenticatorTransport{"internal"},
		Flags: webauthnlib.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: true,
			BackupState:    true,
		},
		Authenticator: webauthnlib.Authenticator{
			AAGUID:       []byte("aaguid"),
			SignCount:    3,
			CloneWarning: false,
		},
	}
}

func assertCredential(t *testing.T, got []webauthnlib.Credential, want *webauthnlib.Credential) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("credential count = %d, want 1", len(got))
	}
	if string(got[0].ID) != string(want.ID) || string(got[0].PublicKey) != string(want.PublicKey) || got[0].AttestationType != want.AttestationType {
		t.Fatalf("credential identity = %+v, want id=%q public key=%q attestation=%q", got[0], want.ID, want.PublicKey, want.AttestationType)
	}
	if len(got[0].Transport) != 1 || got[0].Transport[0] != want.Transport[0] {
		t.Fatalf("credential transports = %v, want %v", got[0].Transport, want.Transport)
	}
	if got[0].Flags != want.Flags || got[0].Authenticator.SignCount != want.Authenticator.SignCount || got[0].Authenticator.CloneWarning != want.Authenticator.CloneWarning || string(got[0].Authenticator.AAGUID) != string(want.Authenticator.AAGUID) {
		t.Fatalf("credential flags/authenticator = %+v/%+v, want %+v/sign-count=%d clone-warning=%t aaguid=%q", got[0].Flags, got[0].Authenticator, want.Flags, want.Authenticator.SignCount, want.Authenticator.CloneWarning, want.Authenticator.AAGUID)
	}
}
