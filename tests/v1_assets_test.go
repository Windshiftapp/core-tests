// v1_assets_test exercises the bearer-token v1 asset surface:
// CRUD on assets, read-only set/type browse, and the assets:* token-scope
// gates. Set / type fixtures are seeded over the cookie-auth admin
// surface (v1 doesn't expose mutations for those in this slice).
package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// adminAssetToken mints a token with the full assets:* scope set as the
// boot-strapped admin user. The default-test bearer minted by
// CreateBearerToken uses the legacy "admin" scope which no longer auto-
// expands to assets:* (per asset-api-v1-security-review-2026-06-03
// finding 1 — assets are opt-in only). Granular and legacy scopes
// can't be combined on one token because expandLegacyScopes returns
// the moment it sees a legacy string, so this helper mints with
// granular scopes only.
func adminAssetToken(t *testing.T, ts *TestServer) string {
	t.Helper()
	return createTokenWithScopesAsUser(t, ts, "admin", "testpass123", []string{
		"assets:read", "assets:write", "assets:delete",
	})
}

// seedAssetSetAndType creates a fresh asset set + asset type via the
// cookie-auth admin surface. Returns (setID, assetTypeID). Use this from
// any v1 asset test that needs writable scaffolding.
func seedAssetSetAndType(t *testing.T, ts *TestServer, suffix string) (setID, assetTypeID int) {
	t.Helper()
	setBody := map[string]interface{}{
		"name":        "Asset v1 test " + suffix,
		"description": "fixture for v1 asset tests",
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/asset-sets", setBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("seed: create asset set: %d - %s", resp.StatusCode, string(body))
	}
	var setOut map[string]interface{}
	DecodeJSON(t, resp, &setOut)
	setID = ExtractIDFromResponse(t, setOut)

	typeBody := map[string]interface{}{
		"name":        "Laptop",
		"description": "fixture asset type",
		"icon":        "Laptop",
		"color":       "#1f6feb",
	}
	resp = MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/asset-sets/%d/types", setID), typeBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("seed: create asset type: %d - %s", resp.StatusCode, string(body))
	}
	var typeOut map[string]interface{}
	DecodeJSON(t, resp, &typeOut)
	assetTypeID = ExtractIDFromResponse(t, typeOut)
	return setID, assetTypeID
}

// TestV1Assets_HappyPath_AdminToken walks the full create → get → list →
// update → delete cycle against the v1 surface with an admin-minted bearer
// token. Doubles as a positive control for the slice-1..7 wiring — if this
// breaks, the route / handler / DTO pipeline is misaligned.
func TestV1Assets_HappyPath_AdminToken(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "happy")

	t.Run("list_empty_set", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var out map[string]interface{}
		DecodeJSON(t, resp, &out)
		data, _ := out["data"].([]interface{})
		if len(data) != 0 {
			t.Fatalf("expected empty set, got %d rows", len(data))
		}
		if _, hasPagination := out["pagination"]; !hasPagination {
			t.Fatalf("paginated response missing pagination envelope: %v", out)
		}
	})

	var createdID int
	t.Run("create", func(t *testing.T) {
		body := map[string]interface{}{
			"title":         "Lenovo X1",
			"description":   "Carbon Gen 11",
			"asset_tag":     "LAP-001",
			"asset_type_id": assetTypeID,
		}
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var out map[string]interface{}
		DecodeJSON(t, resp, &out)
		createdID = ExtractIDFromResponse(t, out)
		AssertJSONField(t, out, "title", "Lenovo X1")
		AssertJSONField(t, out, "asset_tag", "LAP-001")
		AssertJSONField(t, out, "set_id", float64(setID))
	})

	t.Run("get_by_id", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/assets/%d", createdID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var out map[string]interface{}
		DecodeJSON(t, resp, &out)
		AssertJSONField(t, out, "title", "Lenovo X1")
	})

	t.Run("update_partial", func(t *testing.T) {
		body := map[string]interface{}{"title": "Lenovo X1 Carbon"}
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPut,
			fmt.Sprintf("/rest/api/v1/assets/%d", createdID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var out map[string]interface{}
		DecodeJSON(t, resp, &out)
		AssertJSONField(t, out, "title", "Lenovo X1 Carbon")
		// asset_tag must survive — partial update preserves untouched fields.
		AssertJSONField(t, out, "asset_tag", "LAP-001")
	})

	t.Run("list_now_has_one", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var out map[string]interface{}
		DecodeJSON(t, resp, &out)
		data, _ := out["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("expected 1 row, got %d", len(data))
		}
	})

	t.Run("delete", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/assets/%d", createdID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNoContent)

		resp2 := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/assets/%d", createdID), nil)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusNotFound)
	})

	t.Run("list_sets_includes_seeded", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodGet, "/rest/api/v1/asset-sets", nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var sets []map[string]interface{}
		DecodeJSON(t, resp, &sets)
		var found bool
		for _, s := range sets {
			if int(s["id"].(float64)) == setID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("seeded set %d missing from list-sets response", setID)
		}
	})

	t.Run("list_types_in_set", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/types", setID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var types []map[string]interface{}
		DecodeJSON(t, resp, &types)
		if len(types) == 0 {
			t.Fatalf("expected at least one type in set %d", setID)
		}
	})
}

