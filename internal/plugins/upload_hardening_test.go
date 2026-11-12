//go:build !noplugins

package plugins

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type zipEntry struct {
	name string
	data []byte
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, e := range entries {
		entry, err := writer.Create(e.name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", e.name, err)
		}
		if _, err := entry.Write(e.data); err != nil {
			t.Fatalf("write zip entry %s: %v", e.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func manifestFor(name, version string) []byte {
	return []byte(`{"name":"` + name + `","version":"` + version + `","entryPoint":"plugin.wasm"}`)
}

func TestUploadRejectsMultipleManifests(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	zipBytes := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "1.0.0")},
		{"nested/manifest.json", manifestFor("other", "9.9.9")},
		{"plugin.wasm", minimalCapabilityModule()},
	})

	err := m.UploadPluginWithLicense("", zipBytes, nil)
	if err == nil {
		t.Fatal("expected upload to fail for multiple manifests")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("error = %v, want an exactly-one-manifest diagnostic", err)
	}
	assertNoPluginDirs(t, root)
}

func TestUploadRejectsNameMismatch(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	zipBytes := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("other", "1.0.0")},
		{"plugin.wasm", minimalCapabilityModule()},
	})

	if err := m.UploadPluginWithLicense("requested", zipBytes, nil); err == nil {
		t.Fatal("expected upload to fail when the requested name disagrees with the manifest")
	}
	assertNoPluginDirs(t, root)
}

func TestUploadRejectsEntryOutsideRoot(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	zipBytes := buildZip(t, []zipEntry{
		{"foo/manifest.json", manifestFor("foo", "1.0.0")},
		{"bar/plugin.wasm", minimalCapabilityModule()},
	})

	err := m.UploadPluginWithLicense("", zipBytes, nil)
	if err == nil {
		t.Fatal("expected upload to fail for an entry outside the archive root")
	}
	if !strings.Contains(err.Error(), "outside the plugin root") {
		t.Fatalf("error = %v, want an outside-root diagnostic", err)
	}
	assertNoPluginDirs(t, root)
}

func TestUploadRejectsTraversalEntry(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	zipBytes := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "1.0.0")},
		{"plugin.wasm", minimalCapabilityModule()},
		{"../escaped.txt", []byte("nope")},
	})

	if err := m.UploadPluginWithLicense("", zipBytes, nil); err == nil {
		t.Fatal("expected upload to fail for a traversal entry")
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escaped.txt")); err == nil {
		t.Fatal("traversal entry was written outside the plugin directory")
	}
	assertNoPluginDirs(t, root)
}

func TestUploadFailurePreservesPreviousInstall(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	v1 := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "1.0.0")},
		{"plugin.wasm", minimalCapabilityModule()},
	})
	if err := m.UploadPluginWithLicense("", v1, nil); err != nil {
		t.Fatalf("upload v1: %v", err)
	}

	// v2 names an entry point that the archive does not contain, so it must
	// fail before promotion and leave v1 intact.
	v2 := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "2.0.0")},
		{"other.wasm", minimalCapabilityModule()},
	})
	if err := m.UploadPluginWithLicense("", v2, nil); err == nil {
		t.Fatal("expected upload of a broken archive to fail")
	}

	plugin, ok := m.GetPlugin("demo")
	if !ok {
		t.Fatal("plugin disappeared after a failed upgrade")
	}
	if plugin.Manifest.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0 after a failed upgrade", plugin.Manifest.Version)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "manifest.json")); err != nil {
		t.Fatalf("previous install manifest missing: %v", err)
	}
}

func TestUploadReplacesAndRemovesStaleFiles(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	v1 := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "1.0.0")},
		{"plugin.wasm", minimalCapabilityModule()},
		{"assets/extra.js", []byte("stale")},
	})
	if err := m.UploadPluginWithLicense("", v1, nil); err != nil {
		t.Fatalf("upload v1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "assets", "extra.js")); err != nil {
		t.Fatalf("v1 asset missing after upload: %v", err)
	}

	v2 := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "2.0.0")},
		{"plugin.wasm", minimalCapabilityModule()},
	})
	if err := m.UploadPluginWithLicense("", v2, nil); err != nil {
		t.Fatalf("upload v2: %v", err)
	}

	plugin, ok := m.GetPlugin("demo")
	if !ok {
		t.Fatal("plugin missing after upgrade")
	}
	if plugin.Manifest.Version != "2.0.0" {
		t.Fatalf("version = %q, want 2.0.0", plugin.Manifest.Version)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "assets", "extra.js")); !os.IsNotExist(err) {
		t.Fatalf("stale asset survived the upgrade (err = %v)", err)
	}
}

