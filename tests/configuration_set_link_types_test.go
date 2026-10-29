package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise the link_types section of the configuration-set template
// (WI-1333): export carries the active non-system registry, import creates
// missing types / reuses identical ones, name collisions with differing
// definitions produce a structured 409 report, and import is atomic.
// Everything runs through the public API v2.

type linkTypeRow struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	ForwardLabel       string   `json:"forward_label"`
	ReverseLabel       string   `json:"reverse_label"`
	Color              string   `json:"color"`
	AllowedEntityTypes []string `json:"allowed_entity_types"`
	IsSystem           bool     `json:"is_system"`
	Active             bool     `json:"active"`
}

type apiWarningRow struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Context string `json:"context"`
}

type templateLinkType struct {
	Name               string   `json:"name"`
	Description        string   `json:"description,omitempty"`
	ForwardLabel       string   `json:"forward_label"`
	ReverseLabel       string   `json:"reverse_label"`
	Color              string   `json:"color,omitempty"`
	AllowedEntityTypes []string `json:"allowed_entity_types,omitempty"`
}

type templateStatus struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	CategoryName string `json:"category_name"`
}

type templateTransition struct {
	FromStatusName  *string `json:"from_status_name"`
	ToStatusName    string  `json:"to_status_name"`
	FromAllStatuses bool    `json:"from_all_statuses"`
}

type templateWorkflow struct {
	Name        string               `json:"name"`
	Transitions []templateTransition `json:"transitions"`
}

type templateCustomField struct {
	Name      string `json:"name"`
	FieldType string `json:"field_type"`
}

type templateScreenField struct {
	FieldKind       string `json:"field_kind"`
	FieldIdentifier string `json:"field_identifier,omitempty"`
	CustomFieldName string `json:"custom_field_name,omitempty"`
}

type templateScreen struct {
	Name         string                `json:"name"`
	Fields       []templateScreenField `json:"fields"`
	SystemFields []string              `json:"system_fields"`
}

type templatePriority struct {
	Name string `json:"name"`
}

type templateItemType struct {
	Name           string `json:"name"`
	HierarchyLevel int    `json:"hierarchy_level"`
}

type templateLinks struct {
	WorkflowName     string   `json:"workflow_name"`
	CreateScreenName string   `json:"create_screen_name"`
	EditScreenName   string   `json:"edit_screen_name"`
	ViewScreenName   string   `json:"view_screen_name"`
	PriorityNames    []string `json:"priority_names"`
}

type templatePayload struct {
	ConfigurationSet struct {
		Name                string `json:"name"`
		DefaultItemTypeName string `json:"default_item_type_name"`
	} `json:"configuration_set"`
	Statuses     []templateStatus      `json:"statuses"`
	Workflows    []templateWorkflow    `json:"workflows"`
	CustomFields []templateCustomField `json:"custom_fields"`
	Screens      []templateScreen      `json:"screens"`
	Priorities   []templatePriority    `json:"priorities"`
	ItemTypes    []templateItemType    `json:"item_types"`
	LinkTypes    []templateLinkType    `json:"link_types"`
	Links        templateLinks         `json:"configuration_set_links"`
}

type templateDoc struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Payload       templatePayload `json:"payload"`
}

func createV2LinkType(t *testing.T, server *TestServer, name, description, forward, reverse, color string, allowed []string) {
	t.Helper()
	body := map[string]interface{}{
		"name":          name,
		"description":   description,
		"forward_label": forward,
		"reverse_label": reverse,
	}
	if color != "" {
		body["color"] = color
	}
	if allowed != nil {
		body["allowed_entity_types"] = allowed
	}
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/link-types", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
}

func fetchV2LinkTypes(t *testing.T, server *TestServer) []linkTypeRow {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/link-types?include_inactive=true", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data []linkTypeRow `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data
}

func findLinkTypeByName(rows []linkTypeRow, name string) *linkTypeRow {
	for i := range rows {
		if strings.EqualFold(rows[i].Name, name) {
			return &rows[i]
		}
	}
	return nil
}

func createV2ConfigSetNamed(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	return createV2ConfigSet(t, server, map[string]interface{}{"name": name})
}

