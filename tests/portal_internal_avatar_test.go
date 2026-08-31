package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestPortalInternalUserBootstrapIncludesUpdatedAvatar(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Avatar Portal WS", shortKey("APWS"))
	slug, _ := SetupPortalChannel(t, server, wsID)
	const avatarURL = "/api/attachments/42/download"

	updateResp := MakeAuthRequest(t, server, http.MethodPut, "/users/1/avatar", map[string]any{
		"avatar_url": avatarURL,
	})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)

	bootstrapResp := MakeAuthRequest(t, server, http.MethodGet,
		fmt.Sprintf("/portal/%s/user-bootstrap", slug), nil)
	defer bootstrapResp.Body.Close()
	AssertStatusCode(t, bootstrapResp, http.StatusOK)

	var bootstrap map[string]any
	DecodeJSON(t, bootstrapResp, &bootstrap)
	user, ok := bootstrap["user"].(map[string]any)
	if !ok {
		t.Fatalf("bootstrap user = %#v, want object", bootstrap["user"])
	}
	if got := user["avatar_url"]; got != avatarURL {
		t.Fatalf("avatar_url = %v, want %q", got, avatarURL)
	}
}
