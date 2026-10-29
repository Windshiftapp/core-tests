package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAssetImportUploadOwnership(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	setID, typeID := seedAssetSetAndType(t, server, "upload-owner")
	otherSetID, otherTypeID := seedAssetSetAndType(t, server, "upload-other")
	userID, username, password := CreateTestUserWithCredentials(t, server, "csv_owner", "csv_owner@test.com")
	adminRole := getAssetRoleID(t, server, "Administrator")
	assignAssetSetRole(t, server, setID, userID, adminRole)
	assignAssetSetRole(t, server, otherSetID, userID, adminRole)
	ownerToken := createTokenWithScopesAsUser(t, server, username, password, []string{"assets:read", "assets:write"})
	owner := *server
	owner.BearerToken = ownerToken
	response := makeBearerMultipartRequest(t, &owner, fmt.Sprintf("/rest/api/v2/asset-sets/%d/import/upload", setID), map[string]string{"has_header": "true"}, "private.csv", []byte("title\nConfidential laptop\n"))
	AssertStatusCode(t, response, http.StatusCreated)
	var upload struct {
		UploadID string `json:"upload_id"`
	}
	DecodeJSON(t, response, &upload)
	response.Body.Close()
	if upload.UploadID == "" {
		t.Fatal("missing upload ID")
	}
	for _, tc := range []struct {
		name, token   string
		setID, typeID int
	}{
		{"other set", ownerToken, otherSetID, otherTypeID},
		{"other user", server.BearerToken, setID, typeID},
	} {
		for _, action := range []string{"suggest-fields", "start"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				body := map[string]any{"upload_id": upload.UploadID, "has_header": true}
				if action == "start" {
					body["asset_type_id"] = tc.typeID
				}
				denied := MakeBearerRequestWithToken(t, server, tc.token, http.MethodPost, fmt.Sprintf("/rest/api/v2/asset-sets/%d/import/%s", tc.setID, action), body)
				defer denied.Body.Close()
				AssertStatusCode(t, denied, http.StatusNotFound)
				var result struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				DecodeJSON(t, denied, &result)
				if result.Error.Code != "not_found" {
					t.Fatalf("error = %+v", result)
				}
			})
		}
	}
	if t.Failed() {
		return
	}
	preview := MakeBearerRequestWithToken(t, server, ownerToken, http.MethodPost, fmt.Sprintf("/rest/api/v2/asset-sets/%d/import/suggest-fields", setID), map[string]any{"upload_id": upload.UploadID, "has_header": true})
	defer preview.Body.Close()
	AssertStatusCode(t, preview, http.StatusOK)
	var samples struct {
		SuggestedFields []struct {
			SampleValues []string `json:"sample_values"`
		} `json:"suggested_fields"`
	}
	DecodeJSON(t, preview, &samples)
	if len(samples.SuggestedFields) != 1 || len(samples.SuggestedFields[0].SampleValues) != 1 || samples.SuggestedFields[0].SampleValues[0] != "Confidential laptop" {
		t.Fatalf("owner preview = %+v", samples)
	}
	for _, id := range []int{setID, otherSetID} {
		jobs := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/asset-sets/%d/import/jobs", id), nil)
		AssertStatusCode(t, jobs, http.StatusOK)
		var data []any
		DecodeJSON(t, jobs, &data)
		jobs.Body.Close()
		if len(data) != 0 {
			t.Fatalf("denied requests created jobs: %v", data)
		}
	}
	assignAssetSetRole(t, server, setID, userID, getAssetRoleID(t, server, "Viewer"))
	for _, action := range []string{"suggest-fields", "start"} {
		body := map[string]any{"upload_id": upload.UploadID, "has_header": true}
		if action == "start" {
			body["asset_type_id"] = typeID
		}
		denied := MakeBearerRequestWithToken(t, server, ownerToken, http.MethodPost, fmt.Sprintf("/rest/api/v2/asset-sets/%d/import/%s", setID, action), body)
		AssertStatusCode(t, denied, http.StatusNotFound)
		denied.Body.Close()
	}
}