// TestV1Assets_TokenScopeEnforcement walks each route with a token that
// is missing the required scope and asserts the bearer-auth middleware
// refuses the call before the handler runs.
func TestV1Assets_TokenScopeEnforcement(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "scopes")

	// Need a non-admin user so the system-admin bypass doesn't swallow the
	// asset-role check. Asset routes still need a valid principal to load
	// the per-set role from, so we mint tokens off this user.
	uid, uname, password := CreateTestUserWithCredentials(t, ts, "asset_scope_user", "asset_scope_user@test.com")
	// Grant the user view+edit on the seeded set so any scope-allowed call
	// can reach the handler body. Asset-role wiring is via the cookie-auth
	// surface (POST /asset-sets/{id}/roles).
	editorRoleID := getAssetRoleID(t, ts, "Editor")
	assignAssetSetRole(t, ts, setID, uid, editorRoleID)

	readToken := createTokenWithScopesAsUser(t, ts, uname, password, []string{"assets:read"})
	writeToken := createTokenWithScopesAsUser(t, ts, uname, password, []string{"assets:read", "assets:write"})
	noScopeToken := createTokenWithScopesAsUser(t, ts, uname, password, []string{"items:read"})

	t.Run("read_route_rejects_without_assets_read", func(t *testing.T) {
		resp := MakeBearerRequestWithToken(t, ts, noScopeToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), nil)
		defer resp.Body.Close()
		// Bearer middleware returns 403 INSUFFICIENT_PERMISSION when the
		// scope is missing — distinct from the 404 the handler emits on
		// per-set role failures.
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("write_route_rejects_without_assets_write", func(t *testing.T) {
		body := map[string]interface{}{
			"title":         "ScopeTest",
			"asset_type_id": assetTypeID,
		}
		resp := MakeBearerRequestWithToken(t, ts, readToken, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusForbidden)
	})

	t.Run("delete_route_rejects_without_assets_delete", func(t *testing.T) {
		// Seed an asset via the write-scoped token so there's something to
		// attempt deleting.
		body := map[string]interface{}{
			"title":         "Deletable",
			"asset_type_id": assetTypeID,
		}
		resp := MakeBearerRequestWithToken(t, ts, writeToken, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), body)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var created map[string]interface{}
		DecodeJSON(t, resp, &created)
		id := ExtractIDFromResponse(t, created)

		// writeToken has read+write but no :delete — must be refused.
		resp2 := MakeBearerRequestWithToken(t, ts, writeToken, http.MethodDelete,
			fmt.Sprintf("/rest/api/v1/assets/%d", id), nil)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusForbidden)
	})

	t.Run("default_mint_includes_read_write_excludes_delete", func(t *testing.T) {
		// DefaultAgentScopes carries assets:read + assets:write — the
		// per-set asset role model is the guard, so the scope flag
		// alone never grants access to a set the user can't act on.
		// assets:delete stays opt-in (matches items:delete posture).
		_, regUname, regPassword := CreateTestUserWithCredentials(t, ts, "asset_default_user", "asset_default_user@test.com")
		defaultToken := createTokenWithDefaultScopes(t, ts, regUname, regPassword)

		// assets:read passes the scope check (handler may still 404 if
		// the user has no asset role on the seeded set, which is the
		// expected per-set guard).
		resp := MakeBearerRequestWithToken(t, ts, defaultToken, http.MethodGet, "/rest/api/v1/asset-sets", nil)
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), "INSUFFICIENT_PERMISSION") {
				t.Fatalf("default mint should include assets:read; got: %s", string(body))
			}
		}

		// assets:delete is opt-in only — must be refused.
		resp2 := MakeBearerRequestWithToken(t, ts, defaultToken, http.MethodDelete, "/rest/api/v1/assets/999999", nil)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusForbidden)
	})

	t.Run("assets_read_grants_read_only", func(t *testing.T) {
		// assets:read passes the scope check on reads (the route may still
		// 404 per the per-set role gate, but never 403) and must not carry
		// write access. This used to be asserted through the legacy 'read'
		// string, which expanded to every non-admin :read scope; legacy
		// strings are no longer valid mint input (WI-959), so the granular
		// scope stands in directly.
		_, uname, password := CreateTestUserWithCredentials(t, ts, "asset_read_user", "asset_read_user@test.com")
		readToken := createTokenWithScopesAsUser(t, ts, uname, password, []string{"assets:read"})

		resp := MakeBearerRequestWithToken(t, ts, readToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), nil)
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), "INSUFFICIENT_PERMISSION") {
				t.Fatalf("assets:read should pass the read scope check; got %s", string(body))
			}
		}

		// But it must NOT grant assets:write — POST should 403.
		resp2 := MakeBearerRequestWithToken(t, ts, readToken, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
			map[string]interface{}{"title": "x", "asset_type_id": assetTypeID})
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusForbidden)
	})

	t.Run("assets_write_grants_read_and_write_not_delete", func(t *testing.T) {
		// assets:write implies assets:read via the scope hierarchy, but
		// assets:delete stays opt-in so the destructive op is never granted
		// implicitly.
		_, uname, password := CreateTestUserWithCredentials(t, ts, "asset_write_user", "asset_write_user@test.com")
		writeToken := createTokenWithScopesAsUser(t, ts, uname, password, []string{"assets:write"})

		// Read passes scope check on the strength of assets:write alone.
		resp := MakeBearerRequestWithToken(t, ts, writeToken, http.MethodGet,
			fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), nil)
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden {
			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), "INSUFFICIENT_PERMISSION") {
				t.Fatalf("assets:write should satisfy assets:read; got %s", string(body))
			}
		}

		// Delete refused — assets:delete is opt-in only.
		resp2 := MakeBearerRequestWithToken(t, ts, writeToken, http.MethodDelete, "/rest/api/v1/assets/999999", nil)
		defer resp2.Body.Close()
		AssertStatusCode(t, resp2, http.StatusForbidden)
	})
}

