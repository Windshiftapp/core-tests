//go:build test

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestConfigurationSetConformanceCustomFieldOptionShrink proves a repair that
// shrinks a select field's option set commits an item-value scrubbing job
// atomically, and that a field-type drift is left for manual attention
// instead of being rewritten in place (WI-1529).
func TestConfigurationSetConformanceCustomFieldOptionShrink(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	// A select field whose canonical set carries two options.
	options := map[string]any{
		"next_id": 3,
		"items": []map[string]any{
			{"id": 1, "label": "Low"},
			{"id": 2, "label": "High"},
		},
	}
	optionsJSON, _ := json.Marshal(options)
	createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
		"name": "CF Severity", "field_type": "select", "options": string(optionsJSON),
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created struct {
		Data struct {
			CustomField struct {
				ID      int    `json:"id"`
				Options string `json:"options"`
			} `json:"custom_field"`
		} `json:"data"`
	}
	DecodeJSON(t, createResp, &created)
	fieldID := created.Data.CustomField.ID
	if fieldID == 0 {
		t.Fatalf("created select field = %#v", created)
	}

	// Wire the field into the fixture set's screen so the canonical template
	// carries it, then export the canonical.
	fx := buildConformanceFixture(t, server)
	putScreenFields(t, server, fx.screenID, []map[string]interface{}{
		{"field_type": "default", "field_identifier": "title", "display_order": 0},
		{"field_type": "custom", "field_identifier": fmt.Sprintf("%d", fieldID), "display_order": 1},
	})
	canonical := exportV2Template(t, server, fx.setID)

	// Shrink the live option set: drop the "High" option (id 2).
	shrunk := map[string]any{
		"next_id": 3,
		"items": []map[string]any{
			{"id": 1, "label": "Low"},
		},
	}
	shrunkJSON, _ := json.Marshal(shrunk)
	patch := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/custom-fields/%d", fieldID), map[string]interface{}{
		"options": string(shrunkJSON),
	})
	defer patch.Body.Close()
	AssertStatusCode(t, patch, http.StatusOK)

	const optionsDriftID = "custom_fields/differing/cf severity"
	conformant, _, drifts := conformanceCheck(t, server, fx.setID, canonical)
	if conformant || findDriftRow(drifts, optionsDriftID) == nil {
		t.Fatalf("shrunken options not reported as drift: %+v", drifts)
	}

	repaired, postConformant, outcomes, _ := conformanceRepairDebug(t, server, fx.setID, canonical, nil)
	if repaired == 0 || !postConformant {
		t.Fatalf("repair did not restore the option set: repaired=%d conformant=%t", repaired, postConformant)
	}
	for _, outcome := range outcomes {
		if outcome["name"] == "CF Severity" && outcome["status"] != "repaired" {
			t.Fatalf("severity field outcome = %v, want repaired", outcome)
		}
	}

	// The scrubbing job for the removed option committed with the repair.
	var cleanupJobs int
	if err := server.DB().QueryRow(
		`SELECT COUNT(*) FROM pending_custom_field_cleanups WHERE field_id = ? AND job_type = 'option_removal'`,
		fieldID,
	).Scan(&cleanupJobs); err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if cleanupJobs != 1 {
		t.Fatalf("option_removal cleanup jobs = %d, want 1", cleanupJobs)
	}
	var payload string
	if err := server.DB().QueryRow(
		`SELECT payload FROM pending_custom_field_cleanups WHERE field_id = ? AND job_type = 'option_removal'`,
		fieldID,
	).Scan(&payload); err != nil {
		t.Fatalf("read cleanup payload: %v", err)
	}
	var decoded struct {
		RemovedIDs []int `json:"removed_ids"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("decode cleanup payload %q: %v", payload, err)
	}
	if len(decoded.RemovedIDs) != 1 || decoded.RemovedIDs[0] != 2 {
		t.Fatalf("removed ids = %v, want [2]", decoded.RemovedIDs)
	}
}

// TestConfigurationSetConformanceCustomFieldTypeDriftSkipped proves a
// field-type drift is reported for manual attention and the field is left
// untouched: rewriting the type would strand old-shaped values in item data
// (WI-1529). Direct SQL seeds the drift because the API refuses type changes
// by design.
func TestConfigurationSetConformanceCustomFieldTypeDriftSkipped(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	options := map[string]any{
		"next_id": 2,
		"items": []map[string]any{
			{"id": 1, "label": "Low"},
		},
	}
	optionsJSON, _ := json.Marshal(options)
	createResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/custom-fields", map[string]interface{}{
		"name": "CF Type Drift", "field_type": "select", "options": string(optionsJSON),
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var created struct {
		Data struct {
			CustomField struct {
				ID      int    `json:"id"`
				Options string `json:"options"`
			} `json:"custom_field"`
		} `json:"data"`
	}
	DecodeJSON(t, createResp, &created)
	fieldID := created.Data.CustomField.ID

	fx := buildConformanceFixture(t, server)
	putScreenFields(t, server, fx.screenID, []map[string]interface{}{
		{"field_type": "default", "field_identifier": "title", "display_order": 0},
		{"field_type": "custom", "field_identifier": fmt.Sprintf("%d", fieldID), "display_order": 1},
	})
	canonical := exportV2Template(t, server, fx.setID)

	// Fixture exception: the API intentionally cannot change a field type
	// after creation, so the drift under test is seeded directly.
	if _, err := server.DB().Exec(
		`UPDATE custom_field_definitions SET field_type = 'text' WHERE id = ?`, fieldID,
	); err != nil {
		t.Fatalf("seed type drift: %v", err)
	}

	conformant, _, drifts := conformanceCheck(t, server, fx.setID, canonical)
	if conformant {
		t.Fatal("type drift not reported")
	}
	var typeRow map[string]interface{}
	for _, row := range drifts {
		if name, _ := row["name"].(string); name == "CF Type Drift" {
			typeRow = row
		}
	}
	if typeRow == nil {
		t.Fatalf("custom field drift row missing: %+v", drifts)
	}

	_, _, outcomes, _ := conformanceRepairDebug(t, server, fx.setID, canonical, nil)
	var outcome map[string]interface{}
	for _, row := range outcomes {
		if row["name"] == "CF Type Drift" {
			outcome = row
		}
	}
	if outcome == nil {
		t.Fatalf("no outcome for the type-drifted field: %+v", outcomes)
	}
	if outcome["status"] != "repaired" {
		t.Fatalf("type-drift outcome = %v, want repaired (safe fields only)", outcome)
	}
	detail, _ := outcome["detail"].(string)
	if !strings.Contains(detail, "cannot be changed") {
		t.Fatalf("type-drift outcome detail = %q, want a manual-attention note", detail)
	}

	// The live type and options survive the repair untouched.
	var liveType, liveOptions string
	if err := server.DB().QueryRow(
		`SELECT field_type, COALESCE(options, '') FROM custom_field_definitions WHERE id = ?`, fieldID,
	).Scan(&liveType, &liveOptions); err != nil {
		t.Fatalf("reload field: %v", err)
	}
	if liveType != "text" || liveOptions != created.Data.CustomField.Options {
		t.Fatalf("field rewritten by repair: type=%q options=%q", liveType, liveOptions)
	}
}

// putScreenFields replaces a screen's field layout via the v2 API.
func putScreenFields(t *testing.T, server *TestServer, screenID int, fields []map[string]interface{}) {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/screens/%d/fields", screenID), map[string]interface{}{
		"fields": fields,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
}
