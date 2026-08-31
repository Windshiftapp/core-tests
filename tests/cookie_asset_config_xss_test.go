// cookie_asset_config_xss_test confirms the cookie-auth asset
// configuration handlers (asset sets, types, categories, statuses)
// strip injection vectors from decoded JSON bodies. WI-185 audit
// found these had request structs with free-form text (Name,
// Description, Icon, Color) being decoded straight into repository
// writes with no sanitize call anywhere — a real gap pre-this PR
// since these fields surface across every asset view.
//
// First of several WI-185 slices; only the cookie-auth asset config
// surface is covered here, larger sweep continues in follow-ups.
package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCookieAuth_AssetSetXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts) // also wires the admin session cookie

	resp := MakeAuthRequest(t, ts, http.MethodPost, "/asset-sets", map[string]interface{}{
		"name":        "<script>alert(1)</script>HQ",
		"description": "main<img src=x onerror=evil()>office",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create set: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("set name unsanitized: %q", name)
	}
	if name, _ := got["name"].(string); name != "HQ" {
		t.Fatalf("set name = %v, want 'HQ'", got["name"])
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("set description unsanitized: %q", desc)
	}
}

func TestCookieAuth_AssetTypeXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	setID := createTestAssetSet(t, ts, "TypeXSS")

	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/asset-sets/%d/types", setID),
		map[string]interface{}{
			"name":        "<script>alert(1)</script>Laptop",
			"description": "portable<img src=x onerror=alert(2)>computer",
			"icon":        "<script>icon()</script>📱",
			"color":       "#1f6feb<script>bad()</script>",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create type: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"name", "description", "icon", "color"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("asset type %s unsanitized: %q", field, val)
		}
	}
	if got["name"] != "Laptop" {
		t.Fatalf("asset type name = %v, want 'Laptop'", got["name"])
	}
}

func TestCookieAuth_AssetCategoryXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	setID := createTestAssetSet(t, ts, "CatXSS")

	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/asset-sets/%d/categories", setID),
		map[string]interface{}{
			"name":        "<script>alert(1)</script>Engineering",
			"description": "Eng<img src=x onerror=evil()>team",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create category: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	if name, _ := got["name"].(string); strings.Contains(name, "<script") {
		t.Fatalf("category name unsanitized: %q", name)
	}
	if desc, _ := got["description"].(string); strings.Contains(desc, "<img") || strings.Contains(desc, "onerror") {
		t.Fatalf("category description unsanitized: %q", desc)
	}
}

func TestCookieAuth_AssetStatusXSS(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	setID := createTestAssetSet(t, ts, "StatXSS")

	resp := MakeAuthRequest(t, ts, http.MethodPost, fmt.Sprintf("/asset-sets/%d/statuses", setID),
		map[string]interface{}{
			"name":        "<script>alert(1)</script>InRepair",
			"description": "Currently<img src=x onerror=alert(2)>being repaired",
			"color":       "#ff0000<script>bad()</script>",
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status: %d %s", resp.StatusCode, string(b))
	}
	var got map[string]interface{}
	DecodeJSON(t, resp, &got)
	for _, field := range []string{"name", "description", "color"} {
		val, _ := got[field].(string)
		if strings.Contains(val, "<script") || strings.Contains(val, "onerror") {
			t.Fatalf("asset status %s unsanitized: %q", field, val)
		}
	}
}

// createTestAssetSet seeds a set via the cookie-auth admin surface
// and returns its id. The body is plain text (no XSS payload) so the
// helper isn't itself part of the audit subject.
func createTestAssetSet(t *testing.T, ts *TestServer, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/asset-sets",
		map[string]interface{}{"name": name, "description": "seed"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("seed asset set: %d %s", resp.StatusCode, string(b))
	}
	var out map[string]interface{}
	DecodeJSON(t, resp, &out)
	return ExtractIDFromResponse(t, out)
}
