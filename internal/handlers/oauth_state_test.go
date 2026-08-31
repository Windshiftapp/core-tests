package handlers

import "testing"

func TestEmailOAuthStateIsBoundToStartingConfig(t *testing.T) {
	state, err := newEmailOAuthState(`{"email_auth_method":"oauth"}`)
	if err != nil {
		t.Fatalf("newEmailOAuthState: %v", err)
	}
	if !emailOAuthStateMatchesConfig(state, `{"email_auth_method":"oauth"}`) {
		t.Fatal("state did not match the starting config")
	}
	if emailOAuthStateMatchesConfig(state, `{"email_auth_method":"basic"}`) {
		t.Fatal("state matched a config changed during OAuth")
	}
	if emailOAuthStateMatchesConfig("legacy-random-state", `{"email_auth_method":"oauth"}`) {
		t.Fatal("legacy/unbound state unexpectedly matched")
	}
}
