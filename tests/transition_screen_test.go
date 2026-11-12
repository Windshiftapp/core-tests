package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// assertTransitionScreenBinding asserts the governance payload carries a screen
// summary for the expected screen.
func assertTransitionScreenBinding(t *testing.T, governance map[string]interface{}, wantID int, wantNamePrefix string) {
	t.Helper()
	screen, ok := governance["screen"].(map[string]interface{})
	if !ok {
		t.Fatalf("governance screen = %#v, want object", governance["screen"])
	}
	if got := int(screen["id"].(float64)); got != wantID {
		t.Fatalf("governance screen id = %d, want %d", got, wantID)
	}
	if wantNamePrefix != "" {
		name, _ := screen["name"].(string)
		if !strings.HasPrefix(name, wantNamePrefix) {
			t.Fatalf("governance screen name = %q, want prefix %q", name, wantNamePrefix)
		}
	}
	for _, field := range []string{"field_count", "required_field_count"} {
		if _, ok := screen[field]; !ok {
			t.Fatalf("governance screen summary missing %q", field)
		}
	}
}

// TestTransitionScreenBinding covers the save-immediately binding API (v2):
// bind, read back through governance, the in-use delete guard, unbind, and the
// validation/not-found contracts.
func TestTransitionScreenBinding(t *testing.T) {
	env := setupConditionTestData(t)
	v2 := "/v2"

	screenName := "Close Screen " + shortKey("SCR")
	screenResp := MakeAuthRequest(t, env.server, http.MethodPost, v2+"/screens", map[string]interface{}{
		"name":        screenName,
		"description": "transition screen fixture",
	})
	AssertStatusCode(t, screenResp, http.StatusCreated)
	var screen map[string]interface{}
	DecodeJSON(t, screenResp, &screen)
	screenResp.Body.Close()
	screenID := ExtractIDFromResponse(t, screen)

	transitionID := env.findTransitionID(t, env.openID, env.inProgressID)
	screenPath := fmt.Sprintf("%s/transitions/%d/screen", v2, transitionID)

	// Bind and read the refreshed governance from the same response.
	bindResp := MakeAuthRequest(t, env.server, http.MethodPut, screenPath, map[string]interface{}{"screen_id": screenID})
	AssertStatusCode(t, bindResp, http.StatusOK)
	var bindGov map[string]interface{}
	DecodeJSON(t, bindResp, &bindGov)
	bindResp.Body.Close()
	assertTransitionScreenBinding(t, bindGov, screenID, screenName)

	// The governance read reflects the binding.
	govResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("%s/transitions/%d/governance", v2, transitionID), nil)
	AssertStatusCode(t, govResp, http.StatusOK)
	var readGov map[string]interface{}
	DecodeJSON(t, govResp, &readGov)
	govResp.Body.Close()
	assertTransitionScreenBinding(t, readGov, screenID, screenName)

	// An in-use screen cannot be deleted; the admin must unbind first.
	deleteInUseResp := MakeAuthRequest(t, env.server, http.MethodDelete, fmt.Sprintf("%s/screens/%d", v2, screenID), nil)
	AssertStatusCode(t, deleteInUseResp, http.StatusConflict)
	deleteInUseResp.Body.Close()

	// Unbind.
	unbindResp := MakeAuthRequest(t, env.server, http.MethodPut, screenPath, map[string]interface{}{"screen_id": nil})
	AssertStatusCode(t, unbindResp, http.StatusOK)
	var unbindGov map[string]interface{}
	DecodeJSON(t, unbindResp, &unbindGov)
	unbindResp.Body.Close()
	if unbindGov["screen"] != nil {
		t.Fatalf("governance screen after unbind = %#v, want null", unbindGov["screen"])
	}

	// The screen is deletable once unbound.
	deleteResp := MakeAuthRequest(t, env.server, http.MethodDelete, fmt.Sprintf("%s/screens/%d", v2, screenID), nil)
	AssertStatusCode(t, deleteResp, http.StatusNoContent)
	deleteResp.Body.Close()

	// A missing screen is a validation error, not a silent no-op.
	missingScreenResp := MakeAuthRequest(t, env.server, http.MethodPut, screenPath, map[string]interface{}{"screen_id": 999999})
	AssertStatusCode(t, missingScreenResp, http.StatusBadRequest)
	missingScreenResp.Body.Close()

	// A missing transition is not found.
	missingTransitionResp := MakeAuthRequest(t, env.server, http.MethodPut, v2+"/transitions/999999/screen", map[string]interface{}{"screen_id": nil})
	AssertStatusCode(t, missingTransitionResp, http.StatusNotFound)
	missingTransitionResp.Body.Close()
}

