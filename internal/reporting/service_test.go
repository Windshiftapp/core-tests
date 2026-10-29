//go:build test

package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

type fakeItemLister struct {
	calls  []services.ItemListRequest
	result services.ItemListResult
	err    error
}

func (f *fakeItemLister) List(_ context.Context, request services.ItemListRequest) (services.ItemListResult, error) {
	f.calls = append(f.calls, request)
	return f.result, f.err
}

type fakeAssetLister struct {
	calls   []int
	assets  []models.Asset
	total   int
	err     error
	lastSet int
}

func (f *fakeAssetLister) ListAssets(_ int, setID int, _ repository.AssetListFilter, _ string) ([]models.Asset, int, error) {
	f.calls = append(f.calls, setID)
	f.lastSet = setID
	return f.assets, f.total, f.err
}

func itemFixture(n int) []models.Item {
	items := make([]models.Item, 0, n)
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	points := 3.0
	for i := 0; i < n; i++ {
		items = append(items, models.Item{
			ID:            i + 1,
			Key:           "WI-" + string(rune('A'+i)),
			Title:         "Item " + string(rune('A'+i)),
			StatusName:    "Open",
			ItemTypeName:  "Task",
			AssigneeName:  "Ada",
			WorkspaceName: "Windshift",
			CreatedAt:     created,
			UpdatedAt:     created,
			StoryPoints:   &points,
		})
	}
	return items
}

type fakeLinkProjector struct {
	links          map[int][]map[string]any
	truncated      bool
	calls          int
	lastWorkspaces []int
	lastFields     []string
}

func (f *fakeLinkProjector) ProjectLinkedItems(_ context.Context, _ []int, workspaceIDs []int, fields []string) (map[int][]map[string]any, bool, error) {
	f.calls++
	f.lastWorkspaces = workspaceIDs
	f.lastFields = fields
	return f.links, f.truncated, nil
}

func TestResolveAttachesLinkProjection(t *testing.T) {
	assets := &fakeAssetLister{
		assets: []models.Asset{{ID: 5, Title: "Laptop"}, {ID: 6, Title: "Phone"}},
		total:  2,
	}
	links := &fakeLinkProjector{links: map[int][]map[string]any{
		5: {{"creator": "Ada"}},
	}}
	service := NewService(&fakeItemLister{}, assets).WithLinks(links)

	results := service.Resolve(context.Background(), Request{
		UserID:                 7,
		AccessibleWorkspaceIDs: []int{3, 4},
		Slots: []Slot{{
			Key:        "assets",
			Entity:     EntityAsset,
			QL:         "a = 1",
			AssetSetID: 9,
			Limit:      5,
			Projection: &Projection{Entity: "item", Fields: []string{"creator"}, Mode: "fanout"},
		}},
	})

	rows, ok := results["assets"].Rows.([]AssetRow)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows = %#v, want two AssetRow", results["assets"].Rows)
	}
	if len(rows[0].LinkedItems) != 1 || rows[0].LinkedItems[0]["creator"] != "Ada" {
		t.Fatalf("linked items = %+v, want the projected creator", rows[0].LinkedItems)
	}
	if len(rows[1].LinkedItems) != 0 {
		t.Fatalf("linked items = %+v, want none for an unlinked asset", rows[1].LinkedItems)
	}
	if len(links.lastWorkspaces) != 2 || links.lastFields[0] != "creator" {
		t.Fatalf("projector saw workspaces %v fields %v, want the accessible set and requested fields", links.lastWorkspaces, links.lastFields)
	}
}

func TestResolveMarksProjectionTruncation(t *testing.T) {
	assets := &fakeAssetLister{assets: []models.Asset{{ID: 5, Title: "Laptop"}}, total: 1}
	links := &fakeLinkProjector{truncated: true}
	service := NewService(&fakeItemLister{}, assets).WithLinks(links)

	results := service.Resolve(context.Background(), Request{
		UserID:                 7,
		AccessibleWorkspaceIDs: []int{3},
		Slots: []Slot{{
			Key:        "assets",
			Entity:     EntityAsset,
			AssetSetID: 9,
			Limit:      5,
			Projection: &Projection{Entity: "item", Fields: []string{"creator"}},
		}},
	})

	if !results["assets"].Truncated {
		t.Fatal("truncated = false, want true when the projection hit its cap")
	}
}

func TestResolveIgnoresProjectionWithoutAProjector(t *testing.T) {
	assets := &fakeAssetLister{assets: []models.Asset{{ID: 5, Title: "Laptop"}}, total: 1}
	service := NewService(&fakeItemLister{}, assets)

	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots: []Slot{{
			Key:        "assets",
			Entity:     EntityAsset,
			AssetSetID: 9,
			Limit:      5,
			Projection: &Projection{Entity: "item", Fields: []string{"creator"}},
		}},
	})

	if results["assets"].Status != "ready" {
		t.Fatalf("status = %q, want ready", results["assets"].Status)
	}
	rows := results["assets"].Rows.([]AssetRow)
	if len(rows[0].LinkedItems) != 0 {
		t.Fatalf("linked items = %+v, want none without a projector", rows[0].LinkedItems)
	}
}