// TestV1Assets_CreatorEmailHidden asserts the response from GET /assets/{id}
// (and the create response) does NOT carry creator.email under assets:read.
// Per asset-api-v1-security-review-2026-06-03 finding 2 — user emails
// gate on users:read, not assets:read.
func TestV1Assets_CreatorEmailHidden(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "email")

	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{"title": "Privacy", "asset_type_id": assetTypeID},
	)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, resp, &created)
	id := ExtractIDFromResponse(t, created)
	assertCreatorEmailAbsent(t, created)

	resp2 := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/assets/%d", id), nil)
	defer resp2.Body.Close()
	AssertStatusCode(t, resp2, http.StatusOK)
	var got map[string]interface{}
	DecodeJSON(t, resp2, &got)
	assertCreatorEmailAbsent(t, got)
}

// TestV1Assets_UnknownCustomFieldKeyRejected covers finding 4: keys not
// declared on the asset type must be rejected as a 400 validation error
// rather than silently stored.
func TestV1Assets_UnknownCustomFieldKeyRejected(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "cfschema")

	body := map[string]interface{}{
		"title":         "BadFields",
		"asset_type_id": assetTypeID,
		"custom_field_values": map[string]interface{}{
			"not_a_real_field": "x",
		},
	}
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)
	bodyBytes, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(bodyBytes), "VALIDATION_FAILED") {
		t.Fatalf("expected VALIDATION_FAILED, got %s", string(bodyBytes))
	}
	if !strings.Contains(string(bodyBytes), "not_a_real_field") {
		t.Fatalf("expected error to name the offending field, got %s", string(bodyBytes))
	}
}

