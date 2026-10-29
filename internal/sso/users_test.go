//go:build test

package sso

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/testutils"
)

// TestFindOrCreateUser_EmailTrustPolicy covers the require_verified_email flag:
// it only governs whether an unverified email claim may auto-link an existing
// account. Every user returned from a successful SSO login is email-verified,
// because the SSO authentication is itself the ownership evidence.
func TestFindOrCreateUser_EmailTrustPolicy(t *testing.T) {
	tests := []struct {
		name                  string
		existingUserEmail     string
		claimsEmail           string
		claimsEmailVerified   bool
		claimsEmailProvided   bool
		trustProviderEmail    bool
		expectError           bool
		expectedErrorContains string
	}{
		{
			name:                "linking allowed - IdP verified email",
			existingUserEmail:   "verified@example.com",
			claimsEmail:         "verified@example.com",
			claimsEmailVerified: true,
			claimsEmailProvided: true,
		},
		{
			name:                  "linking blocked - IdP did not verify email",
			existingUserEmail:     "unverified@example.com",
			claimsEmail:           "unverified@example.com",
			claimsEmailVerified:   false,
			claimsEmailProvided:   true,
			expectError:           true,
			expectedErrorContains: "cannot automatically link",
		},
		{
			name:                "linking allowed - provider trust overrides explicit false",
			existingUserEmail:   "explicitly-unverified@example.com",
			claimsEmail:         "explicitly-unverified@example.com",
			claimsEmailVerified: false,
			claimsEmailProvided: true,
			trustProviderEmail:  true,
		},
		{
			name:                  "linking blocked - IdP did not provide verification claim",
			existingUserEmail:     "noprovided@example.com",
			claimsEmail:           "noprovided@example.com",
			claimsEmailProvided:   false,
			expectError:           true,
			expectedErrorContains: "cannot automatically link",
		},
		{
			name:                "linking allowed - trusted provider covers missing verification claim",
			existingUserEmail:   "trusted-link@example.com",
			claimsEmail:         "trusted-link@example.com",
			claimsEmailProvided: false,
			trustProviderEmail:  true,
		},
		{
			name:                "new user - provisioned verified without verification claim",
			claimsEmail:         "newuser@example.com",
			claimsEmailProvided: false,
		},
		{
			name:                "new user - provisioned verified despite explicit false claim",
			claimsEmail:         "newunverified@example.com",
			claimsEmailVerified: false,
			claimsEmailProvided: true,
		},
		{
			name:                "new user - provisioned verified with trusted missing claim",
			claimsEmail:         "newtrusted@example.com",
			claimsEmailProvided: false,
			trustProviderEmail:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()

			// Create existing user if specified
			if tt.existingUserEmail != "" {
				username := strings.Split(tt.existingUserEmail, "@")[0]
				_, err := tdb.Exec(`
					INSERT INTO users (email, username, first_name, last_name, is_active, email_verified, password_hash)
					VALUES (?, ?, 'Victim', 'User', true, false, '')
				`, tt.existingUserEmail, username)
				if err != nil {
					t.Fatalf("Failed to create existing user: %v", err)
				}
			}

			// Create SSO provider
			_, err := tdb.Exec(`
				INSERT INTO sso_providers (slug, name, provider_type, enabled, is_default, auto_provision_users, require_verified_email)
				VALUES ('test', 'Test', 'oidc', true, true, true, ?)
			`, tt.trustProviderEmail)
			if err != nil {
				t.Fatalf("Failed to create SSO provider: %v", err)
			}

			userStore := NewUserStore(tdb.GetDatabase())
			provider := &SSOProvider{ID: 1, AutoProvisionUsers: true, RequireVerifiedEmail: tt.trustProviderEmail}

			claimsUsername := strings.Split(tt.claimsEmail, "@")[0] + "_sso"
			claims := &OIDCClaims{
				Subject:               "external-" + tt.name,
				Email:                 tt.claimsEmail,
				EmailVerified:         tt.claimsEmailVerified,
				EmailVerifiedProvided: tt.claimsEmailProvided,
				Name:                  "Test User",
				Username:              claimsUsername,
			}

			result, err := userStore.FindOrCreateUser(provider, claims)

			if tt.expectError {
				if err == nil {
					t.Fatalf("Expected error containing %q, got nil", tt.expectedErrorContains)
				}
				if !strings.Contains(err.Error(), tt.expectedErrorContains) {
					t.Errorf("Expected error containing %q, got: %v", tt.expectedErrorContains, err)
				}
				var links int
				if err := tdb.QueryRow(`SELECT COUNT(*) FROM user_external_accounts WHERE external_id = ?`, claims.Subject).Scan(&links); err != nil {
					t.Fatalf("Count external accounts: %v", err)
				}
				if links != 0 {
					t.Errorf("Expected no external account link after refused login, got %d", links)
				}
				return
			}

			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}
			if result == nil || result.User == nil {
				t.Fatal("Expected user result, got nil")
			}

			if tt.existingUserEmail == "" {
				// New users are provisioned email-verified.
				var persistedEmailVerified bool
				if err := tdb.QueryRow(`SELECT email_verified FROM users WHERE id = ?`, result.User.ID).Scan(&persistedEmailVerified); err != nil {
					t.Fatalf("Load persisted email verification: %v", err)
				}
				if !persistedEmailVerified {
					t.Error("Expected provisioned user to be email-verified")
				}
				return
			}

			// Auto-linking must create the external account binding.
			var links int
			if err := tdb.QueryRow(`SELECT COUNT(*) FROM user_external_accounts WHERE external_id = ? AND user_id = ?`, claims.Subject, result.User.ID).Scan(&links); err != nil {
				t.Fatalf("Count external accounts: %v", err)
			}
			if links != 1 {
				t.Errorf("Expected external account linked to user %d, got %d links", result.User.ID, links)
			}
		})
	}
}

