//go:build test

package models

import "testing"

func TestRunGrantsSkillForDeniesSkillsOutsideSnapshot(t *testing.T) {
	grants := &RunGrants{Skills: []SkillGrant{{ID: 4, Name: "attached", Body: "saved"}}}
	if got := grants.SkillFor(4); got == nil || got.Body != "saved" {
		t.Fatalf("attached skill grant: %+v", got)
	}
	if got := grants.SkillFor(5); got != nil {
		t.Fatalf("unattached skill must be denied, got %+v", got)
	}
}
