package services

import (
	"os"
	"strings"
	"testing"

	"windshift/internal/models"
)

func TestBuildDockerRunArgs_DefaultNetworkIsNone(t *testing.T) {
	cfg := models.DockerEnvironmentConfig{
		Image:          "example/image:latest",
		ResourceLimits: models.ResourceLimits{Memory: "512m", CPUs: "1"},
		// NetworkMode left empty — should default to "none".
	}

	args := buildDockerRunArgs(cfg, 31234, "")

	if !hasFlag(args, "--network", "none") {
		t.Fatalf("expected --network none flag in args, got: %v", args)
	}

	// Image must still come last so docker treats it as the positional arg.
	if args[len(args)-1] != "example/image:latest" {
		t.Fatalf("expected last arg to be the image, got: %v", args)
	}
}

func TestBuildDockerRunArgs_RespectsExplicitNetworkMode(t *testing.T) {
	cfg := models.DockerEnvironmentConfig{
		Image:          "example/image:latest",
		ResourceLimits: models.ResourceLimits{Memory: "512m", CPUs: "1"},
		NetworkMode:    "bridge",
	}

	args := buildDockerRunArgs(cfg, 31234, "")

	if !hasFlag(args, "--network", "bridge") {
		t.Fatalf("expected --network bridge flag in args, got: %v", args)
	}
	if hasFlag(args, "--network", "none") {
		t.Fatalf("unexpected --network none in args (NetworkMode was 'bridge'): %v", args)
	}
}

func TestBuildDockerRunArgs_UsesEnvFileNotArgv(t *testing.T) {
	cfg := models.DockerEnvironmentConfig{
		Image:          "example/image:latest",
		ResourceLimits: models.ResourceLimits{Memory: "512m", CPUs: "1"},
		EnvVars:        map[string]string{"FOO": "bar"},
	}

	args := buildDockerRunArgs(cfg, 31234, "/tmp/secret-env.env")

	// Secrets must travel via --env-file, never as -e KEY=VALUE argv (which
	// would expose them in /proc/<pid>/cmdline and `docker inspect`).
	if !hasFlag(args, "--env-file", "/tmp/secret-env.env") {
		t.Fatalf("expected --env-file flag in args, got: %v", args)
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "-e" {
			t.Fatalf("env vars must not be passed via -e argv, got: %v", args)
		}
		if strings.HasPrefix(args[i], "FOO=") {
			t.Fatalf("env value leaked into argv: %v", args)
		}
	}
}

func TestBuildDockerRunArgs_NoEnvFileWhenEmpty(t *testing.T) {
	cfg := models.DockerEnvironmentConfig{
		Image:          "example/image:latest",
		ResourceLimits: models.ResourceLimits{Memory: "512m", CPUs: "1"},
	}

	args := buildDockerRunArgs(cfg, 31234, "")

	for i := 0; i < len(args); i++ {
		if args[i] == "--env-file" {
			t.Fatalf("did not expect --env-file with no env path, got: %v", args)
		}
	}
}

func TestWriteContainerEnvFile(t *testing.T) {
	// No env vars: no file, no-op cleanup.
	path, cleanup, err := writeContainerEnvFile(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if path != "" {
		t.Fatalf("expected empty path for no env vars, got %q", path)
	}

	// Env vars: 0600 file containing KEY=value lines.
	path, cleanup, err = writeContainerEnvFile(map[string]string{"TOKEN": "s3cr3t"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("env file not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 perms, got %v", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read env file: %v", err)
	}
	if !strings.Contains(string(data), "TOKEN=s3cr3t") {
		t.Fatalf("expected TOKEN=s3cr3t in env file, got: %q", string(data))
	}

	// Newline injection is rejected.
	if _, _, err := writeContainerEnvFile(map[string]string{"X": "a\nB=c"}); err == nil {
		t.Fatalf("expected error for newline in env value")
	}
}

// hasFlag returns true when `flag value` appears as a consecutive pair in args.
func hasFlag(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
