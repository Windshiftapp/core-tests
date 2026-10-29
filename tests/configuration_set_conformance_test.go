package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Conformance check + repair (WI-1334): a fully populated configuration set
// is built through the production API, exported as its canonical template,
// drifted one class at a time, and repaired back — asserting the drift rows
// name the concrete difference and that repair restores the exact template
// state without touching item data.

type conformanceFixture struct {
	setID            int
	canonical        []byte
	workflowID       int
	screenID         int
	linkTypeID       int
	approvalSetID    int
	openStatusID     int
	doneStatusID     int
	rejectedStatusID int
}

func conformanceCheck(t *testing.T, server *TestServer, configSetID int, canonical []byte) (conformant bool, driftCount int, drifts []map[string]any) {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/configuration-sets/%d/conformance/check", configSetID),
		json.RawMessage(canonical))
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data struct {
			Conformant bool             `json:"conformant"`
			DriftCount int              `json:"drift_count"`
			Drifts     []map[string]any `json:"drifts"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.Conformant, decoded.Data.DriftCount, decoded.Data.Drifts
}

func conformanceRepair(t *testing.T, server *TestServer, configSetID int, canonical []byte, ids []string) (repaired int, postConformant bool, outcomes []map[string]any) {
	repaired, postConformant, outcomes, _ = conformanceRepairDebug(t, server, configSetID, canonical, ids)
	return repaired, postConformant, outcomes
}

func conformanceRepairDebug(t *testing.T, server *TestServer, configSetID int, canonical []byte, ids []string) (repaired int, postConformant bool, outcomes, postDrifts []map[string]any) {
	t.Helper()
	body := map[string]interface{}{
		"template": json.RawMessage(canonical),
	}
	if ids != nil {
		body["ids"] = ids
	}
	resp := MakeBearerRequest(t, server, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/configuration-sets/%d/conformance/repair", configSetID), body)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var decoded struct {
		Data struct {
			RepairedCount int              `json:"repaired_count"`
			Outcomes      []map[string]any `json:"outcomes"`
			PostCheck     struct {
				Conformant bool             `json:"conformant"`
				Drifts     []map[string]any `json:"drifts"`
			} `json:"post_check"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.RepairedCount, decoded.Data.PostCheck.Conformant, decoded.Data.Outcomes, decoded.Data.PostCheck.Drifts
}

func findDriftRow(drifts []map[string]any, id string) map[string]any {
	for _, d := range drifts {
		if d["id"] == id {
			return d
		}
	}
	return nil
}

func workflowTransitionsForFixture(t *testing.T, server *TestServer, workflowID int) (approveTID, denyTID int) {
	t.Helper()
	resp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/workflows/%d/transitions", workflowID), nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var doc struct {
		Data []struct {
			ID   int `json:"id"`
			From *struct {
				ID int `json:"id"`
			} `json:"from"`
			To struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"to"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &doc)
	for _, tr := range doc.Data {
		if tr.From == nil {
			continue
		}
		switch tr.To.Name {
		case "CF Done":
			approveTID = tr.ID
		case "CF Rejected":
			denyTID = tr.ID
		}
	}
	if approveTID == 0 || denyTID == 0 {
		t.Fatalf("fixture transitions not found: approve=%d deny=%d", approveTID, denyTID)
	}
	return approveTID, denyTID
}

