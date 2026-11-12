package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestBoardViewSettingsHTTPContract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "View settings", shortKey("VWS"))

	created := MakeAuthRequest(t, server, http.MethodPost, "/v2/collections", map[string]any{
		"name": "View settings collection", "workspace_id": workspaceID, "ql_query": "",
	})
	defer created.Body.Close()
	AssertStatusCode(t, created, http.StatusCreated)
	var createdDoc struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, created, &createdDoc)
	collectionID := createdDoc.Data.ID

	base := fmt.Sprintf("/v2/workspaces/%d/board-configuration", workspaceID)
	collectionBase := fmt.Sprintf("/v2/collections/%d/board-configuration", collectionID)

	t.Run("workspace override round-trips and rejects bad input", func(t *testing.T) {
		put := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"columns":       []any{},
			"view_settings": map[string]any{"enabled_views": []string{"board", "list"}},
		})
		defer put.Body.Close()
		AssertStatusCode(t, put, http.StatusOK)

		get := MakeAuthRequest(t, server, http.MethodGet, base, nil)
		defer get.Body.Close()
		AssertStatusCode(t, get, http.StatusOK)
		var config struct {
			Data struct {
				ViewSettings struct {
					EnabledViews *[]string `json:"enabled_views"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, get, &config)
		if config.Data.ViewSettings.EnabledViews == nil || len(*config.Data.ViewSettings.EnabledViews) != 2 {
			t.Fatalf("workspace view settings = %+v, want [board list]", config.Data.ViewSettings)
		}

		unknown := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"kanban"}},
		})
		defer unknown.Body.Close()
		AssertStatusCode(t, unknown, http.StatusBadRequest)
	})

	t.Run("completed visibility and limit round-trip", func(t *testing.T) {
		put := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{
				"completed_visibility": "limit",
				"completed_limit":      250,
			},
		})
		defer put.Body.Close()
		AssertStatusCode(t, put, http.StatusOK)

		get := MakeAuthRequest(t, server, http.MethodGet, base, nil)
		defer get.Body.Close()
		AssertStatusCode(t, get, http.StatusOK)
		var config struct {
			Data struct {
				ViewSettings struct {
					CompletedVisibility *string `json:"completed_visibility"`
					CompletedLimit      *int    `json:"completed_limit"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, get, &config)
		if config.Data.ViewSettings.CompletedVisibility == nil || *config.Data.ViewSettings.CompletedVisibility != "limit" {
			t.Fatalf("completed_visibility = %v, want limit", config.Data.ViewSettings.CompletedVisibility)
		}
		if config.Data.ViewSettings.CompletedLimit == nil || *config.Data.ViewSettings.CompletedLimit != 250 {
			t.Fatalf("completed_limit = %v, want 250", config.Data.ViewSettings.CompletedLimit)
		}

		// An out-of-range cap is rejected instead of silently clamped.
		outOfRange := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"completed_visibility": "limit", "completed_limit": 0},
		})
		defer outOfRange.Body.Close()
		AssertStatusCode(t, outOfRange, http.StatusBadRequest)

		// Restore the views-only override later subtests rely on.
		restore := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board", "list"}},
		})
		defer restore.Body.Close()
		AssertStatusCode(t, restore, http.StatusOK)
	})

	t.Run("collection inherits until it overrides and resets", func(t *testing.T) {
		get := MakeAuthRequest(t, server, http.MethodGet, collectionBase, nil)
		defer get.Body.Close()
		AssertStatusCode(t, get, http.StatusOK)
		var inherited struct {
			Data struct {
				ID                    int  `json:"id"`
				ViewSettingsInherited bool `json:"view_settings_inherited"`
				ViewSettings          struct {
					EnabledViews *[]string `json:"enabled_views"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, get, &inherited)
		if !inherited.Data.ViewSettingsInherited {
			t.Fatal("collection without row must report inherited settings")
		}
		if inherited.Data.ViewSettings.EnabledViews == nil || len(*inherited.Data.ViewSettings.EnabledViews) != 2 {
			t.Fatalf("inherited views = %+v, want the workspace override [board list]", inherited.Data.ViewSettings)
		}

		override := MakeAuthRequest(t, server, http.MethodPut, collectionBase, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board"}},
		})
		defer override.Body.Close()
		AssertStatusCode(t, override, http.StatusOK)

		getAfter := MakeAuthRequest(t, server, http.MethodGet, collectionBase, nil)
		defer getAfter.Body.Close()
		AssertStatusCode(t, getAfter, http.StatusOK)
		var overridden struct {
			Data struct {
				ViewSettingsInherited bool `json:"view_settings_inherited"`
				ViewSettings          struct {
					EnabledViews *[]string `json:"enabled_views"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, getAfter, &overridden)
		if overridden.Data.ViewSettingsInherited {
			t.Fatal("explicit collection override must not report inherited")
		}
		if overridden.Data.ViewSettings.EnabledViews == nil || len(*overridden.Data.ViewSettings.EnabledViews) != 1 {
			t.Fatalf("overridden views = %+v, want [board]", overridden.Data.ViewSettings)
		}

		// An empty set is rejected; the explicit null resets to inherit.
		emptySet := MakeAuthRequest(t, server, http.MethodPut, collectionBase, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{}},
		})
		defer emptySet.Body.Close()
		AssertStatusCode(t, emptySet, http.StatusBadRequest)

		reset := MakeAuthRequest(t, server, http.MethodPut, collectionBase, map[string]any{
			"view_settings": map[string]any{"enabled_views": nil},
		})
		defer reset.Body.Close()
		AssertStatusCode(t, reset, http.StatusOK)

		getReset := MakeAuthRequest(t, server, http.MethodGet, collectionBase, nil)
		defer getReset.Body.Close()
		AssertStatusCode(t, getReset, http.StatusOK)
		var afterReset struct {
			Data struct {
				ViewSettingsInherited bool `json:"view_settings_inherited"`
			} `json:"data"`
		}
		DecodeJSON(t, getReset, &afterReset)
		if !afterReset.Data.ViewSettingsInherited {
			t.Fatal("collection after reset must inherit again")
		}
	})

	t.Run("workspace scope toggles workspace nav ids", func(t *testing.T) {
		put := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board", "agents", "pages"}},
		})
		defer put.Body.Close()
		AssertStatusCode(t, put, http.StatusOK)

		get := MakeAuthRequest(t, server, http.MethodGet, base, nil)
		defer get.Body.Close()
		AssertStatusCode(t, get, http.StatusOK)
		var config struct {
			Data struct {
				ViewSettings struct {
					EnabledViews *[]string `json:"enabled_views"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, get, &config)
		if config.Data.ViewSettings.EnabledViews == nil || len(*config.Data.ViewSettings.EnabledViews) != 3 {
			t.Fatalf("workspace nav settings = %+v, want [board agents pages]", config.Data.ViewSettings)
		}

		// Test-management entries are not toggleable anywhere.
		testEntry := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board", "test-cases"}},
		})
		defer testEntry.Body.Close()
		AssertStatusCode(t, testEntry, http.StatusBadRequest)

		// Restore the views-only override the other subtests rely on.
		restore := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board", "list"}},
		})
		defer restore.Body.Close()
		AssertStatusCode(t, restore, http.StatusOK)
	})

	t.Run("collection scope rejects workspace-only nav ids", func(t *testing.T) {
		put := MakeAuthRequest(t, server, http.MethodPut, collectionBase, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board", "agents"}},
		})
		defer put.Body.Close()
		AssertStatusCode(t, put, http.StatusBadRequest)
	})

	t.Run("bootstrap carries the effective set", func(t *testing.T) {
		bootstrap := MakeAuthRequest(t, server, http.MethodGet, collectionBase+"/bootstrap", nil)
		defer bootstrap.Body.Close()
		AssertStatusCode(t, bootstrap, http.StatusOK)
		var payload struct {
			Data struct {
				BoardConfiguration *struct {
					ViewSettings struct {
						EnabledViews *[]string `json:"enabled_views"`
					} `json:"view_settings"`
				} `json:"board_configuration"`
			} `json:"data"`
		}
		DecodeJSON(t, bootstrap, &payload)
		if payload.Data.BoardConfiguration == nil || payload.Data.BoardConfiguration.ViewSettings.EnabledViews == nil {
			t.Fatalf("bootstrap board configuration = %+v, want effective view settings", payload.Data.BoardConfiguration)
		}
		if len(*payload.Data.BoardConfiguration.ViewSettings.EnabledViews) != 2 {
			t.Fatalf("bootstrap views = %v, want [board list]", *payload.Data.BoardConfiguration.ViewSettings.EnabledViews)
		}

		workspaceBootstrap := MakeAuthRequest(t, server, http.MethodGet, base+"/bootstrap", nil)
		defer workspaceBootstrap.Body.Close()
		AssertStatusCode(t, workspaceBootstrap, http.StatusOK)
	})

	t.Run("unknown view_settings keys are rejected", func(t *testing.T) {
		put := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board"}, "collapsed": true},
		})
		defer put.Body.Close()
		AssertStatusCode(t, put, http.StatusBadRequest)
	})

	t.Run("cross-workspace write is denied", func(t *testing.T) {
		otherWorkspaceID, _ := CreateTestWorkspace(t, server, "View settings other", shortKey("VWO"))
		CreateTestUserWithCredentials(t, server, "vwsseconduser", "vws-second@test.com")
		secondUserCookie := CreateBearerTokenForUser(t, server, "vwsseconduser", "testpass123")

		// A non-member cannot write the first workspace's board configuration.
		denied := MakeAuthRequestWithToken(t, server, secondUserCookie, http.MethodPut,
			fmt.Sprintf("/v2/workspaces/%d/board-configuration", workspaceID),
			map[string]any{"view_settings": map[string]any{"enabled_views": []string{"board"}}})
		defer denied.Body.Close()
		if denied.StatusCode != http.StatusForbidden && denied.StatusCode != http.StatusNotFound {
			t.Fatalf("cross-workspace write status = %d, want 403 or 404", denied.StatusCode)
		}

		// An admin of another workspace manages their own scope independently.
		// The seeded default view (board) must stay in the set.
		other := MakeAuthRequest(t, server, http.MethodPut,
			fmt.Sprintf("/v2/workspaces/%d/board-configuration", otherWorkspaceID),
			map[string]any{"view_settings": map[string]any{"enabled_views": []string{"board", "map"}}})
		defer other.Body.Close()
		AssertStatusCode(t, other, http.StatusOK)
	})

	t.Run("patch merges only the fields present", func(t *testing.T) {
		put := MakeAuthRequest(t, server, http.MethodPut, base, map[string]any{
			"columns": []any{
				map[string]any{"name": "In progress", "display_order": 0, "color": "#123456", "status_ids": []any{}},
			},
			"card_fields": []any{
				map[string]any{"field_identifier": "title", "field_type": "system", "display_order": 0, "width": 4},
			},
			"view_settings": map[string]any{"enabled_views": []string{"board", "list"}},
		})
		defer put.Body.Close()
		AssertStatusCode(t, put, http.StatusOK)

		// The navigation config page sends only view_settings; the columns,
		// card fields, and other settings must survive the merge patch.
		patch := MakeAuthRequest(t, server, http.MethodPatch, base, map[string]any{
			"view_settings": map[string]any{"enabled_views": []string{"board"}},
		})
		defer patch.Body.Close()
		AssertStatusCode(t, patch, http.StatusOK)

		get := MakeAuthRequest(t, server, http.MethodGet, base, nil)
		defer get.Body.Close()
		AssertStatusCode(t, get, http.StatusOK)
		var config struct {
			Data struct {
				Columns []struct {
					Name string `json:"name"`
				} `json:"columns"`
				CardFields []struct {
					FieldIdentifier string `json:"field_identifier"`
				} `json:"card_fields"`
				ViewSettings struct {
					EnabledViews *[]string `json:"enabled_views"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, get, &config)
		if len(config.Data.Columns) != 1 || config.Data.Columns[0].Name != "In progress" {
			t.Fatalf("columns after patch = %+v, want the saved column preserved", config.Data.Columns)
		}
		if len(config.Data.CardFields) != 1 || config.Data.CardFields[0].FieldIdentifier != "title" {
			t.Fatalf("card_fields after patch = %+v, want them preserved", config.Data.CardFields)
		}
		if config.Data.ViewSettings.EnabledViews == nil || len(*config.Data.ViewSettings.EnabledViews) != 1 || (*config.Data.ViewSettings.EnabledViews)[0] != "board" {
			t.Fatalf("view settings after patch = %+v, want [board]", config.Data.ViewSettings)
		}

		// Unknown top-level fields still fail closed under the strict decoder.
		unknown := MakeAuthRequest(t, server, http.MethodPatch, base, map[string]any{"bogus": true})
		defer unknown.Body.Close()
		AssertStatusCode(t, unknown, http.StatusBadRequest)
	})
}
