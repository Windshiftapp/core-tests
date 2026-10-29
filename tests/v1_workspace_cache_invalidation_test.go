package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestV1WorkspaceLifecycleConvergesWithV2Reads(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	key := shortKey("V1KEY")
	createResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodPost,
		"/rest/api/v1/workspaces", map[string]interface{}{
			"name": "V1 Key Cache",
			"key":  key,
		})
	defer createResponse.Body.Close()
	AssertStatusCode(t, createResponse, http.StatusCreated)
	var created struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, createResponse, &created)

	createdInV2 := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%d", created.ID), nil)
	AssertStatusCode(t, createdInV2, http.StatusOK)
	createdInV2.Body.Close()

	deleteResponse := MakeBearerRequestWithToken(t, server, server.BearerToken, http.MethodDelete,
		fmt.Sprintf("/rest/api/v1/workspaces/%d", created.ID), nil)
	AssertStatusCode(t, deleteResponse, http.StatusNoContent)
	deleteResponse.Body.Close()

	deletedInV2 := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/workspaces/%d", created.ID), nil)
	AssertStatusCode(t, deletedInV2, http.StatusNotFound)
	deletedInV2.Body.Close()
}
