//go:build test

package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

type fakeEdgeLinkLister struct {
	edges     []repository.LinkEdge
	truncated bool
	err       error
	lastItems []int
	lastAsset []int
	lastMax   int
}

func (f *fakeEdgeLinkLister) ListAssetItemLinks(_ context.Context, itemIDs, assetIDs []int, maxEdges int) ([]repository.LinkEdge, bool, error) {
	f.lastItems = itemIDs
	f.lastAsset = assetIDs
	f.lastMax = maxEdges
	return f.edges, f.truncated, f.err
}

type fakeReferenceLister struct {
	references map[int][]int
	err        error
	calls      int
}

func (f *fakeReferenceLister) ListItemAssetReferences(_ context.Context, _ []int) (map[int][]int, error) {
	f.calls++
	return f.references, f.err
}

func edgeService(edges *fakeEdgeLinkLister, refs *fakeReferenceLister) *Service {
	items := &fakeItemLister{result: services.ItemListResult{
		Items: []models.Item{
			{ID: 1, Key: "WI-1", Title: "Fix login", StatusName: "Open", WorkspaceName: "Windshift"},
			{ID: 2, Key: "WI-2", Title: "Ship it", StatusName: "Open", WorkspaceName: "Windshift"},
		},
		Total: 2,
	}}
	assets := &fakeAssetLister{
		assets: []models.Asset{
			{ID: 11, AssetTag: "A-11", Title: "Laptop", StatusName: "Active", SetName: "IT"},
			{ID: 12, AssetTag: "A-12", Title: "Phone", StatusName: "Active", SetName: "IT"},
		},
		total: 2,
	}
	service := NewService(items, assets)
	if edges != nil {
		service = service.WithEdges(edges, refs)
	}
	return service
}

func linksSlot(maxEdges int) Slot {
	return Slot{
		Key:    "graph",
		Entity: EntityLinks,
		Links: &LinksSpec{
			Item:     &QueryPart{QL: "", Sort: "-updated_at", Limit: 1000},
			Asset:    &QueryPart{QL: "", AssetSetID: 5, Limit: 1000},
			MaxEdges: maxEdges,
		},
	}
}

func TestResolveLinksBuildsBipartiteGraph(t *testing.T) {
	edges := &fakeEdgeLinkLister{edges: []repository.LinkEdge{{ItemID: 1, AssetID: 11, Label: "relates to", Kind: "link"}}}
	refs := &fakeReferenceLister{references: map[int][]int{2: {12, 999}}}
	service := edgeService(edges, refs)

	results := service.Resolve(context.Background(), Request{UserID: 7, Slots: []Slot{linksSlot(100)}})

	result := results["graph"]
	if result.Status != "ready" || result.Graph == nil {
		t.Fatalf("result = %+v, want a ready graph", result)
	}
	graph := result.Graph
	if graph.Counts.Items != 2 || graph.Counts.Assets != 2 {
		t.Fatalf("counts = %+v, want both seed sets", graph.Counts)
	}
	if len(graph.Nodes) != 4 {
		t.Fatalf("nodes = %+v, want four", graph.Nodes)
	}

	kinds := map[string]int{}
	pairs := map[string]bool{}
	for _, edge := range graph.Edges {
		kinds[edge.Kind]++
		pairs[edge.Source+"->"+edge.Target] = true
	}
	if kinds["link"] != 1 || kinds["field_reference"] != 1 {
		t.Fatalf("edge kinds = %v, want one link and one field reference", kinds)
	}
	if !pairs["item-1->asset-11"] || !pairs["item-2->asset-12"] {
		t.Fatalf("edges = %+v, want both resolved links", graph.Edges)
	}
	// Asset 999 is not in the seed set, so the reference to it is dropped.
	if pairs["item-2->asset-999"] {
		t.Fatal("a reference outside the asset seed set must not become an edge")
	}
	if edges.lastMax != 100 {
		t.Fatalf("max edges passed = %d, want 100", edges.lastMax)
	}
	if len(edges.lastItems) != 2 || len(edges.lastAsset) != 2 {
		t.Fatalf("seed sets = %v/%v, want both resolved sets", edges.lastItems, edges.lastAsset)
	}
}

