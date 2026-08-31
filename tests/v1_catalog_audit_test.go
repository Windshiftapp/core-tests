package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/logger"
)

func TestV1WorkspaceCatalogMutationsEmitOneAuditPerOperation(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	workspaceID, _ := CreateTestWorkspace(t, ts, "V1 Catalog Audit", shortKey("VCA"))

	testCatalogAuditCRUD(t, ts, workspaceID, catalogAuditCase{
		path:         "labels",
		createBody:   map[string]interface{}{"name": "Audit label", "color": "#112233"},
		updateBody:   map[string]interface{}{"name": "Updated audit label"},
		resourceType: logger.ResourceLabel,
		createAction: logger.ActionLabelCreate,
		updateAction: logger.ActionLabelUpdate,
		deleteAction: logger.ActionLabelDelete,
	})

	testCatalogAuditCRUD(t, ts, workspaceID, catalogAuditCase{
		path:         "page-labels",
		createBody:   map[string]interface{}{"name": "Audit page label", "color": "#223344"},
		updateBody:   map[string]interface{}{"name": "Updated audit page label"},
		resourceType: logger.ResourcePageLabel,
		createAction: logger.ActionPageLabelCreate,
		updateAction: logger.ActionPageLabelUpdate,
		deleteAction: logger.ActionPageLabelDelete,
	})

	testCatalogAuditCRUD(t, ts, workspaceID, catalogAuditCase{
		path: "templates",
		createBody: map[string]interface{}{
			"name":             "Audit template",
			"description_body": "Template body",
			"mode":             "selectable",
			"item_type_ids":    []int{},
		},
		updateBody:   map[string]interface{}{"name": "Updated audit template"},
		resourceType: logger.ResourceItemTemplate,
		createAction: logger.ActionTemplateCreate,
		updateAction: logger.ActionTemplateUpdate,
		deleteAction: logger.ActionTemplateDelete,
	})
}

type catalogAuditCase struct {
	path         string
	createBody   map[string]interface{}
	updateBody   map[string]interface{}
	resourceType string
	createAction string
	updateAction string
	deleteAction string
}

func testCatalogAuditCRUD(t *testing.T, ts *TestServer, workspaceID int, tc catalogAuditCase) {
	t.Helper()
	t.Run(tc.path, func(t *testing.T) {
		basePath := fmt.Sprintf("/rest/api/v1/workspaces/%d/%s", workspaceID, tc.path)
		createResp := MakeBearerRequest(t, ts, http.MethodPost, basePath, tc.createBody)
		defer createResp.Body.Close()
		AssertStatusCode(t, createResp, http.StatusCreated)
		var created map[string]interface{}
		DecodeJSON(t, createResp, &created)
		resourceID := ExtractIDFromResponse(t, created)

		updateResp := MakeBearerRequest(t, ts, http.MethodPut,
			fmt.Sprintf("%s/%d", basePath, resourceID), tc.updateBody)
		defer updateResp.Body.Close()
		AssertStatusCode(t, updateResp, http.StatusOK)

		deleteResp := MakeBearerRequest(t, ts, http.MethodDelete,
			fmt.Sprintf("%s/%d", basePath, resourceID), nil)
		defer deleteResp.Body.Close()
		AssertStatusCode(t, deleteResp, http.StatusNoContent)

		rows, err := ts.DB().Query(`
			SELECT action_type FROM audit_logs
			WHERE resource_type = ? AND resource_id = ? AND success = true
			ORDER BY id
		`, tc.resourceType, resourceID)
		if err != nil {
			t.Fatalf("query %s audit rows: %v", tc.path, err)
		}
		defer rows.Close()

		var actions []string
		for rows.Next() {
			var action string
			if err := rows.Scan(&action); err != nil {
				t.Fatalf("scan %s audit row: %v", tc.path, err)
			}
			actions = append(actions, action)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate %s audit rows: %v", tc.path, err)
		}

		want := []string{tc.createAction, tc.updateAction, tc.deleteAction}
		if len(actions) != len(want) {
			t.Fatalf("%s audit actions = %v, want %v", tc.path, actions, want)
		}
		for i := range want {
			if actions[i] != want[i] {
				t.Fatalf("%s audit action[%d] = %q, want %q", tc.path, i, actions[i], want[i])
			}
		}
	})
}
