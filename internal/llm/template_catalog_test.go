//go:build test

package llm

import (
	"context"
	"testing"

	"windshift/internal/models"
)

// fakeCatalogOverrides is an in-memory AgentTemplateOverrides source used to
// exercise the merge logic without a database.
type fakeCatalogOverrides struct {
	entries []*models.AgentTemplateCatalogEntry
}

func (f *fakeCatalogOverrides) List(context.Context) ([]*models.AgentTemplateCatalogEntry, error) {
	return f.entries, nil
}

func catalogEntry(key, name string, typ models.AgentProfileType, instructions string, enabled bool) *models.AgentTemplateCatalogEntry {
	return &models.AgentTemplateCatalogEntry{
		TemplateKey:  key,
		Name:         name,
		DefaultType:  typ,
		Instructions: instructions,
		Enabled:      enabled,
	}
}

func TestTemplateCatalogOverridesEmbeddedDefaults(t *testing.T) {
	store := NewPromptStore("")
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: []*models.AgentTemplateCatalogEntry{
		catalogEntry("workspace_guide", "Custom Guide", models.AgentProfileStandard, "custom guide instructions", true),
	}})

	templates := catalog.AgentTemplates()
	var matched *AgentTemplate
	for i := range templates {
		if templates[i].Key == "workspace_guide" {
			matched = &templates[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("workspace_guide not present in merged catalog")
	}
	if matched.Name != "Custom Guide" {
		t.Fatalf("name = %q, want override %q", matched.Name, "Custom Guide")
	}
	if matched.Instructions != "custom guide instructions" {
		t.Fatalf("instructions = %q, want override %q", matched.Instructions, "custom guide instructions")
	}
}

func TestTemplateCatalogDisabledOverrideSuppressesDefault(t *testing.T) {
	store := NewPromptStore("")
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: []*models.AgentTemplateCatalogEntry{
		catalogEntry("software_engineer", "Disabled Name", models.AgentProfileCoding, "ignored instructions", false),
	}})

	templates := catalog.AgentTemplates()
	for i := range templates {
		if templates[i].Key == "software_engineer" {
			t.Fatalf("disabled embedded template remained in merged catalog: %+v", templates[i])
		}
	}
	if _, ok := catalog.AgentTemplate("software_engineer"); ok {
		t.Fatal("disabled embedded template resolved by key")
	}
}

func TestTemplateCatalogDisabledCustomTemplateIsOmitted(t *testing.T) {
	store := NewPromptStore("")
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: []*models.AgentTemplateCatalogEntry{
		catalogEntry("custom_agent", "Custom Agent", models.AgentProfileStandard, "custom instructions", false),
	}})

	if _, ok := catalog.AgentTemplate("custom_agent"); ok {
		t.Fatal("disabled custom template resolved by key")
	}
}

func TestTemplateCatalogBlankOverrideFieldFallsBackToDefault(t *testing.T) {
	store := NewPromptStore("")
	// Override only the instructions; name and default_type stay embedded.
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: []*models.AgentTemplateCatalogEntry{
		catalogEntry("code_reviewer", "", "", "custom reviewer instructions", true),
	}})

	templates := catalog.AgentTemplates()
	for i := range templates {
		if templates[i].Key == "code_reviewer" {
			if templates[i].Name != "Code Reviewer" {
				t.Fatalf("blank override clobbered name = %q, want default %q", templates[i].Name, "Code Reviewer")
			}
			if templates[i].DefaultType != models.AgentProfileCoding {
				t.Fatalf("blank override clobbered type = %q, want coding", templates[i].DefaultType)
			}
			if templates[i].Instructions != "custom reviewer instructions" {
				t.Fatalf("instructions = %q, want override %q", templates[i].Instructions, "custom reviewer instructions")
			}
			return
		}
	}
	t.Fatalf("code_reviewer not present in merged catalog")
}

func TestTemplateCatalogAppendsNewKey(t *testing.T) {
	store := NewPromptStore("")
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: []*models.AgentTemplateCatalogEntry{
		catalogEntry("custom_agent", "Custom Agent", models.AgentProfileStandard, "custom instructions", true),
	}})

	templates := catalog.AgentTemplates()
	var matched *AgentTemplate
	for i := range templates {
		if templates[i].Key == "custom_agent" {
			matched = &templates[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("custom_agent (new key) not appended to catalog")
	}
	if matched.Name != "Custom Agent" {
		t.Fatalf("name = %q, want %q", matched.Name, "Custom Agent")
	}
	if matched.DefaultType != models.AgentProfileStandard {
		t.Fatalf("default_type = %q, want standard", matched.DefaultType)
	}
}

func TestTemplateCatalogAgentTemplateResolvesOverride(t *testing.T) {
	store := NewPromptStore("")
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: []*models.AgentTemplateCatalogEntry{
		catalogEntry("release_manager", "Custom Release", models.AgentProfileStandard, "release override", true),
	}})

	tt, ok := catalog.AgentTemplate("release_manager")
	if !ok {
		t.Fatalf("release_manager not resolved from merged catalog")
	}
	if tt.Name != "Custom Release" || tt.Instructions != "release override" {
		t.Fatalf("resolved template = %+v, want override", tt)
	}
}

func TestTemplateCatalogAgentTemplateUnknownKey(t *testing.T) {
	store := NewPromptStore("")
	catalog := NewTemplateCatalog(store, &fakeCatalogOverrides{entries: nil})
	if _, ok := catalog.AgentTemplate("does_not_exist"); ok {
		t.Fatalf("unknown key resolved from catalog")
	}
}
