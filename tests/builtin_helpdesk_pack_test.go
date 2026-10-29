package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/services"
)

// Built-in pack content (WI-1141): the embedded helpdesk pack is the same
// document set an uploaded archive carries, so it is applied through the
// production pack apply endpoint. This verifies the shipped schema layer is
// conformant and that applying it attaches its configuration set; it does not
// depend on the built-in apply endpoints, which land separately.

// builtinHelpdeskArchiveBytes renders the embedded helpdesk pack as the
// gzipped tar the apply endpoint accepts.
func builtinHelpdeskArchiveBytes(t *testing.T) []byte {
	t.Helper()
	archive, err := services.BuiltinPackArchive("helpdesk")
	if err != nil {
		t.Fatalf("BuiltinPackArchive(helpdesk): %v", err)
	}
	return tarGz(t, archive.Files)
}

func TestBuiltinHelpdeskPackApply(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	archive := builtinHelpdeskArchiveBytes(t)

	t.Run("apply_helpdesk_succeeds_and_passes_conformance", func(t *testing.T) {
		status, stages, pluginChecks, conformance := applyPack(t, server, "/rest/api/v2/packs/apply", archive,
			map[string]string{"workspace_name": "Helpdesk"})
		if status != "applied" {
			t.Fatalf("apply status = %q (stages: %+v conformance: %+v plugins: %+v)", status, stages, conformance, pluginChecks)
		}
		if conformance == nil || conformance["conformant"] != true {
			t.Fatalf("helpdesk conformance = %+v", conformance)
		}
		if stageByName(t, stages, "schema")["status"] != "ok" {
			t.Fatalf("schema stage = %+v", stageByName(t, stages, "schema"))
		}
		if stageByName(t, stages, "content")["status"] != "ok" {
			t.Fatalf("content stage = %+v", stageByName(t, stages, "content"))
		}
	})

	t.Run("workspace_gets_config_set_nav_preset_and_slas", func(t *testing.T) {
		wsID := findWorkspaceByName(t, server, "Helpdesk")
		if wsID == 0 {
			t.Fatal("helpdesk workspace not found")
		}
		bundle, _ := exportWorkspaceBundle(t, server, wsID)
		if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Helpdesk" {
			t.Fatalf("configuration set not attached: %+v", bundle.ConfigurationSet)
		}

		configResp := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/board-configuration", wsID), nil)
		defer configResp.Body.Close()
		AssertStatusCode(t, configResp, http.StatusOK)
		var configDoc struct {
			Data struct {
				ViewSettings struct {
					EnabledViews *[]string `json:"enabled_views"`
				} `json:"view_settings"`
			} `json:"data"`
		}
		DecodeJSON(t, configResp, &configDoc)
		if configDoc.Data.ViewSettings.EnabledViews == nil {
			t.Fatal("helpdesk workspace has no view settings override")
		}
		enabled := map[string]bool{}
		for _, id := range *configDoc.Data.ViewSettings.EnabledViews {
			enabled[id] = true
		}
		if !enabled["queue"] {
			t.Fatalf("queue not enabled: %v", *configDoc.Data.ViewSettings.EnabledViews)
		}
		for _, disabled := range []string{"backlog", "tree", "map", "roadmap", "iterations", "milestones"} {
			if enabled[disabled] {
				t.Fatalf("view %q should be disabled: %v", disabled, *configDoc.Data.ViewSettings.EnabledViews)
			}
		}

		metricsResp := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", wsID), nil)
		defer metricsResp.Body.Close()
		AssertStatusCode(t, metricsResp, http.StatusOK)
		var metricsDoc struct {
			Data []struct {
				Name       string `json:"name"`
				Conditions []struct {
					Phase         string `json:"phase"`
					ConditionType string `json:"condition_type"`
				} `json:"conditions"`
				Goals []struct {
					Targets []any `json:"targets"`
				} `json:"goals"`
			} `json:"data"`
		}
		DecodeJSON(t, metricsResp, &metricsDoc)
		names := map[string]bool{}
		firstResponseStop := ""
		for _, metric := range metricsDoc.Data {
			names[metric.Name] = true
			if len(metric.Conditions) == 0 || len(metric.Goals) == 0 || len(metric.Goals[0].Targets) == 0 {
				t.Fatalf("metric %q is missing conditions/goals/targets: %+v", metric.Name, metric)
			}
			if metric.Name == "First Response" {
				for _, condition := range metric.Conditions {
					if condition.Phase == "stop" {
						firstResponseStop = condition.ConditionType
					}
				}
			}
		}
		if !names["First Response"] || !names["Time to Resolve"] {
			t.Fatalf("SLA metrics = %v, want First Response and Time to Resolve", names)
		}
		// First response is the first public agent reply, not an assignment: a
		// ticket that is answered without ever being assigned must still be
		// satisfied.
		if firstResponseStop != "comment_by_agent" {
			t.Fatalf("First Response stop condition = %q, want comment_by_agent", firstResponseStop)
		}

		calendarsResp := MakeBearerRequest(t, server, http.MethodGet,
			fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/calendars", wsID), nil)
		defer calendarsResp.Body.Close()
		AssertStatusCode(t, calendarsResp, http.StatusOK)
		var calendarsDoc struct {
			Data []struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		DecodeJSON(t, calendarsResp, &calendarsDoc)
		foundCalendar := false
		for _, calendar := range calendarsDoc.Data {
			if calendar.Name == "Business Hours" {
				foundCalendar = true
			}
		}
		if !foundCalendar {
			t.Fatalf("Business Hours calendar missing: %+v", calendarsDoc.Data)
		}
	})
}

// TestBuiltinHelpdeskPackAdoptsExistingStatuses: statuses are global and
// unique by name, so the pack adopts the instance's "Open"/"Closed" as they
// are. Attribute differences (descriptions, categories) are the admin's
// design — they must neither fail the apply nor be overwritten.
func TestBuiltinHelpdeskPackAdoptsExistingStatuses(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	categories := CreateTestStatusCategories(t, server, "Adopt")

	// Rework the seeded "Open" wording; add "Closed" with custom wording too.
	openID := statusIDByName(t, server, "Open")
	if openID == 0 {
		t.Fatal("seeded Open status not found")
	}
	patchResp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/statuses/%d", openID),
		map[string]interface{}{"description": "instance wording for open work"})
	defer patchResp.Body.Close()
	AssertStatusCode(t, patchResp, http.StatusOK)

	createClosed := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/statuses",
		map[string]interface{}{"name": "Closed", "category_id": categories[0], "description": "instance wording for closed work"})
	defer createClosed.Body.Close()
	AssertStatusCode(t, createClosed, http.StatusCreated)

	status, stages, _, conformance := applyPack(t, server, "/rest/api/v2/packs/apply", builtinHelpdeskArchiveBytes(t),
		map[string]string{"workspace_name": "Adopt Statuses"})
	if status != "applied" {
		t.Fatalf("apply status = %q (stages: %+v conformance: %+v)", status, stages, conformance)
	}
	if stageByName(t, stages, "schema")["status"] != "ok" {
		t.Fatalf("schema stage = %+v", stageByName(t, stages, "schema"))
	}
	if stageByName(t, stages, "conformance")["status"] != "ok" {
		t.Fatalf("conformance stage = %+v", stageByName(t, stages, "conformance"))
	}

	// Adoption: the instance's wording survives the apply untouched.
	descriptions := statusDescriptionsByName(t, server)
	if got := descriptions["Open"]; got != "instance wording for open work" {
		t.Fatalf("Open description after apply = %q, want the instance's wording", got)
	}
	if got := descriptions["Closed"]; got != "instance wording for closed work" {
		t.Fatalf("Closed description after apply = %q, want the instance's wording", got)
	}
}

