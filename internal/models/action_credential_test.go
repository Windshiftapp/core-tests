package models

import "testing"

func TestValidateActionCredentialMetadataRejectsNestedSecrets(t *testing.T) {
	t.Parallel()

	invalid := []string{
		`null`,
		`[]`,
		`{"token":"plaintext"}`,
		`{"provider":{"clientSecret":"plaintext"}}`,
		`{"accounts":[{"refresh_token":"plaintext"}]}`,
		`{"signing":{"private-key":"plaintext"}}`,
	}
	for _, metadata := range invalid {
		metadata := metadata
		t.Run(metadata, func(t *testing.T) {
			t.Parallel()
			if err := ValidateActionCredentialMetadata(metadata); err == nil {
				t.Fatalf("ValidateActionCredentialMetadata(%s) unexpectedly succeeded", metadata)
			}
		})
	}
}

func TestValidateActionCredentialMetadataAcceptsNonSensitiveMetadata(t *testing.T) {
	t.Parallel()

	for _, metadata := range []string{
		``,
		`{}`,
		`{"provider":"github","scopes":["repo"],"expires_at":"2027-01-01"}`,
		`{"labels":{"environment":"production"}}`,
	} {
		if err := ValidateActionCredentialMetadata(metadata); err != nil {
			t.Errorf("ValidateActionCredentialMetadata(%s): %v", metadata, err)
		}
	}
}

func TestActionCredentialSanitizeFailsClosedForUnsafeLegacyMetadata(t *testing.T) {
	t.Parallel()

	credential := &ActionCredential{
		EncryptedSecret: "ciphertext",
		SecretMetadata:  `{"provider":{"accessToken":"plaintext"}}`,
	}
	sanitized := credential.Sanitize()
	if sanitized.SecretMetadata != "" {
		t.Fatalf("unsafe metadata was returned: %q", sanitized.SecretMetadata)
	}
	if !sanitized.HasSecret {
		t.Fatal("sanitized credential lost has_secret indicator")
	}
}

func TestIsSensitiveHeaderNameIncludesSignatureHeaders(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"X-Signature", "x-webhook-signature"} {
		if !IsSensitiveHeaderName(header) {
			t.Errorf("IsSensitiveHeaderName(%q) = false", header)
		}
	}
}

func TestHTTPHeaderAndAuthSchemeValidation(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Authorization", "X-API-Key", "X_Custom"} {
		if !IsValidHTTPHeaderName(name) {
			t.Errorf("IsValidHTTPHeaderName(%q) = false", name)
		}
	}
	for _, name := range []string{"", " X-API-Key", "X-Bad\r\nInjected"} {
		if IsValidHTTPHeaderName(name) {
			t.Errorf("IsValidHTTPHeaderName(%q) = true", name)
		}
	}
	if !IsValidHTTPAuthScheme("Bearer") || IsValidHTTPAuthScheme("Bearer plaintext") {
		t.Fatal("HTTP auth scheme validation did not enforce single-token grammar")
	}
}