func createV2ConfigSet(t *testing.T, server *TestServer, body map[string]interface{}) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ConfigurationSet struct {
				ID int `json:"id"`
			} `json:"configuration_set"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	if decoded.Data.ConfigurationSet.ID == 0 {
		t.Fatalf("create configuration set: missing id in response")
	}
	return decoded.Data.ConfigurationSet.ID
}

// createV2Status returns the new status ID.
func createV2Status(t *testing.T, server *TestServer, name string, categoryID int) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/statuses", map[string]interface{}{
		"name": name, "category_id": categoryID,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.ID
}

// findV2StatusCategory returns the ID of a system-seeded status category.
func findV2StatusCategory(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/status-categories", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	for _, cat := range decoded.Data {
		if cat.Name == name {
			return cat.ID
		}
	}
	t.Fatalf("status category %q not found", name)
	return 0
}

// createV2WorkflowWithTransitions creates a workflow and replaces its
// transitions; returns the workflow ID.
func createV2WorkflowWithTransitions(t *testing.T, server *TestServer, name string, transitions []map[string]interface{}) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/workflows", map[string]interface{}{
		"name": name,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	wfID := decoded.Data.ID

	putResp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/workflows/%d/transitions", wfID), map[string]interface{}{
		"transitions": transitions,
	})
	defer putResp.Body.Close()
	AssertStatusCode(t, putResp, http.StatusOK)
	return wfID
}

// createV2CustomField returns the new custom field ID.
func createV2CustomField(t *testing.T, server *TestServer, name, fieldType string) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
		"name": name, "field_type": fieldType,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			CustomField struct {
				ID int `json:"id"`
			} `json:"custom_field"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	if decoded.Data.CustomField.ID == 0 {
		t.Fatalf("create custom field %q: missing id in response", name)
	}
	return decoded.Data.CustomField.ID
}

// createV2ScreenWithFields creates a screen, replaces its fields (one
// default, one custom referencing customFieldID), sets a system field, and
// returns the screen ID.
func createV2ScreenWithFields(t *testing.T, server *TestServer, name string, customFieldID int) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/screens", map[string]interface{}{
		"name": name, "description": "Round-trip screen",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	screenID := decoded.Data.ID

	fieldsResp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/screens/%d/fields", screenID), map[string]interface{}{
		"fields": []map[string]interface{}{
			{"field_type": "default", "field_identifier": "title", "display_order": 0},
			{"field_type": "custom", "field_identifier": strconv.Itoa(customFieldID), "display_order": 1, "is_required": true, "field_width": "full"},
		},
	})
	defer fieldsResp.Body.Close()
	AssertStatusCode(t, fieldsResp, http.StatusOK)

	sysResp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/screens/%d/system-fields", screenID), map[string]interface{}{
		"system_fields": []string{"priority_id"},
	})
	defer sysResp.Body.Close()
	AssertStatusCode(t, sysResp, http.StatusOK)
	return screenID
}

func createV2Priority(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/priorities", map[string]interface{}{
		"name": name, "color": "#dc2626",
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.ID
}

func createV2ItemType(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/item-types", map[string]interface{}{
		"name": name, "hierarchy_level": 1,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)
	var decoded struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.ID
}

func getV2ConfigurationSet(t *testing.T, server *TestServer, id int) map[string]interface{} {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/configuration-sets/%d", id), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data map[string]interface{} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data
}

func getV2WorkflowTransitionPairs(t *testing.T, server *TestServer, workflowID int) map[string]bool {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/workflows/%d/transitions", workflowID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data []struct {
			From *struct {
				Name string `json:"name"`
			} `json:"from"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	pairs := map[string]bool{}
	for _, tr := range decoded.Data {
		from := "<initial>"
		if tr.From != nil {
			from = tr.From.Name
		}
		pairs[from+"→"+tr.To.Name] = true
	}
	return pairs
}

func getV2ScreenFields(t *testing.T, server *TestServer, screenID int) []struct {
	FieldType       string `json:"field_type"`
	FieldIdentifier string `json:"field_identifier"`
} {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/screens/%d/fields", screenID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data []struct {
			FieldType       string `json:"field_type"`
			FieldIdentifier string `json:"field_identifier"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data
}

func findV2CustomFieldID(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/custom-fields", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	for _, cf := range decoded.Data {
		if cf.Name == name {
			return cf.ID
		}
	}
	t.Fatalf("custom field %q not found", name)
	return 0
}

func exportV2Template(t *testing.T, server *TestServer, configSetID int) []byte {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/configuration-sets/%d/export", configSetID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	raw := make([]byte, 0)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	return raw
}