type transitionScreenErrorDoc struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			MissingFields []struct {
				Identifier string `json:"identifier"`
				FieldType  string `json:"field_type"`
			} `json:"missing_fields"`
		} `json:"details"`
	} `json:"error"`
}

// TestTransitionScreenEnforcement covers the runtime gate: a bound screen with
// a required field rejects the move with screen_required, the available-
// transitions read exposes the screen summary, and a submitted field is applied
// and persisted with the status change.
func TestTransitionScreenEnforcement(t *testing.T) {
	env := setupConditionTestData(t)
	v2 := "/v2"

	// The fixture item has no due date, so a required due_date gate fires.
	screenName := "Enforce Screen " + shortKey("ENF")
	screenResp := MakeAuthRequest(t, env.server, http.MethodPost, v2+"/screens", map[string]interface{}{"name": screenName})
	AssertStatusCode(t, screenResp, http.StatusCreated)
	var screen map[string]interface{}
	DecodeJSON(t, screenResp, &screen)
	screenResp.Body.Close()
	screenID := ExtractIDFromResponse(t, screen)

	fieldsResp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("%s/screens/%d/fields", v2, screenID), map[string]interface{}{
		"fields": []map[string]interface{}{
			{"field_type": "system", "field_identifier": "due_date", "display_order": 0, "is_required": true, "field_width": "half"},
		},
	})
	AssertStatusCode(t, fieldsResp, http.StatusOK)
	fieldsResp.Body.Close()

	transitionID := env.findTransitionID(t, env.openID, env.inProgressID)
	bindResp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("%s/transitions/%d/screen", v2, transitionID), map[string]interface{}{"screen_id": screenID})
	AssertStatusCode(t, bindResp, http.StatusOK)
	bindResp.Body.Close()

	// Available transitions advertise the screen so the client can open it.
	availResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("%s/items/%d/available-transitions", v2, env.itemID), nil)
	AssertStatusCode(t, availResp, http.StatusOK)
	var summary map[string]interface{}
	DecodeJSON(t, availResp, &summary)
	availResp.Body.Close()
	options, _ := summary["available_transitions"].([]interface{})
	var matched map[string]interface{}
	for _, raw := range options {
		option, _ := raw.(map[string]interface{})
		if option != nil && int(option["id"].(float64)) == env.inProgressID {
			matched = option
			break
		}
	}
	if matched == nil {
		t.Fatalf("available transitions did not include status %d: %#v", env.inProgressID, options)
	}
	if matched["screen"] == nil {
		t.Fatal("available transition is missing the screen summary")
	}

	// A move without the required field is rejected with screen_required and a
	// structured missing-fields payload, and does not change status.
	rejectResp := MakeAuthRequest(t, env.server, http.MethodPost, fmt.Sprintf("%s/items/%d/transition", v2, env.itemID), map[string]interface{}{"to_status_id": env.inProgressID})
	AssertStatusCode(t, rejectResp, http.StatusBadRequest)
	var rejectDoc transitionScreenErrorDoc
	DecodeJSON(t, rejectResp, &rejectDoc)
	rejectResp.Body.Close()
	if rejectDoc.Error.Code != "screen_required" {
		t.Fatalf("rejection code = %q, want screen_required", rejectDoc.Error.Code)
	}
	var sawPriority bool
	for _, field := range rejectDoc.Error.Details.MissingFields {
		if field.Identifier == "due_date" {
			sawPriority = true
		}
	}
	if !sawPriority {
		t.Fatalf("missing_fields = %#v, want due_date", rejectDoc.Error.Details.MissingFields)
	}

	// The item did not move.
	itemResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("%s/items/%d", v2, env.itemID), nil)
	AssertStatusCode(t, itemResp, http.StatusOK)
	var item map[string]interface{}
	DecodeJSON(t, itemResp, &item)
	itemResp.Body.Close()
	if got := int(item["status_id"].(float64)); got != env.openID {
		t.Fatalf("status after rejected transition = %d, want %d", got, env.openID)
	}

	// Submitting the required field moves the item and persists the value.
	okResp := MakeAuthRequest(t, env.server, http.MethodPost, fmt.Sprintf("%s/items/%d/transition", v2, env.itemID), map[string]interface{}{
		"to_status_id": env.inProgressID,
		"fields":       map[string]interface{}{"due_date": "2026-12-31"},
	})
	AssertStatusCode(t, okResp, http.StatusOK)
	var transitioned map[string]interface{}
	DecodeJSON(t, okResp, &transitioned)
	okResp.Body.Close()
	moved, _ := transitioned["item"].(map[string]interface{})
	if moved == nil {
		t.Fatalf("transition response missing item: %#v", transitioned)
	}
	if got := int(moved["status_id"].(float64)); got != env.inProgressID {
		t.Fatalf("status after transition = %d, want %d", got, env.inProgressID)
	}
	due, _ := moved["due_date"].(string)
	if !strings.HasPrefix(due, "2026-12-31") {
		t.Fatalf("due_date after transition = %q, want 2026-12-31", due)
	}
}

