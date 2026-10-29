package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestAdminOffboardedUserCannotBeReactivated covers WI-1308: administrative
// offboarding is irreversible, so the activate endpoint must refuse the
// anonymized account with 409 and the account must stay inactive.
func TestAdminOffboardedUserCannotBeReactivated(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	userID, _, _ := CreateTestUserWithCredentials(t, server, shortKey("obd"), shortKey("obd")+"@example.test")

	offboard := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/users/%d", userID), nil)
	defer offboard.Body.Close()
	AssertStatusCode(t, offboard, http.StatusNoContent)

	reactivate := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/users/%d/activate", userID), nil)
	defer reactivate.Body.Close()
	AssertStatusCode(t, reactivate, http.StatusConflict)

	userResp := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/admin/users/%d", userID), nil)
	defer userResp.Body.Close()
	AssertStatusCode(t, userResp, http.StatusOK)
	var user map[string]interface{}
	DecodeJSON(t, userResp, &user)
	AssertJSONField(t, user, "is_active", false)
}

// TestAdminCannotResetOffboardedUserPassword asserts that password resets are
// refused for offboarded accounts: no credential state may be written onto a
// retired row.
func TestAdminCannotResetOffboardedUserPassword(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	userID, _, _ := CreateTestUserWithCredentials(t, server, shortKey("obr"), shortKey("obr")+"@example.test")

	offboard := MakeAuthRequest(t, server, http.MethodDelete, fmt.Sprintf("/users/%d", userID), nil)
	defer offboard.Body.Close()
	AssertStatusCode(t, offboard, http.StatusNoContent)

	reset := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/users/%d/reset-password", userID), map[string]interface{}{
		"user_id":        userID,
		"generate_random": true,
	})
	defer reset.Body.Close()
	AssertStatusCode(t, reset, http.StatusConflict)
}