func TestResolveLinksTruncatesAtTheEdgeCap(t *testing.T) {
	edges := &fakeEdgeLinkLister{edges: []repository.LinkEdge{
		{ItemID: 1, AssetID: 11, Kind: "link"},
		{ItemID: 2, AssetID: 12, Kind: "link"},
	}}
	service := edgeService(edges, &fakeReferenceLister{})

	results := service.Resolve(context.Background(), Request{UserID: 7, Slots: []Slot{linksSlot(1)}})

	result := results["graph"]
	if !result.Truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(result.Graph.Edges) != 1 {
		t.Fatalf("edges = %d, want the cap of 1", len(result.Graph.Edges))
	}
}

func TestResolveLinksReportsFailurePerSlot(t *testing.T) {
	edges := &fakeEdgeLinkLister{err: errors.New("boom")}
	service := edgeService(edges, &fakeReferenceLister{})

	results := service.Resolve(context.Background(), Request{UserID: 7, Slots: []Slot{linksSlot(10)}})

	if results["graph"].Status != "error" || results["graph"].Error == nil {
		t.Fatalf("result = %+v, want a per-slot error", results["graph"])
	}
}

func TestResolveLinksWithoutEdgeSupport(t *testing.T) {
	service := edgeService(nil, nil)

	results := service.Resolve(context.Background(), Request{UserID: 7, Slots: []Slot{linksSlot(10)}})

	if results["graph"].Status != "error" {
		t.Fatalf("status = %q, want error", results["graph"].Status)
	}
}

func TestNormalizeLinksSlot(t *testing.T) {
	t.Run("applies the edge default and caps the seed limits", func(t *testing.T) {
		slot, err := NormalizeSlot(Slot{
			Key:    "graph",
			Entity: EntityLinks,
			Links: &LinksSpec{
				Item:  &QueryPart{QL: ""},
				Asset: &QueryPart{QL: "", AssetSetID: 3, Limit: 5000},
			},
		})
		if err != nil {
			t.Fatalf("NormalizeSlot: %v", err)
		}
		if slot.Links.MaxEdges != MaxEdges {
			t.Fatalf("max_edges = %d, want default %d", slot.Links.MaxEdges, MaxEdges)
		}
		if slot.Links.Item.Limit != MaxNodesPerSide {
			t.Fatalf("item limit = %d, want the node cap %d", slot.Links.Item.Limit, MaxNodesPerSide)
		}
		if slot.Links.Asset.Limit != MaxNodesPerSide {
			t.Fatalf("asset limit = %d, want the node cap %d", slot.Links.Asset.Limit, MaxNodesPerSide)
		}
	})

	invalid := []struct {
		name string
		slot Slot
		want string
	}{
		{name: "no links spec", slot: Slot{Key: "g", Entity: EntityLinks}, want: "requires both"},
		{
			name: "missing asset seed",
			slot: Slot{Key: "g", Entity: EntityLinks, Links: &LinksSpec{Item: &QueryPart{}}},
			want: "requires both",
		},
		{
			name: "asset seed without a set",
			slot: Slot{Key: "g", Entity: EntityLinks, Links: &LinksSpec{Item: &QueryPart{}, Asset: &QueryPart{}}},
			want: "asset_set_id",
		},
		{
			name: "edge cap above the limit",
			slot: Slot{Key: "g", Entity: EntityLinks, Links: &LinksSpec{
				Item: &QueryPart{}, Asset: &QueryPart{AssetSetID: 1}, MaxEdges: MaxEdges + 1,
			}},
			want: "max_edges",
		},
		{
			name: "unparseable seed query",
			slot: Slot{Key: "g", Entity: EntityLinks, Links: &LinksSpec{
				Item: &QueryPart{QL: "status = "}, Asset: &QueryPart{AssetSetID: 1},
			}},
			want: "ql",
		},
	}

	for _, test := range invalid {
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