// buildConformanceFixture creates a fully populated configuration set and
// exports its canonical template. Everything runs through API v2.
func buildConformanceFixture(t *testing.T, server *TestServer) conformanceFixture {
	t.Helper()

	todoID := findV2StatusCategory(t, server, "To Do")
	doneID := findV2StatusCategory(t, server, "Done")
	openID := createV2Status(t, server, "CF Open", todoID)
	doneStatusID := createV2Status(t, server, "CF Done", doneID)
	rejectedID := createV2Status(t, server, "CF Rejected", doneID)
	workflowID := createV2WorkflowWithTransitions(t, server, "CF Workflow", []map[string]interface{}{
		{"from_status_id": nil, "to_status_id": openID, "display_order": 0},
		{"from_status_id": openID, "to_status_id": doneStatusID, "display_order": 1},
		{"from_status_id": openID, "to_status_id": rejectedID, "display_order": 2},
	})
	riskFieldID := createV2CustomField(t, server, "Conformance Score", "number")
	screenID := createV2ScreenWithFields(t, server, "CF Screen", riskFieldID)
	priorityID := createV2Priority(t, server, "CF Critical")
	itemTypeID := createV2ItemType(t, server, "CF Hazard")
	createV2LinkType(t, server, "conforms-to", "Traceability link", "conforms to", "is conformed to", "#123abc", []string{"item"})

	roles := GetWorkspaceRoles(t, server)
	roleID := 0
	for _, id := range roles {
		roleID = id
		break
	}
	approveTID, denyTID := workflowTransitionsForFixture(t, server, workflowID)

	approvalResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/approval-sets", map[string]interface{}{
		"name":        "CF Approvals",
		"workflow_id": workflowID,
		"set_statuses": []map[string]interface{}{
			{
				"status_id":             openID,
				"approve_transition_id": approveTID,
				"deny_transition_id":    denyTID,
				"step_mode":             "sequential",
				"steps": []map[string]interface{}{
					{
						"name":              "Manager sign-off",
						"display_order":     0,
						"quorum_mode":       "any",
						"approver_source":   "role",
						"approver_role_id":  roleID,
						"rejection_policy":  "any_rejection_fails",
						"on_leave_strategy": "use_substitute",
					},
				},
			},
		},
	})
	defer approvalResp.Body.Close()
	AssertStatusCode(t, approvalResp, http.StatusCreated)
	var createdApproval struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, approvalResp, &createdApproval)
	approvalSetID := createdApproval.Data.ID

	inventory := fetchV2LinkTypes(t, server)
	linkTypeID := 0
	for _, lt := range inventory {
		if strings.EqualFold(lt.Name, "conforms-to") {
			linkTypeID = lt.ID
		}
	}

	setID := createV2ConfigSet(t, server, map[string]interface{}{
		"name":                 "CF Set",
		"workflow_id":          workflowID,
		"approval_set_id":      approvalSetID,
		"default_item_type_id": itemTypeID,
		"create_screen_id":     screenID,
		"edit_screen_id":       screenID,
		"view_screen_id":       screenID,
		"priority_ids":         []int{priorityID},
		"item_type_configs": []map[string]interface{}{
			{"item_type_id": itemTypeID, "workflow_id": workflowID},
		},
	})

	return conformanceFixture{
		setID:            setID,
		canonical:        exportV2Template(t, server, setID),
		workflowID:       workflowID,
		screenID:         screenID,
		linkTypeID:       linkTypeID,
		approvalSetID:    approvalSetID,
		openStatusID:     openID,
		doneStatusID:     doneStatusID,
		rejectedStatusID: rejectedID,
	}
}

// putFixtureTransitions replaces the fixture workflow's transitions. A nil
// from_status_id marks the initial transition.
func putFixtureTransitions(t *testing.T, server *TestServer, fx conformanceFixture, withOpenToDone bool, doneOrder int) {
	t.Helper()
	transitions := []map[string]interface{}{
		{"from_status_id": nil, "to_status_id": fx.openStatusID, "display_order": 0},
	}
	if withOpenToDone {
		transitions = append(transitions, map[string]interface{}{
			"from_status_id": fx.openStatusID, "to_status_id": fx.doneStatusID, "display_order": doneOrder,
		})
	}
	transitions = append(transitions, map[string]interface{}{
		"from_status_id": fx.openStatusID, "to_status_id": fx.rejectedStatusID, "display_order": 2,
	})
	resp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/workflows/%d/transitions", fx.workflowID), map[string]interface{}{
		"transitions": transitions,
	})
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
}

