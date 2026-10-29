package tests

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestV2PrivacyAuditUserDirectoryRequiresPermission(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	_, name, password := CreateTestUserWithCredentials(t, ts, "directory_outsider", "directory_outsider@example.test")
	token := createTokenWithScopesAsUser(t, ts, name, password, []string{"users:read"})
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			response := MakeBearerRequestWithToken(t, ts, token, http.MethodGet, "/rest/api/"+version+"/users", nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusForbidden)
		})
	}
	caller := *ts
	caller.SessionCookie = CreateBearerTokenForUser(t, ts, name, password)
	denied := MakeAuthRequest(t, &caller, http.MethodGet, "/v2/users", nil)
	defer denied.Body.Close()
	AssertStatusCode(t, denied, http.StatusForbidden)
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, denied, &failure)
	if failure.Error.Code != "insufficient_permission" {
		t.Fatalf("denial code = %q", failure.Error.Code)
	}
	allowed := MakeAuthRequest(t, ts, http.MethodGet, "/v2/users", nil)
	defer allowed.Body.Close()
	AssertStatusCode(t, allowed, http.StatusOK)
	var directory struct {
		Data []map[string]any `json:"data"`
	}
	DecodeJSON(t, allowed, &directory)
	if len(directory.Data) != 2 {
		t.Fatalf("directory size = %d, want 2", len(directory.Data))
	}
}