// TestTransitionScreenCommentAndLabels covers the two values a transition
// screen collects that are not item-update fields: the optional comment is
// persisted as an item comment, and a required labels field can be satisfied
// through the transition itself.
func TestTransitionScreenCommentAndLabels(t *testing.T) {
	env := setupConditionTestData(t)
	v2 := "/v2"

	label := createLabelFx(t, env.server, env.workspaceID, "Screen Label "+shortKey("LBL"), "#123456")

	screenResp := MakeAuthRequest(t, env.server, http.MethodPost, v2+"/screens", map[string]interface{}{"name": "Comment Labels " + shortKey("CLB")})
	AssertStatusCode(t, screenResp, http.StatusCreated)
	var screen map[string]interface{}
	DecodeJSON(t, screenResp, &screen)
	screenResp.Body.Close()
	screenID := ExtractIDFromResponse(t, screen)

	fieldsResp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("%s/screens/%d/fields", v2, screenID), map[string]interface{}{
		"fields": []map[string]interface{}{
			{"field_type": "system", "field_identifier": "labels", "display_order": 0, "is_required": true, "field_width": "half"},
		},
	})
	AssertStatusCode(t, fieldsResp, http.StatusOK)
	fieldsResp.Body.Close()

	transitionID := env.findTransitionID(t, env.openID, env.inProgressID)
	bindResp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("%s/transitions/%d/screen", v2, transitionID), map[string]interface{}{"screen_id": screenID})
	AssertStatusCode(t, bindResp, http.StatusOK)
	bindResp.Body.Close()

	// Without labels the move is refused.
	rejectResp := MakeAuthRequest(t, env.server, http.MethodPost, fmt.Sprintf("%s/items/%d/transition", v2, env.itemID), map[string]interface{}{"to_status_id": env.inProgressID})
	AssertStatusCode(t, rejectResp, http.StatusBadRequest)
	rejectResp.Body.Close()

	// Supplying labels and a comment moves the item, persists the labels, and
	// records the comment.
	okResp := MakeAuthRequest(t, env.server, http.MethodPost, fmt.Sprintf("%s/items/%d/transition", v2, env.itemID), map[string]interface{}{
		"to_status_id": env.inProgressID,
		"fields": map[string]interface{}{
			"label_ids": []int{label.ID},
			"comment":   "Moving this along",
		},
	})
	AssertStatusCode(t, okResp, http.StatusOK)
	okResp.Body.Close()

	labelsResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("%s/items/%d/labels", v2, env.itemID), nil)
	AssertStatusCode(t, labelsResp, http.StatusOK)
	var labels []map[string]interface{}
	DecodeJSON(t, labelsResp, &labels)
	labelsResp.Body.Close()
	if len(labels) != 1 || int(labels[0]["id"].(float64)) != label.ID {
		t.Fatalf("labels after transition = %#v, want just %d", labels, label.ID)
	}

	commentsResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("%s/items/%d/comments", v2, env.itemID), nil)
	AssertStatusCode(t, commentsResp, http.StatusOK)
	var commentsDoc struct {
		Comments []map[string]interface{} `json:"comments"`
	}
	DecodeJSON(t, commentsResp, &commentsDoc)
	commentsResp.Body.Close()
	var sawComment bool
	for _, comment := range commentsDoc.Comments {
		if strings.Contains(fmt.Sprint(comment["content"]), "Moving this along") {
			sawComment = true
		}
	}
	if !sawComment {
		t.Fatalf("transition comment not persisted: %#v", commentsDoc.Comments)
	}
}

