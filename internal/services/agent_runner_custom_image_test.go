//go:build test

package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/models"
)

// A binding-configured custom image (RunInput.Image, set from runner_image for
// pool runs — WI-450) overrides the runner's static default; an empty override
// keeps the default. The image is always the last positional docker arg.
func TestDockerAgentRunner_BuildDockerArgs_CustomImageOverride(t *testing.T) {
	r := &DockerAgentRunner{Image: "ghcr.io/windshiftapp/windshift-agent:default"}

	t.Run("override wins", func(t *testing.T) {
		args := r.buildDockerArgs(RunInput{RunID: 1, Image: "ghcr.io/acme/playwright:1"}, "/tmp/e.env")
		if got := args[len(args)-1]; got != "ghcr.io/acme/playwright:1" {
			t.Fatalf("image must be the per-run override as last positional; got %q (%v)", got, args)
		}
	})

	t.Run("empty override falls back to default", func(t *testing.T) {
		args := r.buildDockerArgs(RunInput{RunID: 1}, "/tmp/e.env")
		if got := args[len(args)-1]; got != "ghcr.io/windshiftapp/windshift-agent:default" {
			t.Fatalf("empty override must use the runner default; got %q (%v)", got, args)
		}
	})
}

// The override must not relax any baseline sandbox flag — only the image name
// changes, never the hardening contract.
func TestDockerAgentRunner_BuildDockerArgs_CustomImageKeepsHardening(t *testing.T) {
	r := &DockerAgentRunner{Image: "wi/default:test"}
	args := r.buildDockerArgs(RunInput{RunID: 9, Image: "ghcr.io/acme/playwright:1"}, "/tmp/e.env")
	mustContainArg(t, args,
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=1000:1000",
		"--read-only",
		"--network=coding-agent-egress",
	)
}

// agentImage is the selection helper: per-run override when set, else default.
func TestDockerAgentRunner_agentImage(t *testing.T) {
	r := &DockerAgentRunner{Image: "default:img"}
	if got := r.agentImage(RunInput{}); got != "default:img" {
		t.Errorf("no override: want default:img, got %q", got)
	}
	if got := r.agentImage(RunInput{Image: "custom:img"}); got != "custom:img" {
		t.Errorf("override: want custom:img, got %q", got)
	}
}

func TestDockerAgentRunner_RejectsPerRunImageOutsideAllowlist(t *testing.T) {
	docker := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  printf '\n'
  exit 0
fi
printf '%s\n' '{"type":"finish","outcome":"no_changes"}'
`
	if err := os.WriteFile(docker, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	runner := &DockerAgentRunner{
		Image:         "default-agent:v1",
		DockerBinary:  docker,
		InitialPrompt: "test",
	}
	result := runner.Run(context.Background(), RunInput{
		RunID: 17,
		Image: "attacker.example/root-image:latest",
	}, func(string, string) error { return nil })

	if result.Status != models.AgentRunStatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "is not in the runner image allowlist") {
		t.Fatalf("error = %q, want image allowlist denial", result.Error)
	}
}

func TestDockerAgentRunner_RejectsUnlabeledAllowlistedImage(t *testing.T) {
	docker := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  printf '\n'
  exit 0
fi
exit 99
`
	if err := os.WriteFile(docker, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	const image = "operator.example/agent:review"
	runner := &DockerAgentRunner{
		Image:         "default-agent:v1",
		DockerBinary:  docker,
		AllowedImages: []string{image},
		InitialPrompt: "test",
	}

	result := runner.Run(context.Background(), RunInput{RunID: 18, Image: image}, func(string, string) error { return nil })
	if result.Status != models.AgentRunStatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "does not carry the org.windshift.agent-contract label") {
		t.Fatalf("error = %q, want missing agent-contract label", result.Error)
	}
}
