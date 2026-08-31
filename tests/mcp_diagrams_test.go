// mcp_diagrams_test covers the new diagram MCP surface (untracked file
// internal/aitools/diagrams.go that motivated this whole pass): list_diagrams,
// get_diagram, create_diagram, update_diagram, delete_diagram. Mermaid input
// is checked end-to-end including the {type:mermaid,source} seed wrapper.
package tests

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCP_Diagrams_RoundTrip(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)
	session := dialMCP(t, ts)

	target := w.Items[0]

	// 1. Create a mermaid diagram.
	var created struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	callTool(t, session, "create_diagram", map[string]interface{}{
		"item_id": target.ID,
		"name":    "Auth flow",
		"mermaid": "graph TD; A-->B; B-->C",
	}, &created)
	if created.ID == 0 {
		t.Fatalf("create_diagram returned no id: %+v", created)
	}
	if created.Kind != "mermaid" {
		t.Fatalf("create_diagram kind=%q want \"mermaid\"", created.Kind)
	}

	// 2. List diagrams on the item — should include the just-created one.
	var listed struct {
		Diagrams []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"diagrams"`
	}
	callTool(t, session, "list_diagrams", map[string]interface{}{"item_id": target.ID}, &listed)
	found := false
	for _, d := range listed.Diagrams {
		if d.ID == created.ID && d.Name == "Auth flow" && d.Kind == "mermaid" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("list_diagrams missing created diagram: %+v", listed.Diagrams)
	}

	// 3. Get the diagram and verify the seed wrapper was stored verbatim.
	var fetched struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		DiagramData string `json:"diagram_data"`
	}
	callTool(t, session, "get_diagram", map[string]interface{}{"id": created.ID}, &fetched)
	if fetched.Kind != "mermaid" {
		t.Fatalf("get_diagram kind=%q want mermaid", fetched.Kind)
	}
	var wrapper struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(fetched.DiagramData), &wrapper); err != nil {
		t.Fatalf("diagram_data not JSON: %v\nraw=%s", err, fetched.DiagramData)
	}
	if wrapper.Type != "mermaid" || !strings.Contains(wrapper.Source, "graph TD") {
		t.Fatalf("seed wrapper %+v doesn't match expected mermaid source", wrapper)
	}

	// 4. Update — replace with an excalidraw scene this time.
	scene := json.RawMessage(`{"elements":[{"type":"rectangle","id":"r1"}],"appState":{}}`)
	callTool(t, session, "update_diagram", map[string]interface{}{
		"id":         created.ID,
		"name":       "Updated name",
		"excalidraw": scene,
	}, nil)
	callTool(t, session, "get_diagram", map[string]interface{}{"id": created.ID}, &fetched)
	if fetched.Kind != "excalidraw" || fetched.Name != "Updated name" {
		t.Fatalf("after update: got kind=%q name=%q want excalidraw / Updated name", fetched.Kind, fetched.Name)
	}

	// 5. Delete and confirm gone.
	callTool(t, session, "delete_diagram", map[string]interface{}{"id": created.ID}, nil)
	listed.Diagrams = nil
	callTool(t, session, "list_diagrams", map[string]interface{}{"item_id": target.ID}, &listed)
	for _, d := range listed.Diagrams {
		if d.ID == created.ID {
			t.Fatalf("diagram %d still present after delete", created.ID)
		}
	}
}
