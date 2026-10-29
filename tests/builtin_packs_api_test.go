package tests

import (
	"net/http"
	"strings"
	"testing"
)

// Built-in framework packs (WI-1141 slice 2): the server ships packs as
// embedded assets and exposes them for listing, verifying, and applying
// without an archive upload. All are system-administrator operations over the
// same PackApplyReport the upload path produces.

func TestBuiltinPacksList(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/packs", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var decoded struct {
		Data []struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			HasContent bool   `json:"has_content"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)

	var found bool
	for _, p := range decoded.Data {
		if p.Name == "helpdesk" {
			found = true
			if p.Version != "1.1.2" {
				t.Fatalf("helpdesk version = %q, want 1.1.2", p.Version)
			}
			if !p.HasContent {
				t.Fatalf("helpdesk must report content: %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("helpdesk pack missing from list: %+v", decoded.Data)
	}
}

func TestBuiltinPacksListRequiresSystemAdmin(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	_, _, password := CreateTestUserWithCredentials(t, server, "pack-viewer", "pack-viewer@test.local")
	_, token := CreateAuthCredentialsForUser(t, server, "pack-viewer", password)

	resp := MakeBearerRequestWithToken(t, server, token, http.MethodGet, "/rest/api/v2/packs", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusForbidden)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "insufficient_permission" {
		t.Fatalf("denial code = %q", errBody.Error.Code)
	}
}

func TestBuiltinPackVerifyDoesNotCreateWorkspace(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/packs/helpdesk/verify",
		map[string]any{"workspace_name": "Helpdesk Verify"})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)

	var decoded struct {
		Data struct {
			Status string           `json:"status"`
			Stages []map[string]any `json:"stages"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	if decoded.Data.Status != "verified" {
		t.Fatalf("verify status = %q (stages: %+v)", decoded.Data.Status, decoded.Data.Stages)
	}
	for _, stage := range decoded.Data.Stages {
		if stage["name"] == "workspace" {
			t.Fatalf("verify must not resolve apply stages: %+v", decoded.Data.Stages)
		}
	}
	if findWorkspaceByName(t, server, "Helpdesk Verify") != 0 {
		t.Fatal("verify created a workspace")
	}
}

func TestBuiltinPackApplyByName(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	apply := func() map[string]any {
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/packs/helpdesk/apply",
			map[string]any{"workspace_name": "Helpdesk API"})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var decoded struct {
			Data struct {
				Status      string           `json:"status"`
				Stages      []map[string]any `json:"stages"`
				Conformance map[string]any   `json:"conformance"`
			} `json:"data"`
		}
		DecodeJSON(t, resp, &decoded)
		if decoded.Data.Status != "applied" {
			t.Fatalf("apply status = %q (stages: %+v conformance: %+v)", decoded.Data.Status, decoded.Data.Stages, decoded.Data.Conformance)
		}
		if decoded.Data.Conformance == nil || decoded.Data.Conformance["conformant"] != true {
			t.Fatalf("conformance = %+v", decoded.Data.Conformance)
		}
		return decoded.Data.Conformance
	}

	apply()

	wsID := findWorkspaceByName(t, server, "Helpdesk API")
	if wsID == 0 {
		t.Fatal("applied workspace not found")
	}
	bundle, _ := exportWorkspaceBundle(t, server, wsID)
	if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Helpdesk" {
		t.Fatalf("configuration set not attached: %+v", bundle.ConfigurationSet)
	}
}