// TestV1Assets_MutationsEmitAudit covers finding 3: v1 create / update /
// delete must produce audit rows. Read the audit log via the admin
// surface after the operations and check action_type rows are present
// for the same asset id.
func TestV1Assets_MutationsEmitAudit(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "audit")

	createResp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{"title": "AuditMe", "asset_type_id": assetTypeID},
	)
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	assetID := ExtractIDFromResponse(t, created)

	upResp := MakeBearerRequestWithToken(t, ts, token, http.MethodPut,
		fmt.Sprintf("/rest/api/v1/assets/%d", assetID),
		map[string]interface{}{"title": "AuditMe v2"},
	)
	upResp.Body.Close()

	delResp := MakeBearerRequestWithToken(t, ts, token, http.MethodDelete,
		fmt.Sprintf("/rest/api/v1/assets/%d", assetID), nil)
	delResp.Body.Close()

	auditResp := MakeAuthRequest(t, ts, http.MethodGet, "/admin/audit-logs?resource_type=asset&per_page=100", nil)
	defer auditResp.Body.Close()
	AssertStatusCode(t, auditResp, http.StatusOK)
	var auditOut struct {
		Entries []map[string]interface{} `json:"entries"`
	}
	if err := json.NewDecoder(auditResp.Body).Decode(&auditOut); err != nil {
		t.Fatalf("decode audit list: %v", err)
	}
	want := map[string]bool{"asset.create": false, "asset.update": false, "asset.delete": false}
	for _, row := range auditOut.Entries {
		rid, ok := row["resource_id"].(float64)
		if !ok || int(rid) != assetID {
			continue
		}
		action, _ := row["action_type"].(string)
		if _, tracked := want[action]; tracked {
			want[action] = true
		}
	}
	for action, found := range want {
		if !found {
			t.Fatalf("v1 mutation didn't emit audit row for %s on asset %d; got entries=%+v", action, assetID, auditOut.Entries)
		}
	}
}

// TestV1Assets_XSSSanitized covers asset-api-v1-security-review-followup
// 'V1 asset create/update bypasses legacy XSS sanitization': the cookie
// surface used to strip HTML on title / description, the v1 surface
// stored them verbatim. After the AssetService consolidation, both
// surfaces share the same input policy.
func TestV1Assets_XSSSanitized(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "xss")

	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{
			"title":         "<script>alert(1)</script>SafeTitle",
			"description":   "Before<script>evil()</script>After",
			"asset_tag":     "<img src=x onerror=alert(1)>TAG-001",
			"asset_type_id": assetTypeID,
		})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"title", "description", "asset_tag"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("XSS payload survived sanitization on %s: %q", field, val)
		}
	}
	// Cross-check via GET so we know it's not just the response-shape; the
	// stored row should also be clean.
	id := ExtractIDFromResponse(t, got)
	resp2 := MakeBearerRequestWithToken(t, ts, token, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/assets/%d", id), nil)
	defer resp2.Body.Close()
	AssertStatusCode(t, resp2, http.StatusOK)
	var stored map[string]interface{}
	DecodeJSON(t, resp2, &stored)
	for _, field := range []string{"title", "description", "asset_tag"} {
		val, _ := stored[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("XSS payload persisted under %s: %q", field, val)
		}
	}
}

