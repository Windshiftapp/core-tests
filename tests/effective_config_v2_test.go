package tests

import (
	"fmt"
	"net/http"
	"testing"
)

// TestEffectiveConfigV2 exercises the workspace effective-configuration
// endpoint: assigned→default config set fallback, the edit/view → create
// screen chain, hydrated screens, and the caller-visibility denial contract.
func TestEffectiveConfigV2(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	screenPayload := func(name string) map[string]interface{} {
		return map[string]interface{}{"name": name}
	}
	createScreen := func(name string) int {
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/screens", screenPayload(name))
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var created struct {
			ID int `json:"id"`
		}
		DecodeJSON(t, resp, &created)
		if created.ID == 0 {
			t.Fatalf("screen %q created without id", name)
		}
		return created.ID
	}
	createConfigSet := func(name string, workspaceID int, create, edit, view int) int {
		payload := map[string]interface{}{
			"name":          name,
			"workspace_ids": []int{workspaceID},
		}
		if create != 0 {
			payload["create_screen_id"] = create
		}
		if edit != 0 {
			payload["edit_screen_id"] = edit
		}
		if view != 0 {
			payload["view_screen_id"] = view
		}
		resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", payload)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusCreated)
		var created struct {
			Data struct {
				ConfigurationSet struct {
					ID int `json:"id"`
				} `json:"configuration_set"`
			} `json:"data"`
		}
		DecodeJSON(t, resp, &created)
		if created.Data.ConfigurationSet.ID == 0 {
			t.Fatalf("config set %q created without id", name)
		}
		return created.Data.ConfigurationSet.ID
	}

	type effectiveConfig struct {
		ConfigSetID    *int `json:"config_set_id"`
		Screens        struct {
			Create int `json:"create"`
			Edit   int `json:"edit"`
			View   int `json:"view"`
		} `json:"screens"`
		EditScreen struct {
			ID     int `json:"id"`
			Fields []struct {
				FieldType       string `json:"field_type"`
				FieldIdentifier string `json:"field_identifier"`
			} `json:"fields"`
		} `json:"edit_screen"`
		ViewScreen *struct {
			ID int `json:"id"`
		} `json:"view_screen"`
		Priorities []map[string]interface{} `json:"priorities"`
	}
	getConfig := func(workspaceID int, query string) effectiveConfig {
		resp := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/effective-config%s", workspaceID, query), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)
		var config effectiveConfig
		DecodeJSON(t, resp, &config)
		return config
	}

	// A workspace without an assignment runs on the seeded global default set.
	// That set ships a lean create screen plus a full edit/view screen, so the
	// create context must resolve to a different screen than edit/view.
	t.Run("unassigned workspace falls back to default config set", func(t *testing.T) {
		wsID, _ := CreateTestWorkspace(t, server, "Effective Config Unassigned", "")
		config := getConfig(wsID, "")

		defaultSetID := GetDefaultConfigurationSet(t, server)
		if config.ConfigSetID == nil || *config.ConfigSetID != defaultSetID {
			t.Fatalf("config_set_id = %v, want %d", config.ConfigSetID, defaultSetID)
		}
		if config.Screens.Create == 0 || config.Screens.Edit == 0 {
			t.Fatalf("screens = %+v, want concrete create and edit screens", config.Screens)
		}
		if config.Screens.Create == config.Screens.Edit {
			t.Fatalf("screens = %+v, want a separate create screen from edit", config.Screens)
		}
		if config.Screens.View != config.Screens.Edit {
			t.Fatalf("screens.view = %d, want the edit screen %d", config.Screens.View, config.Screens.Edit)
		}
		if config.EditScreen.ID != config.Screens.Edit {
			t.Fatalf("edit_screen.id = %d, want %d", config.EditScreen.ID, config.Screens.Edit)
		}
		if len(config.EditScreen.Fields) == 0 {
			t.Fatal("edit_screen.fields is empty, want hydrated screen fields")
		}
		if config.ViewScreen != nil {
			t.Fatalf("view_screen = %+v, want null when view resolves to the edit screen", config.ViewScreen)
		}

		// The lean create screen keeps due_date but drops the planning fields
		// that live on the edit screen.
		createFields := map[string]bool{}
		for _, field := range getV2ScreenFields(t, server, config.Screens.Create) {
			createFields[field.FieldIdentifier] = true
		}
		for _, planning := range []string{"milestone", "iteration", "start_date", "end_date"} {
			if createFields[planning] {
				t.Errorf("create screen exposes planning field %q, want it only on the edit screen", planning)
			}
		}
		if !createFields["due_date"] {
			t.Error("create screen is missing due_date")
		}

		if len(config.Priorities) == 0 {
			t.Fatal("priorities is empty, want seeded config-set priorities")
		}
	})

	// An assigned set with distinct screens wins, including the edit → create
	// chain and a hydrated, differing view screen.
	t.Run("assigned config set with distinct screens", func(t *testing.T) {
		wsID, _ := CreateTestWorkspace(t, server, "Effective Config Assigned", "")
		createScreenID := createScreen("Effective Assigned Create")
		viewScreenID := createScreen("Effective Assigned View")
		setID := createConfigSet("Effective Assigned Set", wsID, createScreenID, 0, viewScreenID)

		config := getConfig(wsID, "")
		if config.ConfigSetID == nil || *config.ConfigSetID != setID {
			t.Fatalf("config_set_id = %v, want %d", config.ConfigSetID, setID)
		}
		if config.Screens.Create != createScreenID {
			t.Fatalf("screens.create = %d, want %d", config.Screens.Create, createScreenID)
		}
		// Only a create screen is configured, so edit uses it.
		if config.Screens.Edit != createScreenID {
			t.Fatalf("screens.edit = %d, want create fallback %d", config.Screens.Edit, createScreenID)
		}
		if config.Screens.View != viewScreenID {
			t.Fatalf("screens.view = %d, want %d", config.Screens.View, viewScreenID)
		}
		if config.EditScreen.ID != createScreenID {
			t.Fatalf("edit_screen.id = %d, want %d", config.EditScreen.ID, createScreenID)
		}
		if config.ViewScreen == nil || config.ViewScreen.ID != viewScreenID {
			t.Fatalf("view_screen = %+v, want hydrated screen %d", config.ViewScreen, viewScreenID)
		}
	})

	// An unknown item type resolves the config-set defaults — no override.
	t.Run("unknown item type id resolves config set defaults", func(t *testing.T) {
		wsID, _ := CreateTestWorkspace(t, server, "Effective Config Type", "")
		createScreenID := createScreen("Effective Type Create")
		createConfigSet("Effective Type Set", wsID, createScreenID, 0, 0)

		config := getConfig(wsID, "?item_type_id=999999")
		if config.Screens.Create != createScreenID || config.Screens.Edit != createScreenID || config.Screens.View != createScreenID {
			t.Fatalf("screens = %+v, want all contexts on %d", config.Screens, createScreenID)
		}
	})

	// A caller who cannot see the workspace gets the generic 404, not the
	// configuration. Personal workspaces are owner-only, so an outsider's
	// personal workspace is the clean denial fixture.
	t.Run("denies callers without workspace visibility", func(t *testing.T) {
		_, _, outsiderPass := CreateTestUserWithCredentials(t, server, "effcfgeditor", "effcfgeditor@test.local")
		outsiderSession, _ := CreateAuthCredentialsForUser(t, server, "effcfgeditor", outsiderPass)
		createResp := MakeAuthRequestWithToken(t, server, outsiderSession, http.MethodGet, "/workspaces/personal", nil)
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created struct {
			ID int `json:"id"`
		}
		DecodeJSON(t, createResp, &created)
		if created.ID == 0 {
			t.Fatalf("personal workspace created without id: %#v", created)
		}

		// A regular user who is not the owner must not resolve the
		// configuration of someone else's personal workspace.
		_, _, viewerPass := CreateTestUserWithCredentials(t, server, "effcfgviewer", "effcfgviewer@test.local")
		viewerSession, _ := CreateAuthCredentialsForUser(t, server, "effcfgviewer", viewerPass)
		resp := MakeAuthRequestWithToken(t, server, viewerSession, http.MethodGet,
			fmt.Sprintf("/v2/workspaces/%d/effective-config", created.ID), nil)
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusNotFound)
	})
}
