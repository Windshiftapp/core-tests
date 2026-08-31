//go:build test

package wscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteSkillQuotesMetadataAndDelimitsBody(t *testing.T) {
	var out bytes.Buffer
	err := writeSkill(&out, &AgentSkill{
		Name: "safe\n## injected heading", Description: "use it\n- injected list", Body: "# Trusted body",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `Skill name: "safe\n## injected heading"`) ||
		!strings.Contains(got, `Skill description: "use it\n- injected list"`) {
		t.Fatalf("metadata was not rendered as quoted data: %q", got)
	}
	if !strings.Contains(got, "--- BEGIN SAVED SKILL BODY ---\n# Trusted body\n--- END SAVED SKILL BODY ---") {
		t.Fatalf("body delimiters missing: %q", got)
	}
}
