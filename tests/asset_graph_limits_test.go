package tests

import (
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/models"
)

func TestAssetGraphLimitsWideFirstHop(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	setID, typeID := seedAssetSetAndType(t, server, "graph-limit")
	var origin int
	for i := 0; i <= 300; i++ {
		response := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/asset-sets/%d/assets", setID), map[string]any{"title": fmt.Sprintf("Graph asset %03d", i), "asset_type_id": typeID})
		AssertStatusCode(t, response, http.StatusCreated)
		var asset models.Asset
		DecodeJSON(t, response, &asset)
		response.Body.Close()
		if i == 0 {
			origin = asset.ID
			continue
		}
		linked := MakeAuthRequest(t, server, http.MethodPost, fmt.Sprintf("/v2/assets/%d/links", origin), map[string]any{"link_type_id": 4, "target_type": "asset", "target_id": asset.ID})
		AssertStatusCode(t, linked, http.StatusCreated)
		linked.Body.Close()
	}
	response := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/v2/assets/%d/relationship-graph", origin), nil)
	defer response.Body.Close()
	AssertStatusCode(t, response, http.StatusOK)
	var graph models.RelationshipGraphResponse
	DecodeJSON(t, response, &graph)
	if len(graph.Nodes) != 51 || len(graph.Edges) != 100 || !graph.Truncated {
		t.Fatalf("graph nodes=%d edges=%d truncated=%v; want 51, 100, true", len(graph.Nodes), len(graph.Edges), graph.Truncated)
	}
	for _, node := range graph.Nodes {
		if node.Hop > 1 {
			t.Fatalf("unexpected hop %d for %s", node.Hop, node.ID)
		}
	}
}