func TestConfigurationSetConformance(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	fx := buildConformanceFixture(t, server)
	const workflowOrderDriftID = "workflows/differing/cf workflow/transition/cf open→cf done"

	t.Run("clean_set_conforms", func(t *testing.T) {
		conformant, driftCount, _ := conformanceCheck(t, server, fx.setID, fx.canonical)
		if !conformant || driftCount != 0 {
			t.Fatalf("clean fixture reported conformant=%t drift=%d", conformant, driftCount)
		}
	})

	t.Run("edited_workflow_is_detected_and_named", func(t *testing.T) {
		putFixtureTransitions(t, server, fx, true, 9)

		conformant, _, drifts := conformanceCheck(t, server, fx.setID, fx.canonical)
		if conformant {
			t.Fatalf("edited workflow not detected")
		}
		row := findDriftRow(drifts, workflowOrderDriftID)
		if row == nil {
			t.Fatalf("expected %q in drifts: %+v", workflowOrderDriftID, drifts)
		}
		if row["detail"] != `workflow "CF Workflow" transition "CF Open" → "CF Done" differs (order/handles changed)` {
			t.Fatalf("detail must name the difference: %v", row["detail"])
		}
		if row["repairable"] != true {
			t.Fatalf("edited workflow must be repairable: %v", row)
		}

		// Per-row repair fixes exactly this row.
		repaired, postConformant, outcomes, postDrifts := conformanceRepairDebug(t, server, fx.setID, fx.canonical, []string{workflowOrderDriftID})
		if repaired != 1 || len(outcomes) != 1 {
			t.Fatalf("per-row repair = %d repaired, %d outcomes: outcomes=%+v postDrifts=%+v", repaired, len(outcomes), outcomes, postDrifts)
		}
		if outcomes[0]["status"] != "repaired" {
			t.Fatalf("outcome = %v", outcomes[0])
		}
		if !postConformant {
			t.Fatalf("per-row repair left drift behind")
		}
	})

	t.Run("removed_screen_field_is_detected_and_repaired", func(t *testing.T) {
		resp := MakeBearerRequest(t, server, http.MethodPut, fmt.Sprintf("/rest/api/v2/screens/%d/fields", fx.screenID), map[string]interface{}{
			"fields": []map[string]interface{}{
				{"field_type": "default", "field_identifier": "title", "display_order": 0},
			},
		})
		defer resp.Body.Close()
		AssertStatusCode(t, resp, http.StatusOK)

		const driftID = "screens/missing/cf screen/field/custom:conformance score"
		conformant, _, drifts := conformanceCheck(t, server, fx.setID, fx.canonical)
		if conformant || findDriftRow(drifts, driftID) == nil {
			t.Fatalf("removed screen field not reported: %+v", drifts)
		}

		repaired, postConformant, _, postDrifts := conformanceRepairDebug(t, server, fx.setID, fx.canonical, nil)
		if repaired == 0 || !postConformant {
			t.Fatalf("repair did not restore the screen: repaired=%d conformant=%t postDrifts=%+v", repaired, postConformant, postDrifts)
		}
	})

	t.Run("disabled_link_type_is_detected_and_reactivated", func(t *testing.T) {
		patch := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/link-types/%d", fx.linkTypeID), map[string]interface{}{
			"active": false,
		})
		defer patch.Body.Close()
		AssertStatusCode(t, patch, http.StatusOK)

		const driftID = "link_types/missing/conforms-to"
		conformant, _, drifts := conformanceCheck(t, server, fx.setID, fx.canonical)
		if conformant || findDriftRow(drifts, driftID) == nil {
			t.Fatalf("disabled link type not reported: %+v", drifts)
		}

		repaired, postConformant, _, postDrifts := conformanceRepairDebug(t, server, fx.setID, fx.canonical, nil)
		if repaired == 0 || !postConformant {
			t.Fatalf("repair did not restore the link type: repaired=%d conformant=%t postDrifts=%+v", repaired, postConformant, postDrifts)
		}
		// Repair reactivated the row instead of creating a duplicate.
		inventory := fetchV2LinkTypes(t, server)
		count := 0
		for _, lt := range inventory {
			if strings.EqualFold(lt.Name, "conforms-to") {
				count++
				if !lt.Active {
					t.Fatalf("link type still inactive after repair: %+v", lt)
				}
			}
		}
		if count != 1 {
			t.Fatalf("found %d conforms-to rows, want 1", count)
		}
	})

	t.Run("altered_approval_step_is_detected_and_repaired", func(t *testing.T) {
		// Read the approval set, alter the step's display order, write it back.
		getResp := MakeBearerRequest(t, server, http.MethodGet, fmt.Sprintf("/rest/api/v2/approval-sets/%d", fx.approvalSetID), nil)
		var approval struct {
			Data struct {
				SetStatuses []map[string]any `json:"set_statuses"`
			} `json:"data"`
		}
		DecodeJSON(t, getResp, &approval)
		getResp.Body.Close()
		if len(approval.Data.SetStatuses) != 1 {
			t.Fatalf("fixture approval set has %d statuses", len(approval.Data.SetStatuses))
		}
		steps, _ := approval.Data.SetStatuses[0]["steps"].([]any)
		if len(steps) != 1 {
			t.Fatalf("fixture approval status has %d steps", len(steps))
		}
		step, _ := steps[0].(map[string]any)
		step["display_order"] = 4

		patch := MakeBearerRequest(t, server, http.MethodPatch, fmt.Sprintf("/rest/api/v2/approval-sets/%d", fx.approvalSetID), map[string]any{
			"set_statuses": approval.Data.SetStatuses,
		})
		defer patch.Body.Close()
		AssertStatusCode(t, patch, http.StatusOK)

		const driftID = "approval_sets/differing/cf approvals/status/cf open"
		conformant, _, drifts := conformanceCheck(t, server, fx.setID, fx.canonical)
		if conformant {
			t.Fatalf("altered approval step not detected")
		}
		row := findDriftRow(drifts, driftID)
		if row == nil {
			t.Fatalf("expected %q in drifts: %+v", driftID, drifts)
		}
		if row["detail"] != `approval set "CF Approvals" approval configuration for status "CF Open" differs from the template` {
			t.Fatalf("detail must name the status: %v", row["detail"])
		}

		repaired, postConformant, outcomes, postDrifts := conformanceRepairDebug(t, server, fx.setID, fx.canonical, nil)
		if repaired == 0 || !postConformant {
			t.Fatalf("repair did not restore the approval set: repaired=%d conformant=%t outcomes=%+v postDrifts=%+v", repaired, postConformant, outcomes, postDrifts)
		}
	})

	t.Run("removed_transition_cascades_and_repair_restores_both", func(t *testing.T) {
		// Removing Open→Done cascades the approval binding that references it:
		// one deliberate edit, two drift classes.
		putFixtureTransitions(t, server, fx, false, 1)

		conformant, driftCount, drifts := conformanceCheck(t, server, fx.setID, fx.canonical)
		if conformant {
			t.Fatalf("removed transition not detected")
		}
		if findDriftRow(drifts, "workflows/missing/cf workflow/transition/cf open→cf done") == nil {
			t.Fatalf("missing transition not reported: %+v", drifts)
		}
		if findDriftRow(drifts, "approval_sets/missing/cf approvals/status/cf open") == nil {
			t.Fatalf("cascaded approval binding not reported: %+v", drifts)
		}
		_ = driftCount

		repaired, postConformant, outcomes, postDrifts := conformanceRepairDebug(t, server, fx.setID, fx.canonical, nil)
		if repaired < 2 || !postConformant {
			t.Fatalf("repair did not restore transition and approval binding: repaired=%d conformant=%t outcomes=%+v postDrifts=%+v", repaired, postConformant, outcomes, postDrifts)
		}
	})

	t.Run("repair_on_clean_is_a_noop", func(t *testing.T) {
		repaired, postConformant, outcomes, postDrifts := conformanceRepairDebug(t, server, fx.setID, fx.canonical, nil)
		if repaired != 0 || len(outcomes) != 0 || !postConformant {
			t.Fatalf("repair on clean set was not a no-op: repaired=%d outcomes=%+v conformant=%t postDrifts=%+v",
				repaired, outcomes, postConformant, postDrifts)
		}
	})
}

