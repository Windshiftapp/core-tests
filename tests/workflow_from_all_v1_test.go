package tests

import (
	"fmt"
	"net/http"
	"testing"
)

func TestV1WorkflowTransitionsExposeFromAllStatus(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	categories := CreateTestStatusCategories(t, server, "V1 from-all")
	statuses := CreateTestStatuses(t, server, "V1 from-all", categories)

	createResp := MakeAuthRequest(t, server, http.MethodPost, "/workflows", map[string]interface{}{
		"name":        "V1 from-all workflow",
		"description": "REST transition discriminator",
	})
	defer createResp.Body.Close()
	AssertStatusCode(t, createResp, http.StatusCreated)
	var workflow map[string]interface{}
	DecodeJSON(t, createResp, &workflow)
	workflowID := ExtractIDFromResponse(t, workflow)

	updateResp := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/workflows/%d/transitions", workflowID), []map[string]interface{}{
			{"from_status_id": nil, "to_status_id": statuses[0]},
			{"from_status_id": nil, "from_all_statuses": true, "to_status_id": statuses[2]},
		})
	defer updateResp.Body.Close()
	AssertStatusCode(t, updateResp, http.StatusOK)

	transitionsResp := MakeBearerRequest(t, server, http.MethodGet,
		fmt.Sprintf("/rest/api/v1/workflows/%d/transitions", workflowID), nil)
	defer transitionsResp.Body.Close()
	AssertStatusCode(t, transitionsResp, http.StatusOK)
	var transitions []map[string]interface{}
	DecodeJSON(t, transitionsResp, &transitions)

	var initial, fromAll map[string]interface{}
	for _, transition := range transitions {
		toStatusID, ok := transition["to_status_id"].(float64)
		if !ok {
			t.Fatalf("transition has invalid to_status_id: %#v", transition)
		}
		switch int(toStatusID) {
		case statuses[0]:
			initial = transition
		case statuses[2]:
			fromAll = transition
		}
	}
	if initial == nil || fromAll == nil {
		t.Fatalf("transitions = %#v, want initial and from-all rows", transitions)
	}
	if got, ok := initial["from_all_statuses"].(bool); !ok || got {
		t.Fatalf("initial from_all_statuses = %#v, want false", initial["from_all_statuses"])
	}
	if got, ok := fromAll["from_all_statuses"].(bool); !ok || !got {
		t.Fatalf("from-all from_all_statuses = %#v, want true", fromAll["from_all_statuses"])
	}
	if _, present := fromAll["from_status_id"]; present {
		t.Fatalf("from-all response unexpectedly includes from_status_id: %#v", fromAll)
	}
}
