//go:build test

package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func TestBuiltinPacks_ShippedPacksParse(t *testing.T) {
	if err := ValidateBuiltinPacks(); err != nil {
		t.Fatalf("ValidateBuiltinPacks() = %v, want nil", err)
	}

	summaries, err := BuiltinPacks()
	if err != nil {
		t.Fatalf("BuiltinPacks() error = %v", err)
	}
	var helpdesk *BuiltinPackSummary
	for i := range summaries {
		if summaries[i].Name == "helpdesk" {
			helpdesk = &summaries[i]
			break
		}
	}
	if helpdesk == nil {
		t.Fatalf("helpdesk pack missing from %+v", summaries)
	}
	if helpdesk.Version != "1.1.2" {
		t.Fatalf("helpdesk version = %q, want 1.1.2", helpdesk.Version)
	}
	if !helpdesk.HasContent {
		t.Fatalf("helpdesk pack must declare a content bundle: %+v", helpdesk)
	}
	if len(helpdesk.Plugins) != 0 {
		t.Fatalf("helpdesk pack must not require plugins: %+v", helpdesk.Plugins)
	}
}

func TestBuiltinPackArchive_HelpdeskDocumentsParse(t *testing.T) {
	archive, err := BuiltinPackArchive("helpdesk")
	if err != nil {
		t.Fatalf("BuiltinPackArchive(helpdesk) error = %v", err)
	}
	if archive.Manifest.Schema.ConfigurationSet == "" {
		t.Fatal("helpdesk manifest must declare a configuration-set file")
	}
	template, err := archive.ConfigurationSetTemplate()
	if err != nil {
		t.Fatalf("ConfigurationSetTemplate() error = %v", err)
	}
	if template.Payload.ConfigurationSet.Name != "Helpdesk" {
		t.Fatalf("configuration set name = %q, want Helpdesk", template.Payload.ConfigurationSet.Name)
	}
	if archive.Manifest.Conformance == nil ||
		archive.Manifest.Conformance.ConfigurationSet != archive.Manifest.Schema.ConfigurationSet {
		t.Fatalf("helpdesk conformance must point at its schema file: %+v", archive.Manifest.Conformance)
	}
	bundle, declared, err := archive.WorkspaceBundleFile()
	if err != nil {
		t.Fatalf("WorkspaceBundleFile() error = %v", err)
	}
	if !declared {
		t.Fatal("helpdesk manifest must declare a content bundle")
	}
	payload, ok := bundle["payload"].(map[string]any)
	if !ok {
		t.Fatalf("helpdesk bundle has no payload: %+v", bundle)
	}
	viewSettings, ok := payload["view_settings"].(map[string]any)
	if !ok {
		t.Fatalf("helpdesk bundle must declare view_settings: %+v", payload)
	}
	enabled, _ := viewSettings["enabled_views"].([]any)
	enabledSet := map[string]bool{}
	for _, id := range enabled {
		if name, ok := id.(string); ok {
			enabledSet[name] = true
		}
	}
	if !enabledSet["queue"] {
		t.Fatalf("helpdesk view_settings must enable the queue: %+v", enabled)
	}
	for _, disabled := range []string{"backlog", "tree", "map", "roadmap", "iterations", "milestones"} {
		if enabledSet[disabled] {
			t.Fatalf("helpdesk view_settings must disable %q: %+v", disabled, enabled)
		}
	}
	slas, ok := payload["slas"].(map[string]any)
	if !ok {
		t.Fatalf("helpdesk bundle must declare slas: %+v", payload)
	}
	if metrics, _ := slas["metrics"].([]any); len(metrics) == 0 {
		t.Fatalf("helpdesk bundle must declare SLA metrics: %+v", slas)
	} else {
		// First response is the first public agent reply, not an assignment:
		// a ticket answered without ever being assigned must be satisfied.
		stop := ""
		for _, raw := range metrics {
			metric, _ := raw.(map[string]any)
			if metric["name"] != "First Response" {
				continue
			}
			conditions, _ := metric["conditions"].([]any)
			for _, rawCondition := range conditions {
				condition, _ := rawCondition.(map[string]any)
				if condition["phase"] == "stop" {
					stop, _ = condition["condition_type"].(string)
				}
			}
		}
		if stop != "comment_by_agent" {
			t.Fatalf("helpdesk First Response stop condition = %q, want comment_by_agent", stop)
		}
	}
	if calendars, _ := slas["calendars"].([]any); len(calendars) == 0 {
		t.Fatalf("helpdesk bundle must declare SLA calendars: %+v", slas)
	}
}

func TestBuiltinPackArchive_UnknownName(t *testing.T) {
	_, err := BuiltinPackArchive("does-not-exist")
	if !errors.Is(err, ErrBuiltinPackNotFound) {
		t.Fatalf("error = %v, want ErrBuiltinPackNotFound", err)
	}
}

// TestBuiltinHelpdeskTemplateRoundTripsConformant imports the shipped helpdesk
// schema and exports it again: the live export must diff clean against the
// template. This is what makes a built-in pack self-verifying.
func TestBuiltinHelpdeskTemplateRoundTripsConformant(t *testing.T) {
	dsn := fmt.Sprintf("file:builtin-helpdesk-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	archive, err := BuiltinPackArchive("helpdesk")
	if err != nil {
		t.Fatalf("BuiltinPackArchive: %v", err)
	}
	template, err := archive.ConfigurationSetTemplate()
	if err != nil {
		t.Fatalf("ConfigurationSetTemplate: %v", err)
	}

	repo := repository.NewConfigurationSetRepository(db)
	id, warnings, err := NewConfigSetImportService(db, repo).Import(context.Background(), template)
	if err != nil {
		t.Fatalf("import helpdesk template: %v", err)
	}
	if len(warnings) != 0 {
		t.Logf("import warnings: %v", warnings)
	}

	live, err := NewConfigSetExportService(db, repo).Export(context.Background(), id, nil)
	if err != nil {
		t.Fatalf("export helpdesk config set: %v", err)
	}
	drifts := DiffConformanceTemplates(template, live)
	if len(drifts) != 0 {
		for _, d := range drifts {
			t.Logf("drift: %s — %s", d.ID, d.Detail)
		}
		t.Fatalf("helpdesk schema round-trip drifted %d rows (live: %d statuses, %d workflows, %d screens, %d fields, %d priorities, %d item types)",
			len(drifts), len(live.Payload.Statuses), len(live.Payload.Workflows), len(live.Payload.Screens),
			len(live.Payload.CustomFields), len(live.Payload.Priorities), len(live.Payload.ItemTypes))
	}
}
