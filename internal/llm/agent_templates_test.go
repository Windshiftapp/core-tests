package llm

import (
	"os"
	"path/filepath"
	"testing"

	"windshift/internal/models"
)

func TestPromptStoreAgentTemplatesExposeApprovedCatalogAndOverrides(t *testing.T) {
	overrideDir := t.TempDir()
	override := "Use the workspace-specific guide policy."
	if err := os.WriteFile(filepath.Join(overrideDir, PromptAgentWorkspaceGuide+".txt"), []byte(override), 0o600); err != nil {
		t.Fatalf("write prompt override: %v", err)
	}
	store := NewPromptStore(overrideDir)
	templates := store.AgentTemplates()

	wantNames := []string{
		"Workspace Guide",
		"Work-item Triage",
		"Delivery Coordinator",
		"Software Engineer",
		"Code Reviewer",
		"QA / Test Engineer",
		"Release Manager",
		"Blank",
	}
	if len(templates) != len(wantNames) {
		t.Fatalf("template count = %d, want %d", len(templates), len(wantNames))
	}
	for i, want := range wantNames {
		if templates[i].Name != want {
			t.Fatalf("template[%d].name = %q, want %q", i, templates[i].Name, want)
		}
		if i != len(templates)-1 && templates[i].Instructions == "" {
			t.Fatalf("template[%d] has empty instructions", i)
		}
	}
	if templates[0].Instructions != override {
		t.Fatalf("Workspace Guide instructions = %q, want override %q", templates[0].Instructions, override)
	}
	if templates[3].DefaultType != models.AgentProfileCoding ||
		templates[4].DefaultType != models.AgentProfileCoding {
		t.Fatalf("Coding template defaults changed: software=%q review=%q", templates[3].DefaultType, templates[4].DefaultType)
	}
	if templates[0].DefaultType != models.AgentProfileStandard ||
		templates[7].DefaultType != models.AgentProfileStandard {
		t.Fatalf("Standard template defaults changed: guide=%q blank=%q", templates[0].DefaultType, templates[7].DefaultType)
	}
}