// TestV1Assets_BearerAuditAttribution covers asset-api-v1-security-review-
// followup 'Bearer-token asset audit rows lack token attribution'. Audit
// row details on a v1-driven create must carry api_token_id +
// api_token_prefix + auth_method=bearer so a compromised-token
// investigation can attribute it.
func TestV1Assets_BearerAuditAttribution(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "audit-attr")

	createResp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{"title": "AttrMe", "asset_type_id": assetTypeID},
	)
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created map[string]interface{}
	DecodeJSON(t, createResp, &created)
	assetID := ExtractIDFromResponse(t, created)

	auditResp := MakeAuthRequest(t, ts, http.MethodGet, "/admin/audit-logs?resource_type=asset&per_page=100", nil)
	defer auditResp.Body.Close()
	AssertStatusCode(t, auditResp, http.StatusOK)
	var auditOut struct {
		Entries []map[string]interface{} `json:"entries"`
	}
	if err := json.NewDecoder(auditResp.Body).Decode(&auditOut); err != nil {
		t.Fatalf("decode audit list: %v", err)
	}
	var row map[string]interface{}
	for _, e := range auditOut.Entries {
		if rid, _ := e["resource_id"].(float64); int(rid) == assetID {
			if a, _ := e["action_type"].(string); a == "asset.create" {
				row = e
				break
			}
		}
	}
	if row == nil {
		t.Fatalf("no asset.create audit row for asset %d; entries=%+v", assetID, auditOut.Entries)
	}
	details, _ := row["details"].(map[string]interface{})
	if details == nil {
		t.Fatalf("audit row has no details block: %+v", row)
	}
	if m, _ := details["auth_method"].(string); m != "bearer" {
		t.Fatalf("auth_method=%q, want bearer; details=%+v", m, details)
	}
	if id, _ := details["api_token_id"].(float64); id == 0 {
		t.Fatalf("api_token_id missing or zero; details=%+v", details)
	}
	if prefix, _ := details["api_token_prefix"].(string); prefix == "" {
		t.Fatalf("api_token_prefix missing; details=%+v", details)
	}
}

// TestV1Assets_CustomFieldRequiredEnforcedOnCreate covers asset-api-v1-
// security-review-followup 'Custom-field schema validation remains
// incomplete' — required fields must be enforced on Create.
func TestV1Assets_CustomFieldRequiredEnforcedOnCreate(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "cf-req")
	requiredFieldID := addRequiredFieldToType(t, ts, assetTypeID, "hostname", "text")

	// Missing required field → 400.
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{"title": "ReqMissing", "asset_type_id": assetTypeID})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "required") || !strings.Contains(string(body), "hostname") {
		t.Fatalf("expected required-field error naming hostname; got %s", string(body))
	}

	// Supplying the required field → 201.
	resp2 := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{
			"title":               "ReqPresent",
			"asset_type_id":       assetTypeID,
			"custom_field_values": map[string]interface{}{fmt.Sprintf("%d", requiredFieldID): "host01"},
		})
	defer resp2.Body.Close()
	AssertStatusCode(t, resp2, http.StatusCreated)
}

// TestV1Assets_CustomFieldTypeMismatchRejected covers schema type checks.
func TestV1Assets_CustomFieldTypeMismatchRejected(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	token := adminAssetToken(t, ts)
	setID, assetTypeID := seedAssetSetAndType(t, ts, "cf-type")
	cpuCoresFieldID := addRequiredFieldToType(t, ts, assetTypeID, "cpu_cores", "number")

	// String for number field → 400.
	resp := MakeBearerRequestWithToken(t, ts, token, http.MethodPost,
		fmt.Sprintf("/rest/api/v1/asset-sets/%d/assets", setID),
		map[string]interface{}{
			"title":               "TypeMismatch",
			"asset_type_id":       assetTypeID,
			"custom_field_values": map[string]interface{}{fmt.Sprintf("%d", cpuCoresFieldID): "not-a-number"},
		})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "VALIDATION_FAILED") {
		t.Fatalf("expected VALIDATION_FAILED on number-field type mismatch, got %s", string(body))
	}
}