func TestConfigurationSetConformanceRequiresSystemAdmin(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	fx := buildConformanceFixture(t, server)

	_, _, editorPass := CreateTestUserWithCredentials(t, server, "conformance-editor", "conformance-editor@test.local")
	_, editorToken := CreateAuthCredentialsForUser(t, server, "conformance-editor", editorPass)

	check := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/configuration-sets/%d/conformance/check", fx.setID), json.RawMessage(fx.canonical))
	defer check.Body.Close()
	AssertStatusCode(t, check, http.StatusForbidden)
	var checkErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, check, &checkErr)
	if checkErr.Error.Code != "insufficient_permission" {
		t.Fatalf("check denial code = %q", checkErr.Error.Code)
	}

	repair := MakeBearerRequestWithToken(t, server, editorToken, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/configuration-sets/%d/conformance/repair", fx.setID), map[string]any{
			"template": json.RawMessage(fx.canonical),
		})
	defer repair.Body.Close()
	AssertStatusCode(t, repair, http.StatusForbidden)
}

func TestConfigurationSetConformanceDefaultSetRefused(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)
	fx := buildConformanceFixture(t, server)

	defaultID := GetDefaultConfigurationSet(t, server)
	resp := MakeBearerRequest(t, server, http.MethodPost,
		fmt.Sprintf("/rest/api/v2/configuration-sets/%d/conformance/check", defaultID),
		json.RawMessage(fx.canonical))
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusForbidden)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	DecodeJSON(t, resp, &errBody)
	if errBody.Error.Code != "default_not_exportable" {
		t.Fatalf("default set denial code = %q", errBody.Error.Code)
	}
}