// TestBuiltinHelpdeskPackAdoptsModifiedConfigurationSet: an existing
// "Helpdesk" configuration set is the admin's design. A new apply must attach
// that set instead of importing a duplicate, and must keep the modifications.
func TestBuiltinHelpdeskPackAdoptsModifiedConfigurationSet(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	if status, stages, _, conformance := applyPack(t, server, "/rest/api/v2/packs/apply", builtinHelpdeskArchiveBytes(t),
		map[string]string{"workspace_name": "Adopt Set A"}); status != "applied" {
		t.Fatalf("initial apply status = %q (stages: %+v conformance: %+v)", status, stages, conformance)
	}

	setID := configurationSetIDByName(t, server, "Helpdesk")
	if setID == 0 {
		t.Fatal("Helpdesk configuration set not found after first apply")
	}
	modifyResp := MakeAuthRequest(t, server, http.MethodPut, fmt.Sprintf("/configuration-sets/%d", setID),
		map[string]interface{}{"description": "admin tailored helpdesk"})
	defer modifyResp.Body.Close()
	AssertStatusCode(t, modifyResp, http.StatusOK)

	// A second workspace provisioned from the pack must adopt the same set.
	createResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/workspaces", map[string]any{
		"name": "Adopt Set B", "key": "ADOPTB", "template_pack": "helpdesk",
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)

	summaries := listConfigurationSets(t, server)
	helpdeskCount := 0
	adopted := configurationSetSummary{}
	for _, cs := range summaries {
		if strings.EqualFold(cs.Name, "Helpdesk") {
			helpdeskCount++
			adopted = cs
		}
	}
	if helpdeskCount != 1 {
		t.Fatalf("Helpdesk configuration sets after re-provision = %d, want 1 (sets: %+v)", helpdeskCount, summaries)
	}
	if adopted.Description != "admin tailored helpdesk" {
		t.Fatalf("adopted set description = %q, want the admin's modification", adopted.Description)
	}

	wsID := findWorkspaceByName(t, server, "Adopt Set B")
	if wsID == 0 {
		t.Fatal("second helpdesk workspace not found")
	}
	bundle, _ := exportWorkspaceBundle(t, server, wsID)
	if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Helpdesk" {
		t.Fatalf("configuration set not attached to the second workspace: %+v", bundle.ConfigurationSet)
	}
}