func TestResolveNormalizesItemRows(t *testing.T) {
	items := &fakeItemLister{result: services.ItemListResult{Items: itemFixture(2), Total: 2}}
	service := NewService(items, &fakeAssetLister{})

	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots: []Slot{{
			Key:    "k1",
			Entity: EntityItem,
			QL:     "status_completed = false",
			Sort:   "-updated_at",
			Limit:  10,
		}},
	})

	result := results["k1"]
	if result.Status != "ready" {
		t.Fatalf("status = %q, want ready (error: %+v)", result.Status, result.Error)
	}
	if result.Total != 2 || result.Truncated {
		t.Fatalf("total = %d truncated = %v, want 2/false", result.Total, result.Truncated)
	}
	rows, ok := result.Rows.([]ItemRow)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows = %#v, want two ItemRow", result.Rows)
	}
	if rows[0].Status != "Open" || rows[0].Assignee != "Ada" {
		t.Fatalf("row = %+v, want the joined display fields", rows[0])
	}

	if len(items.calls) != 1 {
		t.Fatalf("list calls = %d, want 1", len(items.calls))
	}
	call := items.calls[0]
	if call.UserID != 7 || !call.OmitDescriptions {
		t.Fatalf("call = %+v, want the viewer identity and summary fields only", call)
	}
	if call.SortBy != "updated_at" || call.SortAsc {
		t.Fatalf("sort = %q asc=%v, want updated_at descending", call.SortBy, call.SortAsc)
	}
}

// TestResolveItemPayloadStaysSmallAtTheLimit measures the wire shape at the row
// cap. The point of the normalized row is that a widget never ships
// descriptions, comments, or attachments, and that 1000 rows stay a reasonable
// payload for a dashboard.
func TestResolveItemPayloadStaysSmallAtTheLimit(t *testing.T) {
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	points := 5.0
	items := make([]models.Item, 0, 1000)
	for i := 0; i < 1000; i++ {
		items = append(items, models.Item{
			ID:            i + 1,
			Key:           fmt.Sprintf("WI-%d", i+1),
			Title:         strings.Repeat("t", 80),
			Description:   strings.Repeat("d", 4000),
			StatusName:    "In Progress",
			ItemTypeName:  "Bug",
			PriorityName:  "High",
			AssigneeName:  "Alexandra Someone",
			WorkspaceName: "Windshift",
			CreatedAt:     created,
			UpdatedAt:     created,
			StoryPoints:   &points,
		})
	}

	service := NewService(&fakeItemLister{result: services.ItemListResult{Items: items, Total: 1000}}, &fakeAssetLister{})
	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots:  []Slot{{Key: "k", Entity: EntityItem, QL: "a = 1", Limit: 1000}},
	})

	encoded, err := json.Marshal(results["k"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(encoded, []byte("dddd")) {
		t.Fatal("payload contains a description")
	}

	const budget = 400 * 1024
	if len(encoded) > budget {
		t.Fatalf("payload = %d bytes, want under %d", len(encoded), budget)
	}
	t.Logf("1000-row payload = %d bytes (%.1f KiB)", len(encoded), float64(len(encoded))/1024)
}

func TestResolveDedupesIdenticalSlotsAndSlicesPerLimit(t *testing.T) {
	items := &fakeItemLister{result: services.ItemListResult{Items: itemFixture(10), Total: 40}}
	service := NewService(items, &fakeAssetLister{})

	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots: []Slot{
			{Key: "small", Entity: EntityItem, QL: "a = 1", Sort: "-updated_at", Limit: 3},
			{Key: "large", Entity: EntityItem, QL: "a = 1", Sort: "-updated_at", Limit: 10},
		},
	})

	if len(items.calls) != 1 {
		t.Fatalf("list calls = %d, want 1 for identical slots", len(items.calls))
	}
	if items.calls[0].Pagination.Limit != 10 {
		t.Fatalf("fetch limit = %d, want the largest slot limit", items.calls[0].Pagination.Limit)
	}

	small := results["small"].Rows.([]ItemRow)
	large := results["large"].Rows.([]ItemRow)
	if len(small) != 3 || len(large) != 10 {
		t.Fatalf("row counts = %d/%d, want 3/10", len(small), len(large))
	}
	if !results["small"].Truncated || !results["large"].Truncated {
		t.Fatalf("truncation = %v/%v, want both true against total 40", results["small"].Truncated, results["large"].Truncated)
	}
	if results["small"].Total != 40 {
		t.Fatalf("total = %d, want the server total 40", results["small"].Total)
	}
}