// A linked account login returns the user unchanged: the store never mutates
// the local email row (the SSO callback owns the verified repair) regardless
// of what the provider asserts about the address.
func TestFindOrCreateUser_LinkedAccountReturnedWithoutMutations(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	_, err := tdb.Exec(`
		INSERT INTO users (id, email, username, first_name, last_name, is_active, email_verified, password_hash)
		VALUES (100, 'ssolinked@example.com', 'ssolinkeduser', 'Test', 'User', true, false, '')
	`)
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	_, err = tdb.Exec(`
		INSERT INTO sso_providers (id, slug, name, provider_type, enabled, is_default, auto_provision_users, require_verified_email)
		VALUES (100, 'test-linked', 'Test Linked', 'oidc', true, true, true, true)
	`)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	_, err = tdb.Exec(`
		INSERT INTO user_external_accounts (user_id, provider_id, external_id, email, linked_at)
		VALUES (100, 100, 'external-456', 'ssolinked@example.com', CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Failed to create external account: %v", err)
	}

	userStore := NewUserStore(tdb.GetDatabase())
	provider := &SSOProvider{ID: 100, AutoProvisionUsers: true, RequireVerifiedEmail: true}

	run := func(claims *OIDCClaims) *FindOrCreateResult {
		t.Helper()
		result, err := userStore.FindOrCreateUser(provider, claims)
		if err != nil {
			t.Fatalf("Expected linked account login to succeed, got: %v", err)
		}
		if result.User == nil || result.User.ID != 100 {
			t.Fatalf("Expected linked user 100, got %+v", result.User)
		}
		return result
	}

	// Explicitly unverified claim from the provider.
	run(&OIDCClaims{Subject: "external-456", Email: "ssolinked@example.com", EmailVerified: false, EmailVerifiedProvided: true})

	// Verified claim for a different address than the local row (GH #272).
	run(&OIDCClaims{Subject: "external-456", Email: "different@example.com", EmailVerified: true, EmailVerifiedProvided: true})

	// The store must not overwrite the local row with the provider payload.
	var email string
	var verified bool
	if err := tdb.QueryRow(`SELECT email, email_verified FROM users WHERE id = 100`).Scan(&email, &verified); err != nil {
		t.Fatalf("Load linked user row: %v", err)
	}
	if email != "ssolinked@example.com" {
		t.Errorf("Expected local email to stay untouched, got %q", email)
	}
	if verified {
		t.Error("Expected store not to mutate email_verified; that is the callback's repair step")
	}
}

func TestFindOrCreateUser_AutoProvisionDisabled(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	// Create SSO provider with auto-provision disabled
	_, err := tdb.Exec(`
		INSERT INTO sso_providers (id, slug, name, provider_type, enabled, is_default, auto_provision_users)
		VALUES (1, 'test', 'Test', 'oidc', true, true, false)
	`)
	if err != nil {
		t.Fatalf("Failed to create SSO provider: %v", err)
	}

	userStore := NewUserStore(tdb.GetDatabase())
	provider := &SSOProvider{ID: 1, AutoProvisionUsers: false}

	claims := &OIDCClaims{
		Subject:               "external-789",
		Email:                 "newuser@example.com",
		EmailVerified:         true,
		EmailVerifiedProvided: true,
	}

	_, err = userStore.FindOrCreateUser(provider, claims)
	if err == nil {
		t.Error("Expected error when auto-provision is disabled")
	}

	if err != ErrAutoProvisionDisabled {
		t.Errorf("Expected ErrAutoProvisionDisabled, got: %v", err)
	}
}

func TestSplitCommaDisplayName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantFirst string
		wantLast  string
		wantOK    bool
	}{
		{name: "empty", input: "", wantOK: false},
		{name: "whitespace only", input: "   ", wantOK: false},
		{name: "comma form", input: "Lovelace, Ada", wantFirst: "Ada", wantLast: "Lovelace", wantOK: true},
		{name: "comma form with middle name", input: "Lovelace, Ada King", wantFirst: "Ada King", wantLast: "Lovelace", wantOK: true},
		{name: "multi-part comma name keeps both halves intact", input: "García López, José Antonio", wantFirst: "José Antonio", wantLast: "García López", wantOK: true},
		{name: "space form is not split", input: "Ada Lovelace", wantOK: false},
		{name: "single token is not split", input: "Ada", wantOK: false},
		{name: "trailing comma keeps the family name", input: "Lovelace,", wantFirst: "", wantLast: "Lovelace", wantOK: true},
		{name: "leading comma keeps the given name", input: ", Ada", wantFirst: "Ada", wantLast: "", wantOK: true},
		{name: "bare comma is not a split", input: ",", wantOK: false},
		{name: "trims surrounding whitespace", input: "  Lovelace ,  Ada  ", wantFirst: "Ada", wantLast: "Lovelace", wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFirst, gotLast, gotOK := splitCommaDisplayName(tt.input)
			if gotFirst != tt.wantFirst || gotLast != tt.wantLast || gotOK != tt.wantOK {
				t.Errorf("splitCommaDisplayName(%q) = (%q, %q, %t), want (%q, %q, %t)", tt.input, gotFirst, gotLast, gotOK, tt.wantFirst, tt.wantLast, tt.wantOK)
			}
		})
	}
}

func TestCreateUser_ResolvesNames(t *testing.T) {
	tests := []struct {
		name      string
		claims    OIDCClaims
		wantFirst string
		wantLast  string
	}{
		{
			name:      "explicit given and family names win",
			claims:    OIDCClaims{GivenName: "Ada", FamilyName: "Lovelace", Name: "ignored"},
			wantFirst: "Ada",
			wantLast:  "Lovelace",
		},
		{
			name:      "comma display name splits into first then last",
			claims:    OIDCClaims{Name: "Lovelace, Ada"},
			wantFirst: "Ada",
			wantLast:  "Lovelace",
		},
		{
			name:      "multi-part comma display name stays intact",
			claims:    OIDCClaims{Name: "García López, José Antonio"},
			wantFirst: "José Antonio",
			wantLast:  "García López",
		},
		{
			name:      "space display name is not guessed",
			claims:    OIDCClaims{Name: "Ada Lovelace"},
			wantFirst: "",
			wantLast:  "",
		},
		{
			name:      "space display name with many parts is not guessed",
			claims:    OIDCClaims{Name: "José Antonio García López"},
			wantFirst: "",
			wantLast:  "",
		},
		{
			name:      "middle name stays with the given name",
			claims:    OIDCClaims{Name: "Lovelace, Ada King"},
			wantFirst: "Ada King",
			wantLast:  "Lovelace",
		},
		{
			name:      "missing family name is filled from a comma display name",
			claims:    OIDCClaims{GivenName: "Ada", Name: "Lovelace, Ada"},
			wantFirst: "Ada",
			wantLast:  "Lovelace",
		},
		{
			name:      "missing family name is not guessed from a space display name",
			claims:    OIDCClaims{GivenName: "Ada", Name: "Ada Lovelace"},
			wantFirst: "Ada",
			wantLast:  "",
		},
		{
			name:      "no name claims leaves both empty",
			claims:    OIDCClaims{},
			wantFirst: "",
			wantLast:  "",
		},
	}

	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	userStore := NewUserStore(tdb.GetDatabase())

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := tt.claims
			claims.Subject = fmt.Sprintf("create-sub-%d", i)
			claims.Email = fmt.Sprintf("create%d@example.com", i)
			claims.Username = fmt.Sprintf("createuser%d", i)

			user, err := userStore.CreateUser(&claims, true)
			if err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			if user.FirstName != tt.wantFirst || user.LastName != tt.wantLast {
				t.Errorf("names = (%q, %q), want (%q, %q)", user.FirstName, user.LastName, tt.wantFirst, tt.wantLast)
			}
		})
	}
}

func TestFindOrCreateUser_SyncsNames(t *testing.T) {
	tests := []struct {
		name          string
		existingFirst string
		existingLast  string
		linkExternal  bool
		claims        OIDCClaims
		wantFirst     string
		wantLast      string
	}{
		{
			name:          "explicit claims repair a linked account name",
			existingFirst: "Lovelace,",
			existingLast:  "Ada",
			linkExternal:  true,
			claims:        OIDCClaims{GivenName: "Ada", FamilyName: "Lovelace"},
			wantFirst:     "Ada",
			wantLast:      "Lovelace",
		},
		{
			name:          "explicit claims repair the name on first email link",
			existingFirst: "Lovelace,",
			existingLast:  "Ada",
			claims:        OIDCClaims{GivenName: "Ada", FamilyName: "Lovelace"},
			wantFirst:     "Ada",
			wantLast:      "Lovelace",
		},
		{
			name:          "display name alone does not overwrite stored names",
			existingFirst: "Ada",
			existingLast:  "Lovelace",
			linkExternal:  true,
			claims:        OIDCClaims{Name: "Lovelace, Ada"},
			wantFirst:     "Ada",
			wantLast:      "Lovelace",
		},
		{
			name:          "a half missing from the claims keeps the stored value",
			existingFirst: "Ada",
			existingLast:  "Lovelace",
			linkExternal:  true,
			claims:        OIDCClaims{GivenName: "Augusta"},
			wantFirst:     "Augusta",
			wantLast:      "Lovelace",
		},
		{
			name:          "explicit given plus comma display name fills the family name",
			existingFirst: "Wrong",
			existingLast:  "Name",
			linkExternal:  true,
			claims:        OIDCClaims{GivenName: "Ada", Name: "Lovelace, Ada"},
			wantFirst:     "Ada",
			wantLast:      "Lovelace",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()

			userID := 300 + i
			providerID := 300 + i
			email := fmt.Sprintf("sync%d@example.com", i)

			if _, err := tdb.Exec(`
				INSERT INTO users (id, email, username, first_name, last_name, is_active, email_verified, password_hash)
				VALUES (?, ?, ?, ?, ?, true, true, '')
			`, userID, email, fmt.Sprintf("syncuser%d", i), tt.existingFirst, tt.existingLast); err != nil {
				t.Fatalf("create user: %v", err)
			}

			if _, err := tdb.Exec(`
				INSERT INTO sso_providers (id, slug, name, provider_type, enabled, is_default, auto_provision_users, require_verified_email)
				VALUES (?, ?, 'Sync', 'oidc', true, true, true, true)
			`, providerID, fmt.Sprintf("sync%d", i)); err != nil {
				t.Fatalf("create provider: %v", err)
			}

			if tt.linkExternal {
				if _, err := tdb.Exec(`
					INSERT INTO user_external_accounts (user_id, provider_id, external_id, email, linked_at)
					VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
				`, userID, providerID, fmt.Sprintf("ext-sync-%d", i), email); err != nil {
					t.Fatalf("create external account: %v", err)
				}
			}

			userStore := NewUserStore(tdb.GetDatabase())
			provider := &SSOProvider{ID: providerID, Slug: fmt.Sprintf("sync%d", i), AutoProvisionUsers: true, RequireVerifiedEmail: true}

			claims := tt.claims
			claims.Subject = fmt.Sprintf("ext-sync-%d", i)
			claims.Email = email
			claims.EmailVerified = true
			claims.EmailVerifiedProvided = true

			result, err := userStore.FindOrCreateUser(provider, &claims)
			if err != nil {
				t.Fatalf("FindOrCreateUser: %v", err)
			}
			if result.User.FirstName != tt.wantFirst || result.User.LastName != tt.wantLast {
				t.Errorf("returned names = (%q, %q), want (%q, %q)", result.User.FirstName, result.User.LastName, tt.wantFirst, tt.wantLast)
			}

			var first, last string
			if err := tdb.QueryRow(`SELECT first_name, last_name FROM users WHERE id = ?`, userID).Scan(&first, &last); err != nil {
				t.Fatalf("load persisted name: %v", err)
			}
			if first != tt.wantFirst || last != tt.wantLast {
				t.Errorf("persisted names = (%q, %q), want (%q, %q)", first, last, tt.wantFirst, tt.wantLast)
			}
		})
	}
}