func importV2Template(t *testing.T, server *TestServer, raw []byte) *http.Response {
	t.Helper()
	// json.RawMessage travels verbatim through the helper's json.Marshal;
	// a plain []byte would be base64-encoded into a JSON string.
	return MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets/import", json.RawMessage(raw))
}

func decodeImportResponse(t *testing.T, resp *http.Response) (configSetID int, warnings []apiWarningRow) {
	t.Helper()
	var decoded struct {
		Data struct {
			ConfigurationSet struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"configuration_set"`
			Warnings []apiWarningRow `json:"warnings"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.ConfigurationSet.ID, decoded.Data.Warnings
}

// assertSameInventory compares two link-type inventories by name and
// semantics, ignoring instance-local state (IDs, timestamps, active flags).
func assertSameInventory(t *testing.T, got, want []linkTypeRow) {
	t.Helper()
	key := func(r linkTypeRow) string { return strings.ToLower(r.Name) }
	sort.Slice(got, func(i, j int) bool { return key(got[i]) < key(got[j]) })
	sort.Slice(want, func(i, j int) bool { return key(want[i]) < key(want[j]) })
	if len(got) != len(want) {
		t.Fatalf("inventory size mismatch: got %d types, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if key(g) != key(w) {
			t.Fatalf("inventory name mismatch at %d: got %q, want %q", i, g.Name, w.Name)
		}
		if g.Description != w.Description || g.ForwardLabel != w.ForwardLabel ||
			g.ReverseLabel != w.ReverseLabel || g.Color != w.Color {
			t.Fatalf("definition mismatch for %q:\n got  %+v\n want %+v", w.Name, g, w)
		}
		ga, wa := strings.Join(g.AllowedEntityTypes, ","), strings.Join(w.AllowedEntityTypes, ",")
		if ga != wa {
			t.Fatalf("allowed_entity_types mismatch for %q: got %q, want %q", w.Name, ga, wa)
		}
	}
}

// TestConfigurationSetTemplateRoundTrip verifies that a fully populated
// configuration set — statuses, workflow with transitions, custom field,
// screen with a custom field, priority, item type, and link types — exports
// with every section intact and imports on a fresh instance into a set whose
// resolved state matches the source semantics (WI-1333 AC1). The custom
// field inside screen fields must be rewritten to the target instance's ID.
func TestConfigurationSetTemplateRoundTrip(t *testing.T) {
	source, cleanupSource := StartTestServer(t, GetDBType())
	defer cleanupSource()
	CreateBearerToken(t, source)

	// Shared entity seeds.
	todoID := findV2StatusCategory(t, source, "To Do")
	doneID := findV2StatusCategory(t, source, "Done")
	openID := createV2Status(t, source, "RT Open", todoID)
	doneStatusID := createV2Status(t, source, "RT Done", doneID)
	workflowID := createV2WorkflowWithTransitions(t, source, "RT Workflow", []map[string]interface{}{
		{"from_status_id": nil, "to_status_id": openID, "display_order": 0},
		{"from_status_id": openID, "to_status_id": doneStatusID, "display_order": 1},
	})
	riskFieldID := createV2CustomField(t, source, "Risk Score", "number")
	screenID := createV2ScreenWithFields(t, source, "RT Screen", riskFieldID)
	priorityID := createV2Priority(t, source, "RT Critical")
	itemTypeID := createV2ItemType(t, source, "RT Hazard")

	createV2LinkType(t, source, "mitigates-risk", "Risk mitigated by a control", "mitigated by", "mitigates", "#0ea5e9", []string{"item"})
	// Empty color exercises the registry-default normalization (#6b7280).
	createV2LinkType(t, source, "verifies-requirement", "", "verifies", "verified by", "", []string{"item", "test_case"})
	// Nil allowed_entity_types means "all entity types allowed".
	createV2LinkType(t, source, "relates-incident", "Incident relation", "relates to", "related by", "#f97316", nil)

	wantInventory := fetchV2LinkTypes(t, source)

	setID := createV2ConfigSet(t, source, map[string]interface{}{
		"name":                 "RT Full Set",
		"workflow_id":          workflowID,
		"default_item_type_id": itemTypeID,
		"create_screen_id":     screenID,
		"edit_screen_id":       screenID,
		"view_screen_id":       screenID,
		"priority_ids":         []int{priorityID},
		"item_type_configs": []map[string]interface{}{
			{"item_type_id": itemTypeID},
		},
	})
	raw := exportV2Template(t, source, setID)

	var tpl templateDoc
	if err := json.Unmarshal(raw, &tpl); err != nil {
		t.Fatalf("decode exported template: %v", err)
	}
	payload := tpl.Payload

	// Every section the set references must be in the bundle.
	if payload.ConfigurationSet.Name != "RT Full Set" || payload.ConfigurationSet.DefaultItemTypeName != "RT Hazard" {
		t.Fatalf("exported configuration_set = %+v", payload.ConfigurationSet)
	}
	wantStatuses := map[string]string{"RT Open": "To Do", "RT Done": "Done"}
	if len(payload.Statuses) != len(wantStatuses) {
		t.Fatalf("exported statuses = %+v", payload.Statuses)
	}
	for _, st := range payload.Statuses {
		if wantStatuses[st.Name] != st.CategoryName {
			t.Fatalf("exported status %+v, want category %q", st, wantStatuses[st.Name])
		}
	}
	if len(payload.Workflows) != 1 || payload.Workflows[0].Name != "RT Workflow" {
		t.Fatalf("exported workflows = %+v", payload.Workflows)
	}
	wantTransitions := map[string]bool{"\u003cinitial\u003e→RT Open": true, "RT Open→RT Done": true}
	gotTransitions := map[string]bool{}
	for _, tr := range payload.Workflows[0].Transitions {
		from := displayFromName(tr.FromStatusName)
		gotTransitions[from+"→"+tr.ToStatusName] = true
	}
	for pair := range wantTransitions {
		if !gotTransitions[pair] {
			t.Fatalf("exported transitions %v missing %q", gotTransitions, pair)
		}
	}
	if len(payload.CustomFields) != 1 || payload.CustomFields[0].Name != "Risk Score" || payload.CustomFields[0].FieldType != "number" {
		t.Fatalf("exported custom_fields = %+v", payload.CustomFields)
	}
	if len(payload.Screens) != 1 || payload.Screens[0].Name != "RT Screen" {
		t.Fatalf("exported screens = %+v", payload.Screens)
	}
	screen := payload.Screens[0]
	gotCustomName, gotTitle := "", false
	customCount := 0
	for _, f := range screen.Fields {
		if f.FieldKind == "custom" {
			customCount++
			gotCustomName = f.CustomFieldName
		}
		if f.FieldIdentifier == "title" {
			gotTitle = true
		}
	}
	if customCount != 1 || gotCustomName != "Risk Score" {
		t.Fatalf("exported screen fields = %+v, want exactly one custom field name-rewritten to Risk Score", screen.Fields)
	}
	if !gotTitle {
		t.Fatalf("exported screen fields = %+v, want a title field", screen.Fields)
	}
	if len(screen.SystemFields) != 1 || screen.SystemFields[0] != "priority_id" {
		t.Fatalf("exported screen system_fields = %+v", screen.SystemFields)
	}
	if len(payload.Priorities) != 1 || payload.Priorities[0].Name != "RT Critical" {
		t.Fatalf("exported priorities = %+v", payload.Priorities)
	}
	if len(payload.ItemTypes) != 1 || payload.ItemTypes[0].Name != "RT Hazard" {
		t.Fatalf("exported item_types = %+v", payload.ItemTypes)
	}
	if len(payload.LinkTypes) != 3 {
		t.Fatalf("exported link_types = %+v, want exactly the 3 custom types", payload.LinkTypes)
	}
	if payload.Links.WorkflowName != "RT Workflow" || payload.Links.CreateScreenName != "RT Screen" ||
		payload.Links.EditScreenName != "RT Screen" || payload.Links.ViewScreenName != "RT Screen" ||
		strings.Join(payload.Links.PriorityNames, ",") != "RT Critical" {
		t.Fatalf("exported configuration_set_links = %+v", payload.Links)
	}

	// Import the same bundle on a fresh instance.
	target, cleanupTarget := StartTestServer(t, GetDBType())
	defer cleanupTarget()
	CreateBearerToken(t, target)

	importResp := importV2Template(t, target, raw)
	defer importResp.Body.Close()
	AssertStatusCode(t, importResp, http.StatusCreated)
	id, warnings := decodeImportResponse(t, importResp)
	if id == 0 {
		t.Fatalf("import returned no configuration set id")
	}
	if len(warnings) != 0 {
		t.Fatalf("fresh-instance import produced warnings = %v, want none", warnings)
	}

	// The imported set resolves to same-named relations.
	imported := getV2ConfigurationSet(t, target, id)
	for key, want := range map[string]interface{}{
		"workflow_name":          "RT Workflow",
		"create_screen_name":     "RT Screen",
		"edit_screen_name":       "RT Screen",
		"view_screen_name":       "RT Screen",
		"default_item_type_name": "RT Hazard",
	} {
		if got := imported[key]; got != want {
			t.Fatalf("imported set %s = %v, want %v", key, got, want)
		}
	}
	priorities, _ := imported["priorities_detailed"].([]interface{})
	if len(priorities) != 1 {
		t.Fatalf("imported set priorities_detailed = %v", imported["priorities_detailed"])
	}
	priority, _ := priorities[0].(map[string]interface{})
	if priority["name"] != "RT Critical" {
		t.Fatalf("imported set priority name = %v, want RT Critical", priority["name"])
	}

	// The workflow on the target connects the same statuses by name.
	importedWorkflowID := int(imported["workflow_id"].(float64))
	gotPairs := getV2WorkflowTransitionPairs(t, target, importedWorkflowID)
	for pair := range wantTransitions {
		if !gotPairs[pair] {
			t.Fatalf("imported workflow transitions %v missing %q", gotPairs, pair)
		}
	}

	// The imported screen's custom field must point at the target's own
	// custom field row, not a stale identifier.
	importedScreenID := int(imported["create_screen_id"].(float64))
	fields := getV2ScreenFields(t, target, importedScreenID)
	targetRiskID := findV2CustomFieldID(t, target, "Risk Score")
	gotCustom, gotTitle := false, false
	targetCustomCount := 0
	for _, f := range fields {
		if f.FieldType == "custom" {
			targetCustomCount++
			gotCustom = true
			if f.FieldIdentifier != strconv.Itoa(targetRiskID) {
				t.Fatalf("imported custom screen field identifier = %q, want target id %d", f.FieldIdentifier, targetRiskID)
			}
		}
		if f.FieldIdentifier == "title" {
			gotTitle = true
		}
	}
	if targetCustomCount != 1 || !gotCustom || !gotTitle {
		t.Fatalf("imported screen fields = %+v, want exactly one custom field and a title", fields)
	}

	gotInventory := fetchV2LinkTypes(t, target)
	assertSameInventory(t, gotInventory, wantInventory)
}

func displayFromName(from *string) string {
	if from == nil {
		return "\u003cinitial\u003e"
	}
	return *from
}

// TestConfigurationSetLinkTypeImportConflict verifies that importing a link
// type whose name collides with a differently defined existing type fails
// with a structured 409 report naming both definitions, and that the
// registry stays untouched (WI-1333 AC2).
func TestConfigurationSetLinkTypeImportConflict(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	createV2LinkType(t, server, "conforms-to", "Traceability to a requirement", "conforms to", "is conformed to", "#123abc", []string{"item"})

	setID := createV2ConfigSetNamed(t, server, "Conflict Source Set")
	raw := exportV2Template(t, server, setID)

	// Tamper with the definition, keeping the colliding name.
	var tpl map[string]interface{}
	if err := json.Unmarshal(raw, &tpl); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	payload := tpl["payload"].(map[string]interface{})
	linkTypes := payload["link_types"].([]interface{})
	first := linkTypes[0].(map[string]interface{})
	if first["name"].(string) != "conforms-to" {
		t.Fatalf("expected the seeded type in the bundle, got %v", first["name"])
	}
	first["reverse_label"] = "conformed by"
	tampered, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("encode tampered template: %v", err)
	}

	resp := importV2Template(t, server, tampered)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusConflict)

	var conflictErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details []struct {
				Name     string           `json:"name"`
				Template templateLinkType `json:"template"`
				Existing templateLinkType `json:"existing"`
			} `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &conflictErr)
	if conflictErr.Error.Code != "link_type_definition_conflict" {
		t.Fatalf("error code = %q, want link_type_definition_conflict", conflictErr.Error.Code)
	}
	if n := len(conflictErr.Error.Details); n != 1 {
		t.Fatalf("conflict report has %d entries, want 1", n)
	}
	conflict := conflictErr.Error.Details[0]
	if conflict.Name != "conforms-to" {
		t.Fatalf("conflict name = %q, want conforms-to", conflict.Name)
	}
	if conflict.Template.ReverseLabel != "conformed by" {
		t.Fatalf("conflict template reverse_label = %q, want %q", conflict.Template.ReverseLabel, "conformed by")
	}
	if conflict.Existing.ReverseLabel != "is conformed to" {
		t.Fatalf("conflict existing reverse_label = %q, want %q", conflict.Existing.ReverseLabel, "is conformed to")
	}

	// The registry is unchanged and no configuration set landed.
	inventory := fetchV2LinkTypes(t, server)
	row := findLinkTypeByName(inventory, "conforms-to")
	if row == nil {
		t.Fatalf("conforms-to vanished after the refused import")
	}
	if row.ReverseLabel != "is conformed to" {
		t.Fatalf("existing definition was mutated: %+v", row)
	}
	count := 0
	for _, r := range inventory {
		if strings.EqualFold(r.Name, "conforms-to") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("found %d conforms-to rows after the refused import, want exactly 1", count)
	}
}

// TestConfigurationSetLinkTypeImportIdenticalCollision verifies that
// importing a link type whose name collides with an identically defined
// existing type succeeds, reuses the existing row, and does not duplicate it
// (WI-1333 AC2). The name is written with different casing to pin the
// case-insensitive match.
func TestConfigurationSetLinkTypeImportIdenticalCollision(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	createV2LinkType(t, server, "depends-on", "Blocking dependency", "depends on", "blocks", "", nil)

	setID := createV2ConfigSetNamed(t, server, "Reuse Source Set")
	raw := exportV2Template(t, server, setID)

	var tpl map[string]interface{}
	if err := json.Unmarshal(raw, &tpl); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	payload := tpl["payload"].(map[string]interface{})
	linkTypes := payload["link_types"].([]interface{})
	first := linkTypes[0].(map[string]interface{})
	if first["name"].(string) != "depends-on" {
		t.Fatalf("expected the seeded type in the bundle, got %v", first["name"])
	}
	first["name"] = "Depends-On"
	renamed, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("encode template: %v", err)
	}

	resp := importV2Template(t, server, renamed)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	_, warnings := decodeImportResponse(t, resp)
	if len(warnings) == 0 {
		t.Fatalf("identical-collision import returned no warnings, want a reuse note")
	}
	foundReuse := false
	for _, w := range warnings {
		if w.Code == "import_reuse" && strings.Contains(w.Message, "reusing existing definition") {
			foundReuse = true
		}
	}
	if !foundReuse {
		t.Fatalf("warnings %v do not include a reuse note for depends-on", warnings)
	}

	inventory := fetchV2LinkTypes(t, server)
	count := 0
	for _, r := range inventory {
		if strings.EqualFold(r.Name, "depends-on") {
			count++
			if r.Name != "depends-on" {
				t.Fatalf("existing row was renamed by import: %q", r.Name)
			}
		}
	}
	if count != 1 {
		t.Fatalf("found %d depends-on rows after import, want exactly 1", count)
	}
}

// TestConfigurationSetLinkTypeImportAtomicity verifies that a bundle failing
// mid-apply (after the link-type insert) leaves no partially created link
// types or configuration set behind (WI-1333 AC3).
func TestConfigurationSetLinkTypeImportAtomicity(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	tpl := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.configuration-set",
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]interface{}{
			"configuration_set": map[string]interface{}{"name": "Atomicity Probe Set"},
			"link_types": []map[string]interface{}{
				{"name": "atomic-probe", "forward_label": "probes", "reverse_label": "probed by"},
			},
			"statuses": []map[string]interface{}{
				{"name": "Probe Status", "category_name": "To Do"},
			},
			"workflows": []map[string]interface{}{
				{
					"name": "Atomic Probe Workflow",
					"transitions": []map[string]interface{}{
						{"from_status_name": nil, "to_status_name": "Ghost Status"},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("encode template: %v", err)
	}

	// Mid-apply failures surface as the current 500 contract; the important
	// part is that nothing persists.
	resp := importV2Template(t, server, raw)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusInternalServerError)

	inventory := fetchV2LinkTypes(t, server)
	if findLinkTypeByName(inventory, "atomic-probe") != nil {
		t.Fatalf("atomic-probe survived a rolled-back import")
	}

	listResp := MakeBearerRequest(t, server, http.MethodGet, "/rest/api/v2/configuration-sets?search=Atomicity", nil)
	defer listResp.Body.Close()
	AssertStatusCode(t, listResp, http.StatusOK)
	var decoded struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	DecodeJSON(t, listResp, &decoded)
	if decoded.Meta.Total != 0 {
		t.Fatalf("imported configuration set survived the rollback, total = %d", decoded.Meta.Total)
	}
}

// TestConfigurationSetLinkTypeImportSanitization verifies the template
// sanitizer scrubs hostile link-type fields with the live link-type policies
// before import (WI-1333 AC4).
func TestConfigurationSetLinkTypeImportSanitization(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	tpl := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.configuration-set",
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]interface{}{
			"configuration_set": map[string]interface{}{"name": "Scrubby Set"},
			"link_types": []map[string]interface{}{
				{
					"name":                 "  <b>Scrubby</b>  ",
					"description":          "<script>alert('x')</script>Traceability notes",
					"forward_label":        "<i>scrubs</i>",
					"reverse_label":        "scrubbed by <script>1</script>",
					"color":                "#4b5563 <img src=x onerror=alert(1)>",
					"allowed_entity_types": []string{"<script>item</script>", "test_case"},
				},
			},
		},
	}
	raw, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("encode template: %v", err)
	}

	resp := importV2Template(t, server, raw)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusCreated)

	row := findLinkTypeByName(fetchV2LinkTypes(t, server), "Scrubby")
	if row == nil {
		t.Fatalf("imported link type missing after sanitized import")
	}
	if row.Description != "Traceability notes" {
		t.Fatalf("description = %q, want %q", row.Description, "Traceability notes")
	}
	if row.ForwardLabel != "scrubs" {
		t.Fatalf("forward_label = %q, want %q", row.ForwardLabel, "scrubs")
	}
	if row.ReverseLabel != "scrubbed by" {
		t.Fatalf("reverse_label = %q, want %q", row.ReverseLabel, "scrubbed by")
	}
	if row.Color != "#4b5563" {
		t.Fatalf("color = %q, want %q", row.Color, "#4b5563")
	}
	if strings.Join(row.AllowedEntityTypes, ",") != "test_case" {
		t.Fatalf("allowed_entity_types = %v, want [test_case] (markup-only entry dropped)", row.AllowedEntityTypes)
	}
}

// TestConfigurationSetLinkTypeImportRequiresLabels verifies that a template
// link type missing its directional labels is refused with the live
// validateLinkType contract instead of landing an invalid row.
func TestConfigurationSetLinkTypeImportRequiresLabels(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	tpl := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.configuration-set",
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]interface{}{
			"configuration_set": map[string]interface{}{"name": "Labelless Set"},
			"link_types": []map[string]interface{}{
				{"name": "labelless", "forward_label": "points at"},
			},
		},
	}
	raw, err := json.Marshal(tpl)
	if err != nil {
		t.Fatalf("encode template: %v", err)
	}

	resp := importV2Template(t, server, raw)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusBadRequest)

	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request", errBody.Error.Code)
	}
	if errBody.Error.Message != "link_types[0]: name, forward_label, and reverse_label are required" {
		t.Fatalf("error message = %q", errBody.Error.Message)
	}
	if findLinkTypeByName(fetchV2LinkTypes(t, server), "labelless") != nil {
		t.Fatalf("labelless link type reached the registry")
	}
}

// TestConfigurationSetLinkTypeExportSkipsInactive verifies that deactivated
// custom link types are not carried into bundles: a pack should not
// resurrect a type an admin deliberately retired.
func TestConfigurationSetLinkTypeExportSkipsInactive(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	createV2LinkType(t, server, "retired-link", "Deliberately retired", "retired", "retired by", "", nil)
	inventory := fetchV2LinkTypes(t, server)
	row := findLinkTypeByName(inventory, "retired-link")
	if row == nil {
		t.Fatalf("seeded link type missing")
	}
	patchResp := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/link-types/%d", row.ID), map[string]interface{}{
		"active": false,
	})
	defer patchResp.Body.Close()
	AssertStatusCode(t, patchResp, http.StatusOK)

	setID := createV2ConfigSetNamed(t, server, "Inactive Export Set")
	raw := exportV2Template(t, server, setID)

	var tpl templateDoc
	if err := json.Unmarshal(raw, &tpl); err != nil {
		t.Fatalf("decode exported template: %v", err)
	}
	for _, lt := range tpl.Payload.LinkTypes {
		if strings.EqualFold(lt.Name, "retired-link") {
			t.Fatalf("inactive link type was exported: %+v", lt)
		}
	}
}
