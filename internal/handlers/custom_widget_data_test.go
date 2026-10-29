//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
	"windshift/internal/reporting"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

type stubItemLister struct {
	calls  []services.ItemListRequest
	result services.ItemListResult
	err    error
}

func (s *stubItemLister) List(_ context.Context, request services.ItemListRequest) (services.ItemListResult, error) {
	s.calls = append(s.calls, request)
	return s.result, s.err
}

type stubAssetLister struct {
	assets []models.Asset
	total  int
	sets   []int
}

func (s *stubAssetLister) ListAssets(_ int, setID int, _ repository.AssetListFilter, _ string) ([]models.Asset, int, error) {
	s.sets = append(s.sets, setID)
	return s.assets, s.total, nil
}

type dataHandlerEnv struct {
	data   *CustomWidgetDataHandler
	crud   *CustomWidgetHandler
	tdb    *testutils.TestDB
	items  *stubItemLister
	assets *stubAssetLister
}

func newDataHandlerEnv(t *testing.T) dataHandlerEnv {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	permissionService, _, _ := createTestServices(t, *tdb)
	repo := repository.NewCustomWidgetRepository(tdb.GetDatabase())

	items := &stubItemLister{result: services.ItemListResult{Items: []models.Item{{ID: 1, Title: "One", StatusName: "Open"}}, Total: 1}}
	assets := &stubAssetLister{}
	return dataHandlerEnv{
		data:   NewCustomWidgetDataHandler(repo, permissionService, reporting.NewService(items, assets)),
		crud:   NewCustomWidgetHandler(repo, permissionService),
		tdb:    tdb,
		items:  items,
		assets: assets,
	}
}

func callCustomWidgetData(t *testing.T, handler *CustomWidgetDataHandler, userID int, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/custom-widgets/data", bytes.NewReader(payload))
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, &models.User{ID: userID}))
	rr := httptest.NewRecorder()
	handler.ResolveCustomWidgetData(rr, req)
	return rr
}

func TestCustomWidgetDataResolvesPreviewSlots(t *testing.T) {
	env := newDataHandlerEnv(t)
	userID := insertWidgetUser(t, env.tdb, "widget-data@example.test", "widget-data")

	rr := callCustomWidgetData(t, env.data, userID, map[string]any{
		"slots": []map[string]any{{
			"key": "k1", "entity": "item", "ql": "status_completed = false", "sort": "-updated_at", "limit": 5,
		}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var response struct {
		Results map[string]reporting.Result `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Results["k1"].Status != "ready" {
		t.Fatalf("result = %+v, want ready", response.Results["k1"])
	}
	if len(env.items.calls) != 1 {
		t.Fatalf("list calls = %d, want 1", len(env.items.calls))
	}
	// An unsaved preview carries no workspace, so the item service keeps the
	// viewer's full accessible set.
	if len(env.items.calls[0].WorkspaceIDs) != 0 {
		t.Fatalf("workspace scope = %v, want none for a preview", env.items.calls[0].WorkspaceIDs)
	}
}

func TestCustomWidgetDataScopesSavedWorkspaceWidget(t *testing.T) {
	env := newDataHandlerEnv(t)
	editorID := insertWidgetUser(t, env.tdb, "widget-data-editor@example.test", "widget-data-editor")
	viewerID := insertWidgetUser(t, env.tdb, "widget-data-viewer@example.test", "widget-data-viewer")
	outsiderID := insertWidgetUser(t, env.tdb, "widget-data-outsider@example.test", "widget-data-outsider")

	const workspaceID = 5150
	seedWidgetWorkspace(t, env.tdb, workspaceID, editorID, "Editor")
	seedWidgetWorkspace(t, env.tdb, workspaceID, viewerID, "Viewer")

	body := widgetBody()
	body["workspace_id"] = workspaceID
	body["visibility"] = models.CustomWidgetVisibilityWorkspace
	widgetID := createWidget(t, env.crud, editorID, body)

	request := map[string]any{
		"widget_id": widgetID,
		"slots": []map[string]any{{
			"key": "k1", "entity": "item", "ql": "", "limit": 5,
		}},
	}

	t.Run("a workspace member is scoped to the widget's workspace", func(t *testing.T) {
		rr := callCustomWidgetData(t, env.data, viewerID, request)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if len(env.items.calls) != 1 {
			t.Fatalf("list calls = %d, want 1", len(env.items.calls))
		}
		scope := env.items.calls[0].WorkspaceIDs
		if len(scope) != 1 || scope[0] != workspaceID {
			t.Fatalf("workspace scope = %v, want [%d]", scope, workspaceID)
		}
	})

	t.Run("a user outside the workspace cannot resolve the widget", func(t *testing.T) {
		before := len(env.items.calls)
		rr := callCustomWidgetData(t, env.data, outsiderID, request)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rr.Code)
		}
		if len(env.items.calls) != before {
			t.Fatalf("list calls = %d, want no query for a denied widget", len(env.items.calls))
		}
	})
}

func TestCustomWidgetDataRejectsBadRequests(t *testing.T) {
	env := newDataHandlerEnv(t)
	userID := insertWidgetUser(t, env.tdb, "widget-data-bad@example.test", "widget-data-bad")

	tooMany := make([]map[string]any, 0, reporting.MaxSlots+1)
	for i := 0; i <= reporting.MaxSlots; i++ {
		tooMany = append(tooMany, map[string]any{"key": "k" + string(rune('a'+i)), "entity": "item"})
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "no slots", body: map[string]any{"slots": []map[string]any{}}},
		{name: "too many slots", body: map[string]any{"slots": tooMany}},
		{
			name: "duplicate keys",
			body: map[string]any{"slots": []map[string]any{
				{"key": "k", "entity": "item"},
				{"key": "k", "entity": "item"},
			}},
		},
		{name: "missing key", body: map[string]any{"slots": []map[string]any{{"entity": "item"}}}},
		{name: "unknown entity", body: map[string]any{"slots": []map[string]any{{"key": "k", "entity": "workspace"}}}},
		{name: "asset without a set", body: map[string]any{"slots": []map[string]any{{"key": "k", "entity": "asset"}}}},
		{name: "unparseable cql", body: map[string]any{"slots": []map[string]any{{"key": "k", "entity": "item", "ql": "status = "}}}},
		{name: "unknown widget", body: map[string]any{"widget_id": 999999, "slots": []map[string]any{{"key": "k", "entity": "item"}}}},
	}

	for _, test := range cases {
		t.Run("rejects "+test.name, func(t *testing.T) {
			rr := callCustomWidgetData(t, env.data, userID, test.body)
			if rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 400 or 404; body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	if len(env.items.calls) != 0 {
		t.Fatalf("list calls = %d, want none for rejected requests", len(env.items.calls))
	}
}
