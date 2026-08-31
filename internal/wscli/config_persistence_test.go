//go:build test

package wscli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSaveProjectConfigReplacesBroadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws.toml")
	if err := os.WriteFile(path, []byte("old config"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("set broad seed permissions: %v", err)
	}

	config := Config{Server: ServerConfig{URL: "https://windshift.example", Token: "secret-token"}}
	if err := saveProjectConfig(config, path); err != nil {
		t.Fatalf("saveProjectConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %04o, want 0600", got)
	}

	var loaded Config
	if _, err := toml.DecodeFile(path, &loaded); err != nil {
		t.Fatalf("decode saved config: %v", err)
	}
	if loaded.Server.Token != "secret-token" {
		t.Fatalf("saved token = %q, want secret-token", loaded.Server.Token)
	}
}

func TestSaveGlobalConfigProtectsDirectoryAndFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "ws")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed config directory: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("set broad directory permissions: %v", err)
	}

	config := Config{Server: ServerConfig{URL: "https://windshift.example", Token: "secret-token"}}
	if err := saveGlobalConfig(config); err != nil {
		t.Fatalf("saveGlobalConfig: %v", err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat config directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("config directory permissions = %04o, want 0700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("stat global config: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("global config permissions = %04o, want 0600", got)
	}
}

func TestSaveProjectConfigDoesNotFollowDestinationSymlink(t *testing.T) {
	dir := t.TempDir()
	victimPath := filepath.Join(dir, "victim.toml")
	configPath := filepath.Join(dir, "ws.toml")
	const original = "keep this file unchanged"
	if err := os.WriteFile(victimPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed symlink target: %v", err)
	}
	if err := os.Symlink(victimPath, configPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	config := Config{Server: ServerConfig{URL: "https://windshift.example", Token: "secret-token"}}
	if err := saveProjectConfig(config, configPath); err != nil {
		t.Fatalf("saveProjectConfig: %v", err)
	}
	victim, err := os.ReadFile(victimPath)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if string(victim) != original {
		t.Fatalf("symlink target was overwritten: %q", victim)
	}
	info, err := os.Lstat(configPath)
	if err != nil {
		t.Fatalf("lstat config: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("config path remained a symlink")
	}
}

func TestWriteFileAtomicallyPreservesExistingConfigOnEncodeFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.toml")
	const original = "working config"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	errEncode := errors.New("encode failed")
	err := writeFileAtomically(path, func(w io.Writer) error {
		if _, writeErr := io.WriteString(w, "partial config"); writeErr != nil {
			return writeErr
		}
		return errEncode
	})
	if !errors.Is(err, errEncode) {
		t.Fatalf("writeFileAtomically error = %v, want encode failure", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved config: %v", err)
	}
	if string(contents) != original {
		t.Fatalf("config after failed write = %q, want %q", contents, original)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read config directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "ws.toml" {
		t.Fatalf("config directory entries after failed write = %v, want only ws.toml", entries)
	}
}
