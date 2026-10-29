package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAssetLifecycleConvergesAcrossSessionAndBearerSurfaces(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	token := adminAssetToken(t, server)
	setID, assetTypeID := seedAssetSetAndType(t, server, "convergence")

	created := MakeAuthRequest(t, server, http.MethodPost,
		fmt.Sprintf("/v2/asset-sets/%d/assets", setID), map[string]any{
			"title": "Shared asset", "description": "created by session",
			"asset_tag": "SHARED-1", "asset_type_id": assetTypeID,
		})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var asset map[string]any
	DecodeJSON(t, created, &asset)
	assetID := ExtractIDFromResponse(t, asset)

	bearerUpdate := MakeBearerRequestWithToken(t, server, token, http.MethodPatch,
		fmt.Sprintf("/rest/api/v2/assets/%d", assetID), map[string]any{
			"title": "Updated asset", "description": "updated by bearer",
		})
	defer bearerUpdate.Body.Close()
	AssertStatusCode(t, bearerUpdate, http.StatusOK)

	sessionRead := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/assets/%d", assetID), nil)
	defer sessionRead.Body.Close()
	AssertStatusCode(t, sessionRead, http.StatusOK)
	var updated map[string]any
	DecodeJSON(t, sessionRead, &updated)
	AssertJSONField(t, updated, "title", "Updated asset")
	AssertJSONField(t, updated, "asset_tag", "SHARED-1")

	deleted := MakeBearerRequestWithToken(t, server, token, http.MethodDelete,
		fmt.Sprintf("/rest/api/v2/assets/%d", assetID), nil)
	defer deleted.Body.Close()
	AssertStatusCode(t, deleted, http.StatusNoContent)

	missing := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/assets/%d", assetID), nil)
	defer missing.Body.Close()
	AssertStatusCode(t, missing, http.StatusNotFound)
}
