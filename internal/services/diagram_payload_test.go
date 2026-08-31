//go:build test

package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBuildDiagramPayload(t *testing.T) {
	t.Run("Mermaid seed", func(t *testing.T) {
		data, kind, err := BuildDiagramPayload(" graph TD; A-->B ", nil)
		if err != nil {
			t.Fatalf("build Mermaid payload: %v", err)
		}
		if kind != DiagramKindMermaid {
			t.Fatalf("kind = %q, want %q", kind, DiagramKindMermaid)
		}
		var seed map[string]string
		if err := json.Unmarshal([]byte(data), &seed); err != nil {
			t.Fatalf("decode seed: %v", err)
		}
		if seed["type"] != DiagramKindMermaid || seed["source"] != "graph TD; A-->B" {
			t.Fatalf("seed = %#v", seed)
		}
	})

	t.Run("Excalidraw scene", func(t *testing.T) {
		scene := json.RawMessage(`{"type":"excalidraw","elements":[{"id":"one","type":"rectangle"}],"appState":{},"files":{}}`)
		data, kind, err := BuildDiagramPayload("", scene)
		if err != nil {
			t.Fatalf("build Excalidraw payload: %v", err)
		}
		if kind != DiagramKindExcalidraw || data != string(scene) {
			t.Fatalf("data/kind = %s/%s", data, kind)
		}
	})

	t.Run("Mutually exclusive", func(t *testing.T) {
		_, _, err := BuildDiagramPayload("graph TD; A-->B", json.RawMessage(`{"elements":[]}`))
		if !errors.Is(err, ErrDiagramPayloadConflict) {
			t.Fatalf("error = %v, want ErrDiagramPayloadConflict", err)
		}
	})
}

func TestValidateExcalidrawScene(t *testing.T) {
	tests := []struct {
		name string
		data string
		err  error
	}{
		{name: "minimal", data: `{"elements":[]}`},
		{name: "complete", data: `{"elements":[{"id":"one","type":"text","text":"hello"}],"appState":{},"files":{}}`},
		{name: "not object", data: `[]`, err: ErrDiagramPayloadInvalid},
		{name: "missing elements", data: `{"appState":{}}`, err: ErrDiagramPayloadInvalid},
		{name: "elements not array", data: `{"elements":{}}`, err: ErrDiagramPayloadInvalid},
		{name: "element missing id", data: `{"elements":[{"type":"text"}]}`, err: ErrDiagramPayloadInvalid},
		{name: "app state not object", data: `{"elements":[],"appState":[]}`, err: ErrDiagramPayloadInvalid},
		{name: "files not object", data: `{"elements":[],"files":[]}`, err: ErrDiagramPayloadInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExcalidrawScene([]byte(tt.data))
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestValidateExcalidrawScene_Bounds(t *testing.T) {
	oversized := `{"elements":[],"padding":"` + strings.Repeat("x", MaxDiagramPayloadBytes) + `"}`
	if err := ValidateExcalidrawScene([]byte(oversized)); !errors.Is(err, ErrDiagramPayloadTooLarge) {
		t.Fatalf("oversized error = %v, want ErrDiagramPayloadTooLarge", err)
	}
}
