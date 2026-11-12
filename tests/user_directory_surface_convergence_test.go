package tests

import (
	"fmt"
	"net/http"
	"testing"
)

type surfaceDirectoryUser struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	FullName  string `json:"full_name"`
	IsActive  bool   `json:"is_active"`
}

func TestV2UserReadAllowsTheAuthenticatedUserWithoutUserList(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	userID, username, password := CreateTestUserWithCredentials(t, server, "self_directory_reader", "self_directory_reader@test.com")
	targetID, _, _ := CreateTestUserWithCredentials(t, server, "other_directory_reader", "other_directory_reader@test.com")
	token := createTokenWithScopesAsUser(t, server, username, password, []string{"users:read"})

	resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/users/%d", userID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var user surfaceDirectoryUser
	DecodeJSON(t, resp, &user)
	if user.ID != userID || user.Username != username {
		t.Fatalf("self user = %+v, want id=%d username=%q", user, userID, username)
	}

	other := MakeBearerRequestWithToken(t, server, token, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/users/%d", targetID), nil)
	defer other.Body.Close()
	AssertStatusCode(t, other, http.StatusForbidden)
}

func TestUserDirectoryReadsConvergeAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	userID, username, _ := CreateTestUserWithCredentials(t, server, "directory_reader", "directory_reader@test.com")

	sessionGet := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/users/%d", userID), nil)
	defer sessionGet.Body.Close()
	AssertStatusCode(t, sessionGet, http.StatusOK)
	var sessionUser surfaceDirectoryUser
	DecodeJSON(t, sessionGet, &sessionUser)

	bearerGet := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/users/%d", userID), nil)
	defer bearerGet.Body.Close()
	AssertStatusCode(t, bearerGet, http.StatusOK)
	var bearerUser surfaceDirectoryUser
	DecodeJSON(t, bearerGet, &bearerUser)
	if bearerUser != sessionUser || bearerUser.Username != username || bearerUser.Email != "" {
		t.Fatalf("session user = %+v, bearer user = %+v", sessionUser, bearerUser)
	}

	workspaceID, _ := CreateTestWorkspace(t, server, "Assignable directory", shortKey("ADR"))
	AssignWorkspaceRole(t, server, userID, workspaceID, "Viewer")

	sessionAssignable := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/v2/workspaces/%d/assignable-users", workspaceID), nil)
	defer sessionAssignable.Body.Close()
	AssertStatusCode(t, sessionAssignable, http.StatusOK)
	var sessionUsers []surfaceDirectoryUser
	DecodeJSON(t, sessionAssignable, &sessionUsers)

	bearerAssignable := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v2/workspaces/%d/assignable-users", workspaceID), nil)
	defer bearerAssignable.Body.Close()
	AssertStatusCode(t, bearerAssignable, http.StatusOK)
	var bearerUsers []surfaceDirectoryUser
	DecodeJSON(t, bearerAssignable, &bearerUsers)
	if !directoryContainsUser(sessionUsers, userID) || !directoryContainsUser(bearerUsers, userID) {
		t.Fatalf("assignable user %d missing: session=%+v bearer=%+v", userID, sessionUsers, bearerUsers)
	}
}

// The self endpoint is the only user read that may expose private profile
// fields. The directory summary deliberately omits them (see the email check in
// TestUserDirectoryReadsConvergeAcrossSessionAndBearerSurfaces), so the profile
// page must not source its email, timezone, or language from the directory.
func TestV2SelfProfileIncludesPrivateFields(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	userID, username, _ := CreateTestUserWithCredentials(t, server, "self_profile_reader", "self_profile_reader@test.com")

	update := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/users/%d/regional-settings", userID), map[string]any{
			"timezone": "Europe/Zurich",
			"language": "de",
		})
	defer update.Body.Close()
	AssertStatusCode(t, update, http.StatusOK)

	reset := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/users/%d/reset-password", userID), map[string]any{"generate_random": true})
	defer reset.Body.Close()
	AssertStatusCode(t, reset, http.StatusOK)
	var resetBody struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	DecodeJSON(t, reset, &resetBody)
	if resetBody.TemporaryPassword == "" {
		t.Fatal("reset-password did not return a temporary password")
	}

	token := createTokenWithScopesAsUser(t, server, username, resetBody.TemporaryPassword, []string{"users:read"})
	resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet, "/rest/api/v2/users/me", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var profile struct {
		ID                    int    `json:"id"`
		Email                 string `json:"email"`
		Username              string `json:"username"`
		FullName              string `json:"full_name"`
		Timezone              string `json:"timezone"`
		Language              string `json:"language"`
		RequiresPasswordReset bool   `json:"requires_password_reset"`
	}
	DecodeJSON(t, resp, &profile)
	if profile.ID != userID || profile.Username != username {
		t.Fatalf("self profile identity = %+v, want id=%d username=%q", profile, userID, username)
	}
	if profile.Email != "self_profile_reader@test.com" {
		t.Fatalf("self profile email = %q", profile.Email)
	}
	if profile.Timezone != "Europe/Zurich" || profile.Language != "de" {
		t.Fatalf("self profile regional settings = %+v", profile)
	}
	if profile.FullName != "Test User" {
		t.Fatalf("self profile full_name = %q", profile.FullName)
	}
	if !profile.RequiresPasswordReset {
		t.Fatalf("self profile requires_password_reset = false after a password reset")
	}
}

func directoryContainsUser(users []surfaceDirectoryUser, userID int) bool {
	for _, user := range users {
		if user.ID == userID {
			return true
		}
	}
	return false
}
