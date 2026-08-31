package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"windshift/internal/auth"
)

type scopeCatalogEntry struct {
	Scope         string `json:"scope"`
	Resource      string `json:"resource"`
	Action        string `json:"action"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	ResourceLabel string `json:"resource_label"`
	Admin         bool   `json:"admin"`
	AgentDefault  bool   `json:"agent_default"`
}

func fetchScopeCatalog(t *testing.T, ts *TestServer, cookie string) (*http.Response, []scopeCatalogEntry) {
	t.Helper()
	headers := map[string]string{}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	resp := makeRequest(t, http.MethodGet, ts.APIBase+"/api-tokens/scope-catalog", "", nil, headers)
	if resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	var out []scopeCatalogEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("decode scope catalog: %v (body %s)", err, string(body))
	}
	return resp, out
}

// The token pickers render whatever this endpoint returns, so it must expose
// every scope the server will accept — that equivalence is the whole point of
// serving the catalog instead of hand-maintaining copies in the UI (WI-961).
func TestScopeCatalogEndpointCoversEveryGrantableScope(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	cookie := CreateBearerToken(t, server)

	_, catalog := fetchScopeCatalog(t, server, cookie)
	if len(catalog) == 0 {
		t.Fatal("scope catalog endpoint returned no entries")
	}

	served := make(map[string]scopeCatalogEntry, len(catalog))
	for _, entry := range catalog {
		served[entry.Scope] = entry
	}
	for _, scope := range auth.AllValidScopes {
		entry, ok := served[scope]
		if !ok {
			t.Errorf("scope %q is grantable but absent from the catalog endpoint", scope)
			continue
		}
		if entry.Label == "" || entry.Description == "" || entry.ResourceLabel == "" {
			t.Errorf("scope %q served without the metadata a picker needs: %+v", scope, entry)
		}
	}
	if len(catalog) != len(auth.AllValidScopes) {
		t.Errorf("catalog served %d scopes, server accepts %d", len(catalog), len(auth.AllValidScopes))
	}
}

// The specific regression: time:read/time:write must be offerable by the UI,
// and must be part of the advertised agent default set.
func TestScopeCatalogEndpointExposesTimeScopes(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	cookie := CreateBearerToken(t, server)

	_, catalog := fetchScopeCatalog(t, server, cookie)
	byScope := make(map[string]scopeCatalogEntry, len(catalog))
	for _, entry := range catalog {
		byScope[entry.Scope] = entry
	}

	for _, scope := range []string{"time:read", "time:write"} {
		entry, ok := byScope[scope]
		if !ok {
			t.Fatalf("%q missing from the catalog — the token UI cannot grant it", scope)
		}
		if !entry.AgentDefault {
			t.Errorf("%q should be part of the default agent scope set", scope)
		}
	}
	if entry, ok := byScope["time:delete"]; !ok || entry.AgentDefault {
		t.Errorf("time:delete should be grantable but not a default (got %+v, present=%v)", entry, ok)
	}
	if entry, ok := byScope["mcp:access"]; !ok || !entry.AgentDefault {
		t.Errorf("mcp:access should be grantable and a default (got %+v, present=%v)", entry, ok)
	}
}

// A token minted with the scopes the catalog marks as defaults must be
// accepted verbatim by the mint endpoint — otherwise the picker's
// "Agent default" preset would produce a 400.
func TestScopeCatalogDefaultsAreAcceptedByTokenMint(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	cookie := CreateBearerToken(t, server)

	_, catalog := fetchScopeCatalog(t, server, cookie)
	var defaults []string
	for _, entry := range catalog {
		if entry.AgentDefault {
			defaults = append(defaults, entry.Scope)
		}
	}
	if len(defaults) == 0 {
		t.Fatal("catalog advertised no default scopes")
	}

	resp := makeRequest(t, http.MethodPost, server.APIBase+"/api-tokens", "",
		map[string]interface{}{"name": "catalog-defaults", "permissions": defaults},
		map[string]string{"Cookie": cookie})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("minting with the catalog's default scopes failed: %d - %s", resp.StatusCode, string(body))
	}
}

func TestScopeCatalogEndpointRequiresAuth(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())

	resp, _ := fetchScopeCatalog(t, server, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without a session, got %d", resp.StatusCode)
	}
}
