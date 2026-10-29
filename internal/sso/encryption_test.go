//go:build test

package sso

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"testing"
)

const testSecret = "test-server-secret-with-sufficient-length-for-derivation"

func TestSecretEncryption_RoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		serverSecret string
		plaintext    string
	}{
		{
			name:         "short secret",
			serverSecret: "short",
			plaintext:    "my-client-secret",
		},
		{
			name:         "32-byte secret",
			serverSecret: "12345678901234567890123456789012",
			plaintext:    "another-secret-value",
		},
		{
			name:         "long secret",
			serverSecret: "this-is-a-very-long-secret-that-is-much-longer-than-32-bytes",
			plaintext:    "test-data-to-encrypt",
		},
		{
			name:         "empty plaintext",
			serverSecret: "test-secret",
			plaintext:    "",
		},
		{
			name:         "unicode plaintext",
			serverSecret: "test-secret",
			plaintext:    "секрет-данные-🔐",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewSecretEncryption(tt.serverSecret)

			// Encrypt
			ciphertext, err := enc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encryption failed: %v", err)
			}

			// Empty plaintext should return empty ciphertext
			if tt.plaintext == "" {
				if ciphertext != "" {
					t.Error("Empty plaintext should produce empty ciphertext")
				}
				return
			}

			// Ciphertext should be different from plaintext
			if ciphertext == tt.plaintext {
				t.Error("Ciphertext should not equal plaintext")
			}

			// Decrypt
			decrypted, err := enc.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decryption failed: %v", err)
			}

			// Should match original
			if decrypted != tt.plaintext {
				t.Errorf("Decrypted text doesn't match. Expected: %s, Got: %s", tt.plaintext, decrypted)
			}
		})
	}
}

func TestSecretEncryption_DifferentSecrets_DifferentCiphertext(t *testing.T) {
	enc1 := NewSecretEncryption("secret1")
	enc2 := NewSecretEncryption("secret2")

	plaintext := "test-data"

	cipher1, err := enc1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encryption 1 failed: %v", err)
	}

	cipher2, err := enc2.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encryption 2 failed: %v", err)
	}

	// Different secrets should produce different ciphertext
	// (due to random nonces, even same secret produces different ciphertext,
	// but different secrets definitely should)
	if cipher1 == cipher2 {
		t.Error("Different secrets should produce different ciphertext")
	}

	// Cross-decryption should fail
	_, err = enc2.Decrypt(cipher1)
	if err == nil {
		t.Error("Decryption with wrong secret should fail")
	}

	_, err = enc1.Decrypt(cipher2)
	if err == nil {
		t.Error("Decryption with wrong secret should fail")
	}
}

func TestSecretEncryption_SameSecret_DifferentNonces(t *testing.T) {
	enc := NewSecretEncryption("test-secret")
	plaintext := "same-data"

	// Encrypt the same data twice
	cipher1, _ := enc.Encrypt(plaintext)
	cipher2, _ := enc.Encrypt(plaintext)

	// Should produce different ciphertext due to random nonces
	if cipher1 == cipher2 {
		t.Error("Same plaintext should produce different ciphertext due to random nonces")
	}

	// Both should decrypt to same value
	decrypted1, _ := enc.Decrypt(cipher1)
	decrypted2, _ := enc.Decrypt(cipher2)

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Error("Both ciphertexts should decrypt to original plaintext")
	}
}

func TestSecretEncryption_InvalidCiphertext(t *testing.T) {
	enc := NewSecretEncryption("test-secret")

	// Invalid base64
	_, err := enc.Decrypt("not-valid-base64!!!")
	if err == nil {
		t.Error("Should fail on invalid base64")
	}

	// Valid base64 but too short (less than nonce size)
	_, err = enc.Decrypt("YWJj") // "abc" in base64
	if err == nil {
		t.Error("Should fail on ciphertext shorter than nonce")
	}

	// Valid base64, correct length, but tampered data
	validCipher, _ := enc.Encrypt("test")
	if len(validCipher) > 10 {
		// Tamper with the ciphertext
		tampered := validCipher[:len(validCipher)-5] + "XXXXX"
		_, err = enc.Decrypt(tampered)
		if err == nil {
			t.Error("Should fail on tampered ciphertext")
		}
	}
}

// Ciphertexts written under the legacy SHA-256(serverSecret) key must still
// decrypt after the HKDF migration. Synthesises a legacy ciphertext, then
// confirms the production Decrypt path reads it.
func TestSecretEncryption_LegacyCiphertextDecrypts(t *testing.T) {
	plaintext := "legacy-client-secret"
	legacyCT, err := encryptWithLegacyKey(testSecret, plaintext)
	if err != nil {
		t.Fatalf("legacy encrypt setup: %v", err)
	}

	enc := NewSecretEncryption(testSecret)
	got, err := enc.Decrypt(legacyCT)
	if err != nil {
		t.Fatalf("decrypt legacy: %v", err)
	}
	if got != plaintext {
		t.Fatalf("legacy round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

// encryptWithLegacyKey produces a ciphertext using the pre-HKDF derivation
// (sha256(serverSecret) directly as the AES-GCM key). Test-only.
func encryptWithLegacyKey(serverSecret, plaintext string) (string, error) {
	key := sha256.Sum256([]byte(serverSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Two SecretEncryption instances built from the same server secret but
// different HKDF info labels must not be able to read each other's ciphertext.
// This is the cross-realm isolation guarantee that lets action credentials
// share SSO_SECRET safely.
func TestSecretEncryption_WithInfo_IsolatesRealms(t *testing.T) {
	ssoEnc := NewSecretEncryption(testSecret)
	actionEnc := NewSecretEncryptionWithInfo(testSecret, "windshift-action-credentials-encryption-v1")

	plaintext := "credential-value"
	actionCT, err := actionEnc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("action encrypt: %v", err)
	}

	// The action-realm ciphertext must round-trip in its own realm.
	if got, err := actionEnc.Decrypt(actionCT); err != nil || got != plaintext {
		t.Fatalf("action round-trip: got %q err %v", got, err)
	}

	// And the SSO realm must NOT be able to decrypt it.
	if _, err := ssoEnc.Decrypt(actionCT); err == nil {
		t.Fatalf("SSO realm decrypted an action-credential ciphertext — realms are not isolated")
	}

	// Conversely the action realm must not decrypt SSO-realm ciphertext.
	ssoCT, err := ssoEnc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("sso encrypt: %v", err)
	}
	if _, err := actionEnc.Decrypt(ssoCT); err == nil {
		t.Fatalf("action realm decrypted an SSO ciphertext — realms are not isolated")
	}
}