func TestV2PrivacyAuditCustomFieldsHidePrivateAssetUsage(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	create := func(path string, body any) int {
		t.Helper()
		response := MakeAuthRequest(t, ts, http.MethodPost, "/v2"+path, body)
		defer response.Body.Close()
		AssertStatusCode(t, response, http.StatusCreated)
		var document struct {
			Data map[string]any `json:"data"`
		}
		DecodeJSON(t, response, &document)
		return ExtractIDFromResponse(t, document.Data)
	}
	setID := create("/asset-sets", map[string]any{"name": "Confidential acquisition inventory"})
	typeID := create(fmt.Sprintf("/asset-sets/%d/types", setID), map[string]any{"name": "Private hardware type", "icon": "Laptop", "color": "#1f6feb"})
	fieldID := CreateTestCustomField(t, ts, "Audit field", "text", "")
	assignment := MakeAuthRequest(t, ts, http.MethodPut, fmt.Sprintf("/v2/asset-types/%d/fields", typeID), map[string]any{
		"fields": []map[string]any{{"custom_field_id": fieldID, "is_required": false, "display_order": 0}},
	})
	AssertStatusCode(t, assignment, http.StatusOK)
	assignment.Body.Close()
	userID, name, password := CreateTestUserWithCredentials(t, ts, "asset_outsider", "asset_outsider@example.test")
	token := createTokenWithScopesAsUser(t, ts, name, password, []string{"custom-fields:read", "assets:read"})
	denied := MakeBearerRequestWithToken(t, ts, token, http.MethodGet, fmt.Sprintf("/rest/api/v2/asset-sets/%d", setID), nil)
	AssertStatusCode(t, denied, http.StatusNotFound)
	denied.Body.Close()
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			response := MakeBearerRequestWithToken(t, ts, token, http.MethodGet, "/rest/api/"+version+"/custom-fields", nil)
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var document struct {
				Data []map[string]any `json:"data"`
			}
			if version == "v1" {
				DecodeJSON(t, response, &document.Data)
			} else {
				DecodeJSON(t, response, &document)
			}
			for _, field := range document.Data {
				if intField(field, "id") == fieldID {
					if usages, ok := field["asset_type_usages"].([]any); ok && len(usages) > 0 {
						t.Errorf("private asset usage disclosed: %+v", usages)
					}
					return
				}
			}
			t.Fatal("custom field missing from catalog")
		})
	}
	adminWithScope := createTokenWithScopesAsUser(t, ts, "admin", "testpass123", []string{"custom-fields:read", "assets:read"})
	adminWithoutScope := createTokenWithScopesAsUser(t, ts, "admin", "testpass123", []string{"custom-fields:read"})
	for _, tc := range []struct {
		name, token      string
		session, visible bool
	}{
		{"admin session", "", true, true},
		{"admin bearer with asset scope", adminWithScope, false, true},
		{"admin bearer without asset scope", adminWithoutScope, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response *http.Response
			if tc.session {
				response = MakeAuthRequest(t, ts, http.MethodGet, "/v2/custom-fields", nil)
			} else {
				response = MakeBearerRequestWithToken(t, ts, tc.token, http.MethodGet, "/rest/api/v2/custom-fields", nil)
			}
			defer response.Body.Close()
			AssertStatusCode(t, response, http.StatusOK)
			var document struct {
				Data []struct {
					ID     int              `json:"id"`
					Usages []map[string]any `json:"asset_type_usages"`
				} `json:"data"`
			}
			DecodeJSON(t, response, &document)
			if len(document.Data) != 1 || document.Data[0].ID != fieldID {
				t.Fatalf("catalog = %+v", document.Data)
			}
			want := []map[string]any{}
			if tc.visible {
				want = append(want, map[string]any{"asset_type_name": "Private hardware type", "set_name": "Confidential acquisition inventory"})
			}
			if !reflect.DeepEqual(document.Data[0].Usages, want) {
				t.Fatalf("usages = %+v, want %+v", document.Data[0].Usages, want)
			}
		})
	}
	t.Run("viewer only sees usages in their own set", func(t *testing.T) {
		visibleSet := create("/asset-sets", map[string]any{"name": "Visible inventory"})
		visibleType := create(fmt.Sprintf("/asset-sets/%d/types", visibleSet), map[string]any{"name": "Visible type", "icon": "Laptop", "color": "#1f6feb"})
		fieldResponse := MakeAuthRequest(t, ts, http.MethodPut, fmt.Sprintf("/v2/asset-types/%d/fields", visibleType), map[string]any{
			"fields": []map[string]any{{"custom_field_id": fieldID, "is_required": false, "display_order": 0}},
		})
		AssertStatusCode(t, fieldResponse, http.StatusOK)
		fieldResponse.Body.Close()
		rolesResponse := MakeAuthRequest(t, ts, http.MethodGet, "/v2/asset-roles", nil)
		AssertStatusCode(t, rolesResponse, http.StatusOK)
		var roles struct {
			Data []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		DecodeJSON(t, rolesResponse, &roles)
		rolesResponse.Body.Close()
		roleID := 0
		for _, role := range roles.Data {
			if role.Name == "Viewer" {
				roleID = role.ID
			}
		}
		if roleID == 0 {
			t.Fatal("Viewer role missing")
		}
		grant := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/v2/asset-sets/%d/roles", visibleSet), map[string]any{"user_id": userID, "role_id": roleID})
		AssertStatusCode(t, grant, http.StatusCreated)
		grant.Body.Close()
		caller := *ts
		caller.SessionCookie = CreateBearerTokenForUser(t, ts, name, password)
		for _, mount := range []string{"session", "bearer"} {
			t.Run(mount, func(t *testing.T) {
				var response *http.Response
				if mount == "session" {
					response = MakeAuthRequest(t, &caller, http.MethodGet, "/v2/custom-fields", nil)
				} else {
					response = MakeBearerRequestWithToken(t, ts, token, http.MethodGet, "/rest/api/v2/custom-fields", nil)
				}
				defer response.Body.Close()
				AssertStatusCode(t, response, http.StatusOK)
				var document struct {
					Data []struct {
						ID     int              `json:"id"`
						Usages []map[string]any `json:"asset_type_usages"`
					} `json:"data"`
				}
				DecodeJSON(t, response, &document)
				want := []map[string]any{{"asset_type_name": "Visible type", "set_name": "Visible inventory"}}
				if len(document.Data) != 1 || document.Data[0].ID != fieldID || !reflect.DeepEqual(document.Data[0].Usages, want) {
					t.Fatalf("catalog = %+v, want only visible usage", document.Data)
				}
			})
		}
	})
}
