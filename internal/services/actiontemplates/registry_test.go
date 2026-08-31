package actiontemplates

import (
	"testing"

	"windshift/internal/models"
)

// TestRegistry_LoadsWithoutError pins the "every shipped template
// parses + validates" invariant. A new file in templates/ with a typo
// or unknown node_type fails here at unit-test time rather than at
// startup or first API call.
func TestRegistry_LoadsWithoutError(t *testing.T) {
	all := Registry()
	if err := LoadError(); err != nil {
		t.Fatalf("template registry load error: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("registry empty — no templates shipped?")
	}
}

func TestRegistry_MilestoneTemplates(t *testing.T) {
	// Both halves of the "milestones from releases" capability must be
	// present, wired to the matching SCM triggers, and contain the
	// create_milestone node.
	cases := []struct {
		key     string
		trigger models.ActionTriggerType
	}{
		{"milestone_on_release_branch", models.ActionTriggerSCMReleaseBranchCreated},
		{"milestone_on_release_tag", models.ActionTriggerSCMTagCreated},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			tmpl, ok := Get(c.key)
			if !ok {
				t.Fatalf("template %q missing from registry", c.key)
			}
			if tmpl.TriggerType != c.trigger {
				t.Fatalf("template %q trigger = %q, want %q", c.key, tmpl.TriggerType, c.trigger)
			}
			var sawCreate bool
			for _, n := range tmpl.Nodes {
				if models.ActionNodeType(n.NodeType) == models.ActionNodeCreateMilestone {
					sawCreate = true
					break
				}
			}
			if !sawCreate {
				t.Fatalf("template %q has no create_milestone node", c.key)
			}
		})
	}
}
