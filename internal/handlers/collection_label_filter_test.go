//go:build test

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestItemHandler_CollectionLabelQueriesIncludeAssignedItem(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	data := tdb.SeedTestData(t)
	permService, actTracker, notifService := createTestServices(t, *tdb)
	itemHandler := NewItemHandler(tdb.GetDatabase(), permService, actTracker, notifService)
	collectionHandler := NewCollectionHandler(tdb.GetDatabase(), permService)
	labelHandler := NewLabelHandler(
		repository.NewLabelRepository(tdb.GetDatabase()),
		repository.NewItemRepository(tdb.GetDatabase()),
		permService,
		logger.NewAuditor(tdb.GetDatabase()),
	)
	personalLabelHandler := NewPersonalLabelHandler(tdb.GetDatabase(), permService)

	statusID := data.StatusID
	priorityID := data.PriorityID
	createItemRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/items", models.Item{
		WorkspaceID: data.WorkspaceID,
		Title:       "GitHub-labeled item",
		StatusID:    &statusID,
		PriorityID:  &priorityID,
	}), nil)
	createItemRR.AssertStatusCode(http.StatusCreated)
	var item models.Item
	createItemRR.AssertJSONResponse(&item)

	createLabelRR := testutils.ExecuteAuthenticatedRequest(t, labelHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/labels", map[string]any{
		"name":         "github",
		"workspace_id": data.WorkspaceID,
	}), nil)
	createLabelRR.AssertStatusCode(http.StatusCreated)
	var label models.Label
	createLabelRR.AssertJSONResponse(&label)

	setLabelsRequest := testutils.CreateJSONRequest(t, "PUT", "/api/items/"+testutils.IntToString(item.ID)+"/labels", map[string]any{
		"label_ids": []int{label.ID},
	})
	setLabelsRequest.SetPathValue("id", testutils.IntToString(item.ID))
	setLabelsRR := testutils.ExecuteAuthenticatedRequest(t, labelHandler.SetItemLabels, setLabelsRequest, nil)
	setLabelsRR.AssertStatusCode(http.StatusOK)

	queries := []string{`labels = "github"`, `labels IN ("github")`, "`labels` = \"github\"", "`labels` IN (\"github\")"}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			workspaceID := data.WorkspaceID
			createCollectionRR := testutils.ExecuteAuthenticatedRequest(t, collectionHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/collections", models.Collection{
				Name:        "GitHub label collection",
				WorkspaceID: &workspaceID,
				QLQuery:     query,
			}), nil)
			createCollectionRR.AssertStatusCode(http.StatusCreated)
			var collection models.Collection
			createCollectionRR.AssertJSONResponse(&collection)

			listURL := "/api/items?collection_id=" + testutils.IntToString(collection.ID)
			listRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.GetAll, testutils.CreateJSONRequest(t, "GET", listURL, nil), nil)
			listRR.AssertStatusCode(http.StatusOK)
			var response models.PaginatedItemsResponse
			listRR.AssertJSONResponse(&response)

			if response.Pagination.Total != 1 || len(response.Items) != 1 || response.Items[0].ID != item.ID {
				t.Fatalf("query %q returned items %#v and total %d, want item %d and total 1", query, response.Items, response.Pagination.Total, item.ID)
			}

			directURL := "/api/items?ql=" + query
			directRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.GetAll, testutils.CreateJSONRequest(t, "GET", directURL, nil), nil)
			directRR.AssertStatusCode(http.StatusOK)
			var directResponse models.PaginatedItemsResponse
			directRR.AssertJSONResponse(&directResponse)
			if directResponse.Pagination.Total != 1 || len(directResponse.Items) != 1 || directResponse.Items[0].ID != item.ID {
				t.Fatalf("direct query %q returned items %#v and total %d, want item %d and total 1", query, directResponse.Items, directResponse.Pagination.Total, item.ID)
			}
		})
	}

	workspaceID := data.WorkspaceID
	publicSlug := "workspace-label-board"
	createPublicCollectionRR := testutils.ExecuteAuthenticatedRequest(t, collectionHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/collections", models.Collection{
		Name:        "Public global label collection",
		WorkspaceID: &workspaceID,
		QLQuery:     `workspace_id = 1 AND labels = "github"`,
	}), nil)
	createPublicCollectionRR.AssertStatusCode(http.StatusCreated)
	var publicCollection models.Collection
	createPublicCollectionRR.AssertJSONResponse(&publicCollection)
	// Public sharing is enabled directly because the standard fixture user has
	// workspace admin rights but not the separate global public-board permission.
	if err := repository.NewCollectionRepository(tdb.GetDatabase()).UpdatePublicSharing(publicCollection.ID, true, &publicSlug); err != nil {
		t.Fatalf("enable public sharing: %v", err)
	}

	publicRequest := httptest.NewRequest(http.MethodGet, "/api/public/board/"+publicSlug, nil)
	publicRequest.SetPathValue("slug", publicSlug)
	publicRecorder := httptest.NewRecorder()
	NewPublicBoardHandler(tdb.GetDatabase(), permService, t.TempDir()).GetPublicBoard(publicRecorder, publicRequest)
	if publicRecorder.Code != http.StatusOK {
		t.Fatalf("public board status = %d, want 200; body=%s", publicRecorder.Code, publicRecorder.Body.String())
	}
	var publicResponse publicBoardResponse
	if err := json.NewDecoder(publicRecorder.Body).Decode(&publicResponse); err != nil {
		t.Fatalf("decode public board: %v", err)
	}
	if publicResponse.TotalItems != 1 || publicResponse.LoadedItems != 1 {
		t.Fatalf("public board item metadata = %d/%d, want 1/1", publicResponse.TotalItems, publicResponse.LoadedItems)
	}

	personalLabelUserID := data.UserID
	createPersonalLabelRR := testutils.ExecuteAuthenticatedRequest(t, personalLabelHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/personal-labels", models.PersonalLabel{
		Name:   "github-personal",
		UserID: &personalLabelUserID,
	}), nil)
	createPersonalLabelRR.AssertStatusCode(http.StatusCreated)
	var personalLabel models.PersonalLabel
	createPersonalLabelRR.AssertJSONResponse(&personalLabel)

	setPersonalLabelsRequest := testutils.CreateJSONRequest(t, "PUT", "/api/items/"+testutils.IntToString(item.ID)+"/personal-labels", map[string]any{
		"label_ids": []int{personalLabel.ID},
	})
	setPersonalLabelsRequest.SetPathValue("id", testutils.IntToString(item.ID))
	setPersonalLabelsRR := testutils.ExecuteAuthenticatedRequest(t, personalLabelHandler.SetItemPersonalLabels, setPersonalLabelsRequest, nil)
	setPersonalLabelsRR.AssertStatusCode(http.StatusOK)

	for _, query := range []string{`labels = "github-personal"`, `labels IN ("github-personal")`} {
		t.Run("personal-label-excluded/"+query, func(t *testing.T) {
			workspaceID := data.WorkspaceID
			createCollectionRR := testutils.ExecuteAuthenticatedRequest(t, collectionHandler.Create, testutils.CreateJSONRequest(t, "POST", "/api/collections", models.Collection{
				Name:        "Personal label collection",
				WorkspaceID: &workspaceID,
				QLQuery:     query,
			}), nil)
			createCollectionRR.AssertStatusCode(http.StatusCreated)
			var collection models.Collection
			createCollectionRR.AssertJSONResponse(&collection)

			listURL := "/api/items?collection_id=" + testutils.IntToString(collection.ID)
			listRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.GetAll, testutils.CreateJSONRequest(t, "GET", listURL, nil), nil)
			listRR.AssertStatusCode(http.StatusOK)
			var response models.PaginatedItemsResponse
			listRR.AssertJSONResponse(&response)
			if response.Pagination.Total != 0 || len(response.Items) != 0 {
				t.Fatalf("query %q matched personal labels: items %#v and total %d, want none", query, response.Items, response.Pagination.Total)
			}

			directRR := testutils.ExecuteAuthenticatedRequest(t, itemHandler.GetAll, testutils.CreateJSONRequest(t, "GET", "/api/items?ql="+query, nil), nil)
			directRR.AssertStatusCode(http.StatusOK)
			var directResponse models.PaginatedItemsResponse
			directRR.AssertJSONResponse(&directResponse)
			if directResponse.Pagination.Total != 0 || len(directResponse.Items) != 0 {
				t.Fatalf("direct query %q matched personal labels: items %#v and total %d, want none", query, directResponse.Items, directResponse.Pagination.Total)
			}
		})
	}
}