// TestBuiltinHelpdeskPackFailsWhenRequiredStatusesUnavailable: required
// statuses are the only schema-layer gate. When the adopted set's design no
// longer provides them, the apply fails with the concrete reason and a
// template-pack workspace creation rolls back instead of leaving a workspace
// without a usable lifecycle.
func TestBuiltinHelpdeskPackFailsWhenRequiredStatusesUnavailable(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	if status, stages, _, conformance := applyPack(t, server, "/rest/api/v2/packs/apply", builtinHelpdeskArchiveBytes(t),
		map[string]string{"workspace_name": "Strict A"}); status != "applied" {
		t.Fatalf("initial apply status = %q (stages: %+v conformance: %+v)", status, stages, conformance)
	}

	// The admin reworks the lifecycle and no instance status named "Closed"
	// remains; the adopted set's transitions point at the replacement.
	closedID := statusIDByName(t, server, "Closed")
	if closedID == 0 {
		t.Fatal("Closed status not found after first apply")
	}
	renameResp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/statuses/%d", closedID),
		map[string]interface{}{"name": "Archived"})
	defer renameResp.Body.Close()
	AssertStatusCode(t, renameResp, http.StatusOK)

	// Direct pack apply reports the failing stage and the missing status.
	status, stages, _, conformance := applyPack(t, server, "/rest/api/v2/packs/apply", builtinHelpdeskArchiveBytes(t),
		map[string]string{"workspace_name": "Strict B"})
	if status != "failed" {
		t.Fatalf("apply status = %q (stages: %+v conformance: %+v), want failed", status, stages, conformance)
	}
	conformanceStage := stageByName(t, stages, "conformance")
	if conformanceStage["status"] != "failed" {
		t.Fatalf("conformance stage = %+v, want failed", conformanceStage)
	}
	if detail, _ := conformanceStage["detail"].(string); !strings.Contains(detail, "required statuses") || !strings.Contains(detail, "Closed") {
		t.Fatalf("conformance stage detail = %q, want the required-status reason naming Closed", detail)
	}

	// Workspace creation from the template pack surfaces the same reason and
	// rolls the workspace back.
	createResp := MakeAuthRequest(t, server, http.MethodPost, "/v2/workspaces", map[string]any{
		"name": "Strict C", "key": "STRICTC", "template_pack": "helpdesk",
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusUnprocessableEntity)
	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	DecodeJSON(t, createResp, &errBody)
	if errBody.Error.Code != "pack_provisioning_failed" {
		t.Fatalf("error code = %q", errBody.Error.Code)
	}
	if !strings.Contains(errBody.Error.Message, `stage "conformance" failed`) || !strings.Contains(errBody.Error.Message, "Closed") {
		t.Fatalf("provisioning error message = %q, want the failing stage and the missing status", errBody.Error.Message)
	}
	if findWorkspaceByName(t, server, "Strict C") != 0 {
		t.Fatal("failed provisioning left a workspace behind")
	}
}

// statusIDByName returns the ID of the instance-wide status with the given
// name, or 0 when absent.
func statusIDByName(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	for _, st := range listStatuses(t, server) {
		statusName, _ := st["name"].(string)
		if strings.EqualFold(statusName, name) {
			id, _ := st["id"].(float64)
			return int(id)
		}
	}
	return 0
}

// statusDescriptionsByName maps status name to description via the v2
// catalog endpoint.
func statusDescriptionsByName(t *testing.T, server *TestServer) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, st := range listStatuses(t, server) {
		description, _ := st["description"].(string)
		name, _ := st["name"].(string)
		out[name] = description
	}
	return out
}

func listStatuses(t *testing.T, server *TestServer) []map[string]any {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/statuses", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data []map[string]any `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data
}

type configurationSetSummary struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func listConfigurationSets(t *testing.T, server *TestServer) []configurationSetSummary {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, "/configuration-sets?limit=100", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		ConfigurationSets []configurationSetSummary `json:"configuration_sets"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.ConfigurationSets
}

func configurationSetIDByName(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	for _, cs := range listConfigurationSets(t, server) {
		if strings.EqualFold(cs.Name, name) {
			return cs.ID
		}
	}
	return 0
}
