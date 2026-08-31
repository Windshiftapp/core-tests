package wscli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// captureStdout swaps the package-level stdout for a buffer, runs fn,
// then restores. Mirrors the snapshot pattern in root.go's Run.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := stdout
	var buf bytes.Buffer
	stdout = &buf
	defer func() { stdout = prev }()
	fn()
	return buf.String()
}

func TestOutput_Table_RendersPages(t *testing.T) {
	pages := []Page{
		{ID: 1, Title: "Root", Slug: "root", Depth: 0, UpdatedAt: time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)},
		{ID: 2, Title: "Child", Slug: "child", Depth: 1, UpdatedAt: time.Date(2026, 5, 22, 10, 1, 0, 0, time.UTC)},
	}
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(pages)
	})
	for _, want := range []string{"ID", "TITLE", "SLUG", "UPDATED", "Root", "Child", "child"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n----\n%s", want, out)
		}
	}
	// Regression for #3: must not fall through to JSON.
	if strings.Contains(out, "{") || strings.Contains(out, "\"title\"") {
		t.Errorf("expected table output, got JSON fallback:\n%s", out)
	}
}

func TestOutput_Table_RendersPageDetail(t *testing.T) {
	page := &Page{
		ID: 1, Title: "Onboarding", Slug: "onboarding",
		Depth: 2, Path: "1/4/", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Labels: []PageLabel{{Name: "design"}, {Name: "spec"}},
	}
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(page)
	})
	for _, want := range []string{"ID:", "Title:", "Onboarding", "Slug:", "design", "spec"} {
		if !strings.Contains(out, want) {
			t.Errorf("page detail table missing %q\n----\n%s", want, out)
		}
	}
}

func TestOutput_Table_RendersPageLabels(t *testing.T) {
	labels := []PageLabel{
		{ID: 5, Name: "design", Color: "#3B82F6"},
		{ID: 7, Name: "urgent", Color: "#EF4444"},
	}
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(labels)
	})
	for _, want := range []string{"ID", "NAME", "COLOR", "design", "urgent", "#3B82F6"} {
		if !strings.Contains(out, want) {
			t.Errorf("labels table missing %q\n----\n%s", want, out)
		}
	}
}

func TestOutput_Table_RendersPageRevisions(t *testing.T) {
	revs := []PageRevision{
		{RevisionNumber: 2, ChangeType: "update", CreatedBy: 42, CreatedAt: time.Now()},
		{RevisionNumber: 1, ChangeType: "create", CreatedBy: 42, CreatedAt: time.Now()},
	}
	out := captureStdout(t, func() {
		(&Output{format: "table"}).Print(revs)
	})
	for _, want := range []string{"REVISION", "CHANGE_TYPE", "AUTHOR", "update", "create"} {
		if !strings.Contains(out, want) {
			t.Errorf("revisions table missing %q\n----\n%s", want, out)
		}
	}
}

func TestOutput_CSV_RendersPagesAndLabels(t *testing.T) {
	pages := []Page{{ID: 1, Title: "Root", Slug: "root", UpdatedAt: time.Now()}}
	out := captureStdout(t, func() {
		(&Output{format: "csv"}).Print(pages)
	})
	if !strings.HasPrefix(out, "ID,TITLE,SLUG") {
		t.Errorf("pages CSV missing header row:\n%s", out)
	}
	if !strings.Contains(out, "Root") {
		t.Errorf("pages CSV missing data row:\n%s", out)
	}

	labels := []PageLabel{{ID: 5, Name: "design", Color: "#3B82F6", WorkspaceID: 1}}
	out = captureStdout(t, func() {
		(&Output{format: "csv"}).Print(labels)
	})
	if !strings.HasPrefix(out, "ID,NAME,COLOR") {
		t.Errorf("labels CSV missing header row:\n%s", out)
	}
	if !strings.Contains(out, "design") {
		t.Errorf("labels CSV missing data row:\n%s", out)
	}

	revs := []PageRevision{{RevisionNumber: 1, PageID: 42, ChangeType: "create", CreatedBy: 1, Title: "Onboarding", CreatedAt: time.Now()}}
	out = captureStdout(t, func() {
		(&Output{format: "csv"}).Print(revs)
	})
	if !strings.HasPrefix(out, "REVISION,PAGE_ID,CHANGE_TYPE") {
		t.Errorf("revisions CSV missing header row:\n%s", out)
	}
	if !strings.Contains(out, "Onboarding") {
		t.Errorf("revisions CSV missing data row:\n%s", out)
	}
}