func TestUploadReplacementDropsRemovedSchedules(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	t.Cleanup(func() { _ = m.Close() })

	v1 := buildZip(t, []zipEntry{
		{"manifest.json", []byte(`{
			"name": "demo",
			"version": "1.0.0",
			"entryPoint": "plugin.wasm",
			"schedules": [{"id": "tick", "every": "1h", "handler": "tick"}]
		}`)},
		{"plugin.wasm", minimalCapabilityModule()},
	})
	if err := m.UploadPluginWithLicense("", v1, nil); err != nil {
		t.Fatalf("upload v1: %v", err)
	}
	future := time.Now().Add(2 * time.Hour)
	if due := m.DueSchedules(future); len(due) != 1 {
		t.Fatalf("v1 schedules due = %d, want 1", len(due))
	}

	v2 := buildZip(t, []zipEntry{
		{"manifest.json", manifestFor("demo", "2.0.0")},
		{"plugin.wasm", minimalCapabilityModule()},
	})
	if err := m.UploadPluginWithLicense("", v2, nil); err != nil {
		t.Fatalf("upload v2: %v", err)
	}
	if due := m.DueSchedules(future); len(due) != 0 {
		t.Fatalf("removed schedule still due = %d, want 0", len(due))
	}
}

func TestUploadPreservesDeclaredAssetPolicy(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, WithAllowPublicRoutes(true))
	t.Cleanup(func() { _ = m.Close() })

	zipBytes := buildZip(t, []zipEntry{
		{"manifest.json", []byte(`{
			"name": "demo",
			"version": "1.0.0",
			"entryPoint": "plugin.wasm",
			"assets": [{"path": "public/*", "auth": "none"}]
		}`)},
		{"plugin.wasm", minimalCapabilityModule()},
		{"assets/public/app.js", []byte("export const x = 1;")},
		{"assets/admin/app.js", []byte("export const y = 2;")},
	})
	if err := m.UploadPluginWithLicense("", zipBytes, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "demo", "assets", "public", "app.js")); err != nil {
		t.Fatalf("nested public asset missing: %v", err)
	}

	auth, err := m.GetAssetAuth("demo", "public/app.js")
	if err != nil {
		t.Fatalf("GetAssetAuth: %v", err)
	}
	if auth != RouteAuthNone {
		t.Fatalf("public asset auth = %q, want none", auth)
	}
	auth, err = m.GetAssetAuth("demo", "admin/app.js")
	if err != nil {
		t.Fatalf("GetAssetAuth: %v", err)
	}
	if auth != RouteAuthSession {
		t.Fatalf("undeclared asset auth = %q, want session", auth)
	}
}

func TestReadBoundedResponse(t *testing.T) {
	t.Run("under limit", func(t *testing.T) {
		data, err := readBoundedResponse(strings.NewReader("hello"), 16)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(data) != "hello" {
			t.Fatalf("data = %q, want hello", data)
		}
	})

	t.Run("exactly at limit", func(t *testing.T) {
		data, err := readBoundedResponse(strings.NewReader("hello"), 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(data) != "hello" {
			t.Fatalf("data = %q, want hello", data)
		}
	})

	t.Run("over limit returns the sentinel error", func(t *testing.T) {
		_, err := readBoundedResponse(strings.NewReader("hello"), 4)
		if !errors.Is(err, errHTTPFetchResponseTooLarge) {
			t.Fatalf("error = %v, want errHTTPFetchResponseTooLarge", err)
		}
	})
}

// assertNoPluginDirs fails when a failed upload left a plugin or staging
// directory behind.
func assertNoPluginDirs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read plugin root: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("plugin root not empty after a failed upload: %v", names)
	}
}
