// wscli_diagram_test exercises `ws diagram ...` end-to-end against an
// isolated test server. The CLI talks to the v1 surface
// (/rest/api/v1/items/{id}/diagrams, /rest/api/v1/diagrams/{id}) per
// WI-78; this test guards against a future regression that repoints
// `ws diagram` back to the cookie surface (where bearer tokens are
// rejected and the command silently 401s).
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"windshift/internal/wscli"
)

func TestWSCLI_Diagram_CRUDLifecycle(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, ts)
	w := SeedWorld(t, ts)

	// Pick the first seeded item in Alpha. Tests resolve through the CLI
	// (workspace key + workspace_item_number), not the numeric ID — that
	// exercises the same ResolveItemID path real callers hit and makes the
	// test less brittle if the underlying ID ever drifts.
	target := w.Items[0]

	// 1. Create with a mermaid seed.
	out, stderr, code := runWS(t, ts,
		"diagram", "create", target.Key,
		"--name", "Auth flow",
		"--mermaid", "graph TD; A-->B",
		"-o", "json",
	)
	requireZero(t, code, stderr)

	created := decodeDiagram(t, out)
	if created.ID == 0 {
		t.Fatalf("create: id is zero, body=%s", string(out))
	}
	if created.Name != "Auth flow" {
		t.Fatalf("create: name = %q, want %q", created.Name, "Auth flow")
	}
	// The CLI wraps mermaid sources in the {"type":"mermaid","source":...}
	// seed envelope the frontend expands; decode and check the source field
	// directly (rather than substring-matching, which fails on the JSON
	// encoder's > escape for `>`).
	if got := mermaidSeedSource(t, created.DiagramData); got != "graph TD; A-->B" {
		t.Fatalf("create: mermaid source = %q, want %q (full payload=%s)", got, "graph TD; A-->B", created.DiagramData)
	}

	// 2. List returns the created diagram.
	out, stderr, code = runWS(t, ts,
		"diagram", "list", target.Key,
		"-o", "json",
	)
	requireZero(t, code, stderr)
	listed := decodeDiagrams(t, out)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list: got %v diagrams, want exactly one with id=%d", listed, created.ID)
	}

	// 3. Get by ID matches the create response.
	out, stderr, code = runWS(t, ts,
		"diagram", "get", strconv.Itoa(created.ID),
		"-o", "json",
	)
	requireZero(t, code, stderr)
	fetched := decodeDiagram(t, out)
	if fetched.ID != created.ID || fetched.Name != created.Name {
		t.Fatalf("get: got %+v, want id=%d name=%q", fetched, created.ID, created.Name)
	}

	// 4. Update the name; payload preserved when only --name is passed.
	out, stderr, code = runWS(t, ts,
		"diagram", "update", strconv.Itoa(created.ID),
		"--name", "Auth flow v2",
		"-o", "json",
	)
	requireZero(t, code, stderr)
	updated := decodeDiagram(t, out)
	if updated.Name != "Auth flow v2" {
		t.Fatalf("update: name = %q, want %q", updated.Name, "Auth flow v2")
	}
	if updated.DiagramData != created.DiagramData {
		t.Fatalf("update: diagram_data changed without --mermaid/--excalidraw/--from-file:\n  before: %q\n  after:  %q",
			created.DiagramData, updated.DiagramData)
	}

	// 5. Delete then list — empty.
	_, stderr, code = runWS(t, ts,
		"diagram", "delete", strconv.Itoa(created.ID),
		"-o", "json",
	)
	requireZero(t, code, stderr)

	out, stderr, code = runWS(t, ts,
		"diagram", "list", target.Key,
		"-o", "json",
	)
	requireZero(t, code, stderr)
	listed = decodeDiagrams(t, out)
	if len(listed) != 0 {
		t.Fatalf("list after delete: got %d diagrams, want 0", len(listed))
	}

	// 6. Get-after-delete returns 404; ws should surface a non-zero exit.
	var so, se bytes.Buffer
	code = wscli.Run(
		context.Background(),
		[]string{"diagram", "get", strconv.Itoa(created.ID), "-o", "json"},
		nil, &so, &se,
		map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": ts.BearerToken},
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit for deleted diagram, got 0; stdout=%s stderr=%s", so.String(), se.String())
	}
}

// decodeDiagram decodes a single diagram from the CLI's JSON output. The CLI
// prints the raw `models.ItemDiagram` shape (id, item_id, name, diagram_data,
// timestamps, creator_*).
func decodeDiagram(t *testing.T, out []byte) diagramJSON {
	t.Helper()
	var d diagramJSON
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("decode diagram: %v\nraw=%s", err, string(out))
	}
	return d
}

// decodeDiagrams decodes the JSON array `ws diagram list` prints. The CLI
// unwraps the v1 `{"items":[...]}` envelope before printing, so the on-wire
// output is a bare array.
func decodeDiagrams(t *testing.T, out []byte) []diagramJSON {
	t.Helper()
	// The CLI may print `null` for an empty slice in some marshalers; treat
	// that as zero elements so the test stays readable.
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "null" {
		return nil
	}
	var ds []diagramJSON
	if err := json.Unmarshal(out, &ds); err != nil {
		t.Fatalf("decode diagrams list: %v\nraw=%s", err, string(out))
	}
	return ds
}

type diagramJSON struct {
	ID          int    `json:"id"`
	ItemID      int    `json:"item_id"`
	Name        string `json:"name"`
	DiagramData string `json:"diagram_data"`
}

// mermaidSeedSource extracts the original mermaid source from the seed
// envelope the CLI persists: `{"type":"mermaid","source":"..."}`. Fails
// the test if the payload isn't a mermaid seed.
func mermaidSeedSource(t *testing.T, payload string) string {
	t.Helper()
	var seed struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(payload), &seed); err != nil {
		t.Fatalf("decode mermaid seed: %v (payload=%s)", err, payload)
	}
	if seed.Type != "mermaid" {
		t.Fatalf("seed type = %q, want mermaid (payload=%s)", seed.Type, payload)
	}
	return seed.Source
}
