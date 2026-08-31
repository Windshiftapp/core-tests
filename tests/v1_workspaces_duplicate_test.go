package tests

import (
	"net/http"
	"strings"
	"testing"
)

func TestV1Workspaces_CreateDuplicateKeyIsCaseInsensitiveConflict(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	_, existingKey := CreateTestWorkspace(t, ts, "Duplicate key source", shortKey("DUP"))

	resp := MakeBearerRequestWithToken(t, ts, ts.BearerToken, http.MethodPost, "/rest/api/v1/workspaces", map[string]interface{}{
		"name": "Duplicate key destination",
		"key":  strings.ToLower(existingKey),
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusConflict)

	var got struct {
		Code string `json:"code"`
	}
	DecodeJSON(t, resp, &got)
	if got.Code != "ALREADY_EXISTS" {
		t.Fatalf("error code = %q, want ALREADY_EXISTS", got.Code)
	}
}