func TestResolveKeepsOtherSlotsWhenOneFails(t *testing.T) {
	items := &fakeItemLister{err: errors.New("boom")}
	assets := &fakeAssetLister{assets: []models.Asset{{ID: 5, Title: "Laptop", AssetTag: "A-5"}}, total: 1}
	service := NewService(items, assets)

	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots: []Slot{
			{Key: "items", Entity: EntityItem, QL: "a = 1", Limit: 5},
			{Key: "assets", Entity: EntityAsset, QL: "b = 2", AssetSetID: 4, Limit: 5},
		},
	})

	if results["items"].Status != "error" || results["items"].Error == nil {
		t.Fatalf("item result = %+v, want a per-slot error", results["items"])
	}
	if results["assets"].Status != "ready" {
		t.Fatalf("asset result = %+v, want ready", results["assets"])
	}
	if assets.lastSet != 4 {
		t.Fatalf("asset set = %d, want 4", assets.lastSet)
	}
}

func TestResolveReportsTimeoutAsTimeout(t *testing.T) {
	items := &fakeItemLister{err: context.DeadlineExceeded}
	service := NewService(items, &fakeAssetLister{})

	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots:  []Slot{{Key: "items", Entity: EntityItem, QL: "a = 1", Limit: 5}},
	})

	if results["items"].Error == nil || results["items"].Error.Code != "timeout" {
		t.Fatalf("error = %+v, want code timeout", results["items"].Error)
	}
}

func TestAssetRowsCarryNoDescription(t *testing.T) {
	assets := &fakeAssetLister{
		assets: []models.Asset{{
			ID:            5,
			Title:         "Laptop",
			Description:   "serial 123, purchase order 456",
			AssetTag:      "A-5",
			StatusName:    "Active",
			AssetTypeName: "Hardware",
			SetName:       "IT",
		}},
		total: 1,
	}
	service := NewService(&fakeItemLister{}, assets)

	results := service.Resolve(context.Background(), Request{
		UserID: 7,
		Slots:  []Slot{{Key: "assets", Entity: EntityAsset, QL: "a = 1", AssetSetID: 4, Limit: 5}},
	})

	encoded, err := json.Marshal(results["assets"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "serial 123") || strings.Contains(string(encoded), "description") {
		t.Fatalf("asset payload leaked a description: %s", encoded)
	}
}

func TestNormalizeSlot(t *testing.T) {
	valid := []struct {
		name string
		slot Slot
	}{
		{name: "item slot", slot: Slot{Key: "k", Entity: EntityItem, QL: "a = 1"}},
		{name: "asset slot", slot: Slot{Key: "k", Entity: EntityAsset, QL: "a = 1", AssetSetID: 3}},
	}
	for _, test := range valid {
		t.Run("accepts "+test.name, func(t *testing.T) {
			normalized, err := NormalizeSlot(test.slot)
			if err != nil {
				t.Fatalf("NormalizeSlot() = %v", err)
			}
			if normalized.Limit != DefaultLimit {
				t.Fatalf("limit = %d, want default %d", normalized.Limit, DefaultLimit)
			}
		})
	}

	invalidSlots := []struct {
		name string
		slot Slot
		want string
	}{
		{name: "missing key", slot: Slot{Entity: EntityItem}, want: "key is required"},
		{name: "unknown entity", slot: Slot{Key: "k", Entity: "workspace"}, want: "entity must be"},
		{name: "asset without a set", slot: Slot{Key: "k", Entity: EntityAsset}, want: "asset_set_id"},
		{name: "limit above the cap", slot: Slot{Key: "k", Entity: EntityItem, Limit: 5000}, want: "limit"},
		{name: "unparseable ql", slot: Slot{Key: "k", Entity: EntityItem, QL: "a = "}, want: "ql"},
		{
			name: "projection field cap",
			slot: Slot{Key: "k", Entity: EntityAsset, AssetSetID: 1, Projection: &Projection{
				Entity: "item",
				Fields: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
			}},
			want: "exceeds",
		},
	}
	for _, test := range invalidSlots {
		t.Run("rejects "+test.name, func(t *testing.T) {
			_, err := NormalizeSlot(test.slot)
			if err == nil {
				t.Fatalf("NormalizeSlot() = nil, want error containing %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NormalizeSlot() = %q, want error containing %q", err.Error(), test.want)
			}
		})
	}
}

func TestSplitSort(t *testing.T) {
	tests := []struct {
		sort      string
		wantField string
		wantAsc   bool
	}{
		{sort: "", wantField: "", wantAsc: false},
		{sort: "updated_at", wantField: "updated_at", wantAsc: true},
		{sort: "-updated_at", wantField: "updated_at", wantAsc: false},
	}
	for _, test := range tests {
		field, ascending := SplitSort(test.sort)
		if field != test.wantField || ascending != test.wantAsc {
			t.Fatalf("SplitSort(%q) = %q/%v, want %q/%v", test.sort, field, ascending, test.wantField, test.wantAsc)
		}
	}
}
