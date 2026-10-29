//go:build test

package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// buildGzipTar writes a gzipped tar archive whose entries are the given
// name→content pairs. The uncompressed bytes are streamed, so a large logical
// entry compresses to a handful of KiB.
func buildGzipTar(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header %q: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write tar entry %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// buildGzipBomb writes a gzipped tar with a single entry that decompresses to
// size bytes of zeros without ever holding that much uncompressed data in
// memory on the test side.
func buildGzipBomb(t *testing.T, name string, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(size), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write bomb header: %v", err)
	}
	chunk := make([]byte, 32*1024)
	remaining := size
	for remaining > 0 {
		n := len(chunk)
		if n > remaining {
			n = remaining
		}
		if _, err := tw.Write(chunk[:n]); err != nil {
			t.Fatalf("write bomb chunk: %v", err)
		}
		remaining -= n
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func minimalPackEntries() map[string][]byte {
	return map[string][]byte{
		"manifest.json": []byte(`{
			"schema_version": 1,
			"kind": "windshift.pack",
			"name": "test-pack",
			"version": "1.0.0",
			"schema": {"configuration_set": "cs.json"}
		}`),
		"cs.json": []byte(`{"name": "Test"}`),
	}
}

func TestParsePackArchive_AcceptsArchiveWithinLimits(t *testing.T) {
	archive := buildGzipTar(t, minimalPackEntries())

	parsed, err := parsePackArchive(archive, 1<<20, 1<<20)
	if err != nil {
		t.Fatalf("expected archive within limits to parse, got %v", err)
	}
	if parsed.Manifest.Name != "test-pack" {
		t.Fatalf("manifest name = %q, want test-pack", parsed.Manifest.Name)
	}
}

func TestParsePackArchive_RejectsEntryExceedingPerEntryLimit(t *testing.T) {
	// 128 KiB entry against a 64 KiB per-entry cap, with a generous total cap
	// so the failure can only come from the per-entry bound.
	archive := buildGzipBomb(t, "manifest.json", 128<<10)

	_, err := parsePackArchive(archive, 1<<20, 64<<10)
	if err == nil {
		t.Fatal("expected oversized entry to be rejected")
	}
	if !strings.Contains(err.Error(), "entry") {
		t.Fatalf("error = %q, want per-entry limit error", err.Error())
	}
}

func TestParsePackArchive_RejectsArchiveExceedingTotalLimit(t *testing.T) {
	// Two 64 KiB entries against a 96 KiB total cap: each entry fits the
	// generous per-entry cap, but the archive as a whole exceeds the budget.
	entries := minimalPackEntries()
	entries["extra-a.bin"] = bytes.Repeat([]byte("a"), 64<<10)
	entries["extra-b.bin"] = bytes.Repeat([]byte("b"), 64<<10)
	archive := buildGzipTar(t, entries)

	_, err := parsePackArchive(archive, 96<<10, 1<<20)
	if err == nil {
		t.Fatal("expected oversized archive to be rejected")
	}
	if !strings.Contains(err.Error(), "decompressed archive") {
		t.Fatalf("error = %q, want archive-wide limit error", err.Error())
	}
}