// TestBulkTransitionRefusesScreenBlockedBatch covers the bulk contract: a bulk
// caller cannot collect screen field values, so a batch containing a blocked
// item is refused as a whole instead of partially applied, and the client may
// retry without the blocked items.
func TestBulkTransitionRefusesScreenBlockedBatch(t *testing.T) {
	env := setupConditionTestData(t)
	v2 := "/v2"

	// A required due_date gate on Open -> In Progress.
	screenResp := MakeAuthRequest(t, env.server, http.MethodPost, v2+"/screens", map[string]interface{}{"name": "Bulk Screen " + shortKey("BLK")})
	AssertStatusCode(t, screenResp, http.StatusCreated)
	var screen map[string]interface{}
	DecodeJSON(t, screenResp, &screen)
	screenResp.Body.Close()
	screenID := ExtractIDFromResponse(t, screen)

	fieldsResp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("%s/screens/%d/fields", v2, screenID), map[string]interface{}{
		"fields": []map[string]interface{}{
			{"field_type": "system", "field_identifier": "due_date", "display_order": 0, "is_required": true, "field_width": "half"},
		},
	})
	AssertStatusCode(t, fieldsResp, http.StatusOK)
	fieldsResp.Body.Close()

	transitionID := env.findTransitionID(t, env.openID, env.inProgressID)
	bindResp := MakeAuthRequest(t, env.server, http.MethodPut, fmt.Sprintf("%s/transitions/%d/screen", v2, transitionID), map[string]interface{}{"screen_id": screenID})
	AssertStatusCode(t, bindResp, http.StatusOK)
	bindResp.Body.Close()

	// One item already satisfies the required field; the other does not.
	satisfied := CreateTestItem(t, env.server, env.workspaceID, "Has a due date")
	blocked := CreateTestItem(t, env.server, env.workspaceID, "Needs a due date")
	patchResp := MakeAuthRequest(t, env.server, http.MethodPatch, fmt.Sprintf("%s/items/%d", v2, satisfied), map[string]interface{}{"due_date": "2026-12-31T00:00:00Z"})
	AssertStatusCode(t, patchResp, http.StatusOK)
	patchResp.Body.Close()

	// The batch is refused as a whole: 409 with the blocked item listed, and
	// even the satisfiable item does not move.
	bulkResp := MakeAuthRequest(t, env.server, http.MethodPost, v2+"/items/bulk-transition", map[string]interface{}{
		"item_ids":     []int{satisfied, blocked},
		"to_status_id": env.inProgressID,
	})
	AssertStatusCode(t, bulkResp, http.StatusConflict)
	var bulkDoc struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Items []struct {
					ItemID int    `json:"item_id"`
					Key    string `json:"key"`
				} `json:"items"`
			} `json:"details"`
		} `json:"error"`
	}
	DecodeJSON(t, bulkResp, &bulkDoc)
	bulkResp.Body.Close()
	if bulkDoc.Error.Code != "screen_required" {
		t.Fatalf("bulk refusal code = %q, want screen_required", bulkDoc.Error.Code)
	}
	if len(bulkDoc.Error.Details.Items) != 1 || bulkDoc.Error.Details.Items[0].ItemID != blocked {
		t.Fatalf("blocked items = %#v, want just item %d", bulkDoc.Error.Details.Items, blocked)
	}
	if bulkDoc.Error.Details.Items[0].Key == "" {
		t.Fatalf("blocked item is missing its display key: %#v", bulkDoc.Error.Details.Items[0])
	}

	for _, id := range []int{satisfied, blocked} {
		statusResp := MakeAuthRequest(t, env.server, http.MethodGet, fmt.Sprintf("%s/items/%d", v2, id), nil)
		AssertStatusCode(t, statusResp, http.StatusOK)
		var item map[string]interface{}
		DecodeJSON(t, statusResp, &item)
		statusResp.Body.Close()
		if got := int(item["status_id"].(float64)); got != env.openID {
			t.Fatalf("item %d status after refused batch = %d, want %d", id, got, env.openID)
		}
	}

	// Retrying without the blocked item moves the rest.
	retryResp := MakeAuthRequest(t, env.server, http.MethodPost, v2+"/items/bulk-transition", map[string]interface{}{
		"item_ids":     []int{satisfied},
		"to_status_id": env.inProgressID,
	})
	AssertStatusCode(t, retryResp, http.StatusOK)
	var retry map[string]interface{}
	DecodeJSON(t, retryResp, &retry)
	retryResp.Body.Close()
	if got := int(retry["transitioned_count"].(float64)); got != 1 {
		t.Fatalf("retry transitioned_count = %d, want 1", got)
	}
}
