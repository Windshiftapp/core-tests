package tests

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"windshift/internal/utils"
)

// fakeDocmostServer serves the endpoints the Docmost client uses: the
// authenticated search API and the OAuth register/token endpoints.
func fakeDocmostServer(t *testing.T, validToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/register":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"client_id":"fake-client","token_endpoint_auth_method":"none"}`))
			return
		case "/api/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"oauth-access","refresh_token":"oauth-refresh","token_type":"Bearer","scope":"read write","expires_in":3600}`))
			return
		case "/api/search":
		default:
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+validToken && r.Header.Get("Authorization") != "Bearer oauth-access" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Unauthorized","statusCode":401}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"p1","slugId":"AbC","title":"Deployment Runbook","highlight":"the <b>deploy</b> steps","rank":0.9,"space":{"name":"Engineering","slug":"engineering"}}]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// allowLocalOutbound permits the loopback Docmost fake and restores the prior
// process-wide policy.
func allowLocalOutbound(t *testing.T) {
	t.Helper()
	previous := utils.AllowLocalConnections()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(previous) })
}

// docmostTestServer starts an in-process server with an authenticated admin
// session and a workspace.
func docmostTestServer(t *testing.T) (*TestServer, int) {
	t.Helper()
	allowLocalOutbound(t)
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	workspaceID, _ := CreateTestWorkspace(t, ts, "Docmost Workspace", shortKey("DOC"))
	return ts, workspaceID
}

// createDocmostProvider registers the Docmost integration provider the way an
// admin would, and returns its id.
func createDocmostProvider(t *testing.T, ts *TestServer, baseURL string) string {
	t.Helper()
	resp := MakeAuthRequest(t, ts, http.MethodPost, "/admin/integration-providers", map[string]any{
		"slug":            "docmost",
		"name":            "Docmost",
		"provider_type":   "docmost",
		"enabled":         true,
		"provider_config": fmt.Sprintf(`{"base_url":%q}`, baseURL),
	})
	AssertStatusCode(t, resp, http.StatusCreated)
	resp.Body.Close()

	listResp := MakeAuthRequest(t, ts, http.MethodGet, "/admin/integration-providers", nil)
	AssertStatusCode(t, listResp, http.StatusOK)
	defer listResp.Body.Close()
	var list []struct {
		ID string `json:"id"`
	}
	DecodeJSON(t, listResp, &list)
	for _, p := range list {
		if p.ID != "" {
			return p.ID
		}
	}
	t.Fatal("docmost provider was not listed after create")
	return ""
}

type docmostSearchBody struct {
	Results []struct {
		Source  string `json:"source"`
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	} `json:"results"`
	Sources []struct {
		Source string `json:"source"`
		State  string `json:"state"`
	} `json:"sources"`
}