// TestBuiltinPackApplyRefusesExistingWorkspaceName: framework packs only apply
// to new workspaces. A name that already exists fails the workspace stage and
// leaves the existing workspace untouched instead of applying into it.
func TestBuiltinPackApplyRefusesExistingWorkspaceName(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	wsID, _ := CreateTestWorkspace(t, server, "Existing Pack Target", "")

	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/packs/helpdesk/apply",
		map[string]any{"workspace_name": "Existing Pack Target"})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data struct {
			Status string           `json:"status"`
			Stages []map[string]any `json:"stages"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	if decoded.Data.Status != "failed" {
		t.Fatalf("apply status = %q, want failed (stages: %+v)", decoded.Data.Status, decoded.Data.Stages)
	}
	workspaceStage := stageByName(t, decoded.Data.Stages, "workspace")
	if workspaceStage["status"] != "failed" {
		t.Fatalf("workspace stage = %+v, want failed", workspaceStage)
	}
	if detail, _ := workspaceStage["detail"].(string); !strings.Contains(detail, "already exists") {
		t.Fatalf("workspace stage detail = %q, want an already-exists reason", detail)
	}

	bundle, _ := exportWorkspaceBundle(t, server, wsID)
	if bundle.ConfigurationSet != nil {
		t.Fatalf("existing workspace was modified: %+v", bundle.ConfigurationSet)
	}
}

func TestBuiltinPackUnknownName(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	for _, action := range []string{"apply", "verify"} {
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/packs/nope/"+action,
			map[string]any{"workspace_name": "Should Not Exist"})
		AssertStatusCode(t, resp, http.StatusNotFound)
		var errBody struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		DecodeJSON(t, resp, &errBody)
		resp.Body.Close()
		if errBody.Error.Code != "not_found" {
			t.Fatalf("%s unknown pack code = %q", action, errBody.Error.Code)
		}
	}
	if findWorkspaceByName(t, server, "Should Not Exist") != 0 {
		t.Fatal("unknown pack created a workspace")
	}
}

// Create-from-template-pack: POST /workspaces with template_pack creates a
// workspace and attaches the pack's configuration set in the same request.

func TestCreateWorkspaceFromTemplatePackV2(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/workspaces", map[string]any{
		"name": "Helpdesk From Pack", "key": "HDPACK", "template_pack": "helpdesk",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	if decoded.Data.ID == 0 {
		t.Fatalf("create response has no workspace id")
	}

	bundle, _ := exportWorkspaceBundle(t, server, decoded.Data.ID)
	if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Helpdesk" {
		t.Fatalf("configuration set not attached: %+v", bundle.ConfigurationSet)
	}
}

func TestCreateWorkspaceFromTemplatePackV1(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v1/workspaces", map[string]any{
		"name": "Helpdesk V1 Pack", "key": "HDV1PK", "template_pack": "helpdesk",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created struct {
		ID int `json:"id"`
	}
	DecodeJSON(t, resp, &created)
	if created.ID == 0 {
		t.Fatalf("v1 create response has no workspace id")
	}

	bundle, _ := exportWorkspaceBundle(t, server, created.ID)
	if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Helpdesk" {
		t.Fatalf("configuration set not attached: %+v", bundle.ConfigurationSet)
	}
}

func TestCreateWorkspaceFromTemplatePackUnknown(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/workspaces", map[string]any{
		"name": "Should Not Exist Pack", "key": "NOPACK", "template_pack": "does-not-exist",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusUnprocessableEntity)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "unknown_pack" {
		t.Fatalf("error code = %q", errBody.Error.Code)
	}
	if findWorkspaceByName(t, server, "Should Not Exist Pack") != 0 {
		t.Fatal("unknown pack created a workspace")
	}
}

func TestCreateWorkspaceFromTemplatePackMutualExclusion(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	templateWS, _ := CreateTestWorkspace(t, server, "Pack Template Source", "")
	resp := MakeAuthRequest(t, server, http.MethodPost, "/v2/workspaces", map[string]any{
		"name": "Both Templates", "key": "BOTHTP", "template_pack": "helpdesk", "template_workspace_id": templateWS,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusUnprocessableEntity)
	if findWorkspaceByName(t, server, "Both Templates") != 0 {
		t.Fatal("conflicting templates created a workspace")
	}
}

func TestBuiltinPackApplyTargetValidation(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing_name", map[string]any{}},
		{"blank_name", map[string]any{"workspace_name": "   "}},
		{"workspace_id_rejected", map[string]any{"workspace_id": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/packs/helpdesk/apply", tc.body)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusBadRequest)
			var errBody struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			DecodeJSON(t, resp, &errBody)
			if errBody.Error.Code != "invalid_request" {
				t.Fatalf("error code = %q", errBody.Error.Code)
			}
		})
	}
}