// addRequiredFieldToType creates a custom field of the given type and
// attaches it as required to the asset type. Returns the custom field id.
// Cookie-auth admin path — v1 doesn't expose mutations on the type config.
func addRequiredFieldToType(t *testing.T, ts *TestServer, assetTypeID int, name, fieldType string) int {
	t.Helper()
	customFieldID := CreateTestCustomField(t, ts, name, fieldType, "")
	body := map[string]interface{}{
		"fields": []map[string]interface{}{{
			"custom_field_id": customFieldID,
			"is_required":     true,
			"display_order":   0,
		}},
	}
	resp := MakeAuthRequest(t, ts, http.MethodPut,
		fmt.Sprintf("/asset-types/%d/fields", assetTypeID), body)
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("attach field to type: %d - %s", resp.StatusCode, string(b))
	}
	return customFieldID
}

// assertCreatorEmailAbsent enforces finding 2: asset responses never
// contain a creator.email field, regardless of whether the caller has
// users:read or not. Email gates on users:read separately.
func assertCreatorEmailAbsent(t *testing.T, asset map[string]interface{}) {
	t.Helper()
	creator, ok := asset["creator"].(map[string]interface{})
	if !ok {
		// No creator block at all is also fine — only fail when one
		// exists AND it carries email.
		return
	}
	if email, present := creator["email"]; present && email != "" {
		t.Fatalf("creator.email must be omitted on assets:read; got %q", email)
	}
}

// getAssetRoleID returns the role id for one of the bootstrapped role
// names (Viewer / Editor / Administrator). t.Fatal on missing.
func getAssetRoleID(t *testing.T, ts *TestServer, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodGet, "/asset-roles", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var roles []map[string]interface{}
	DecodeJSON(t, resp, &roles)
	for _, r := range roles {
		if rn, _ := r["name"].(string); rn == name {
			return int(r["id"].(float64))
		}
	}
	t.Fatalf("asset role %q not found", name)
	return 0
}

// assignAssetSetRole grants a user a role on an asset set via the
// cookie-auth admin surface.
func assignAssetSetRole(t *testing.T, ts *TestServer, setID, userID, roleID int) {
	t.Helper()
	body := map[string]interface{}{
		"user_id": userID,
		"role_id": roleID,
	}
	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/asset-sets/%d/roles", setID), body)
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("assign asset-set role: %d - %s", resp.StatusCode, string(body))
	}
}

// createTokenWithDefaultScopes mints a token via the same code path
// `ws init` uses: no explicit `permissions` field on the request, so the
// server applies DefaultAgentScopes.
func createTokenWithDefaultScopes(t *testing.T, ts *TestServer, username, password string) string {
	t.Helper()
	loginResp := makeRequest(t, http.MethodPost, ts.APIBase+"/auth/login", "",
		map[string]string{"email_or_username": username, "password": password}, nil)
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login failed: %d - %s", loginResp.StatusCode, string(body))
	}
	var sessionCookie string
	for _, c := range loginResp.Cookies() {
		if c.Name == "session" || c.Name == "windshift_session" {
			sessionCookie = c.String()
			break
		}
	}
	loginResp.Body.Close()
	tokenResp := makeRequest(t, http.MethodPost, ts.APIBase+"/api-tokens", "",
		map[string]interface{}{"name": "default-mint-" + username},
		map[string]string{"Cookie": sessionCookie},
	)
	defer tokenResp.Body.Close()
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&out); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if out.Token == "" {
		t.Fatalf("empty token in default-mint response")
	}
	return out.Token
}