// TestDocmostIntegrationFlow drives the shared connected-accounts surface for
// Docmost: admin config, user OAuth connect, search, and disconnect.
func TestDocmostIntegrationFlow(t *testing.T) {
	ts, workspaceID := docmostTestServer(t)
	fake := fakeDocmostServer(t, "good-token")
	providerID := createDocmostProvider(t, ts, fake.URL)

	t.Run("the shared available-providers list includes Docmost", func(t *testing.T) {
		resp := MakeAuthRequest(t, ts, http.MethodGet, "/users/me/integration-connections/available", nil)
		AssertStatusCode(t, resp, http.StatusOK)
		defer resp.Body.Close()
		var providers []struct {
			ID           string `json:"id"`
			ProviderType string `json:"provider_type"`
		}
		DecodeJSON(t, resp, &providers)
		found := false
		for _, p := range providers {
			if p.ID == providerID && p.ProviderType == "docmost" {
				found = true
			}
		}
		if !found {
			t.Fatalf("docmost not in available providers: %+v", providers)
		}
	})

	var state string
	t.Run("start returns a Docmost consent URL", func(t *testing.T) {
		resp := MakeAuthRequest(t, ts, http.MethodGet, "/integrations/oauth/docmost/start", nil)
		AssertStatusCode(t, resp, http.StatusOK)
		defer resp.Body.Close()
		var body struct {
			AuthURL string `json:"auth_url"`
		}
		DecodeJSON(t, resp, &body)
		parsed, err := url.Parse(body.AuthURL)
		if err != nil {
			t.Fatalf("parse auth_url: %v", err)
		}
		if parsed.Path != "/oauth/consent" || parsed.Query().Get("client_id") != "fake-client" || parsed.Query().Get("code_challenge_method") != "S256" {
			t.Fatalf("auth_url = %q", body.AuthURL)
		}
		state = parsed.Query().Get("state")
		if state == "" {
			t.Fatal("auth_url has no state")
		}
	})

	t.Run("callback stores the connection", func(t *testing.T) {
		callbackURL := ts.APIBase + "/integrations/oauth/docmost/callback?" + url.Values{"state": {state}, "code": {"auth-code"}}.Encode()
		req, err := http.NewRequest(http.MethodGet, callbackURL, nil)
		if err != nil {
			t.Fatalf("build callback: %v", err)
		}
		req.Header.Set("Cookie", ts.SessionCookie)
		noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "oauth=success") {
			t.Fatalf("callback status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
		}

		connResp := MakeAuthRequest(t, ts, http.MethodGet, "/users/me/integration-connections", nil)
		AssertStatusCode(t, connResp, http.StatusOK)
		defer connResp.Body.Close()
		var connections []struct {
			IntegrationProviderID string `json:"integration_provider_id"`
		}
		DecodeJSON(t, connResp, &connections)
		found := false
		for _, c := range connections {
			if c.IntegrationProviderID == providerID {
				found = true
			}
		}
		if !found {
			t.Fatalf("docmost connection not listed: %+v", connections)
		}
	})

	t.Run("knowledge search returns the Docmost hit", func(t *testing.T) {
		resp := MakeAuthRequest(t, ts, http.MethodGet, fmt.Sprintf("/workspaces/%d/knowledge/search?q=deploy", workspaceID), nil)
		AssertStatusCode(t, resp, http.StatusOK)
		defer resp.Body.Close()
		var body docmostSearchBody
		DecodeJSON(t, resp, &body)
		if len(body.Results) != 1 || body.Results[0].Source != "docmost" {
			t.Fatalf("results = %+v, want one docmost hit", body.Results)
		}
		if body.Results[0].URL != fake.URL+"/s/engineering/p/AbC" || body.Results[0].Snippet != "the deploy steps" {
			t.Fatalf("result = %+v", body.Results[0])
		}
	})

	t.Run("disconnect removes the connection", func(t *testing.T) {
		resp := MakeAuthRequest(t, ts, http.MethodDelete, "/users/me/integration-connections/"+providerID, nil)
		AssertStatusCode(t, resp, http.StatusNoContent)
		resp.Body.Close()

		searchResp := MakeAuthRequest(t, ts, http.MethodGet, fmt.Sprintf("/workspaces/%d/knowledge/search?q=deploy", workspaceID), nil)
		AssertStatusCode(t, searchResp, http.StatusOK)
		defer searchResp.Body.Close()
		var body docmostSearchBody
		DecodeJSON(t, searchResp, &body)
		if len(body.Sources) != 1 || body.Sources[0].State != "not_connected" {
			t.Fatalf("sources = %+v, want not_connected after disconnect", body.Sources)
		}
	})
}

// TestDocmostProviderRequiresAdmin proves the base connection is admin-only.
func TestDocmostProviderRequiresAdmin(t *testing.T) {
	ts, _ := docmostTestServer(t)
	fake := fakeDocmostServer(t, "good-token")

	memberID, username, password := CreateTestUserWithCredentials(t, ts, "docmost-nonadmin", "docmost-nonadmin@test.com")
	_ = memberID
	memberCookie := CreateBearerTokenForUser(t, ts, username, password)

	resp := MakeAuthRequestWithToken(t, ts, memberCookie, http.MethodPost, "/admin/integration-providers", map[string]any{
		"slug":            "docmost",
		"name":            "Docmost",
		"provider_type":   "docmost",
		"enabled":         true,
		"provider_config": fmt.Sprintf(`{"base_url":%q}`, fake.URL),
	})
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("non-admin create status = %d, want 403/404 (%s)", resp.StatusCode, body)
	}
	resp.Body.Close()
}
