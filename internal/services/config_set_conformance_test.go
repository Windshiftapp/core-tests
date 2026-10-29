//go:build test

package services

import (
	"encoding/json"
	"strings"
	"testing"
)

// Table-driven coverage for the template diff engine behind the conformance
// check (WI-1334). Each case introduces one deliberate difference and pins
// the drift rows the engine must report, including repairability.

func canonicalTemplate() *ConfigSetTemplate {
	return &ConfigSetTemplate{
		SchemaVersion: ConfigSetTemplateSchemaVersion,
		Kind:          ConfigSetTemplateKind,
		Payload: ConfigSetTplPayload{
			Statuses: []ConfigSetTplStatus{
				{Name: "RT Open", CategoryName: "To Do"},
				{Name: "RT Done", CategoryName: "Done"},
			},
			Workflows: []ConfigSetTplWorkflow{
				{Name: "RT Workflow", Transitions: []ConfigSetTplWorkflowTransition{
					{ToStatusName: "RT Open"},
					{FromStatusName: confStrPtr("RT Open"), ToStatusName: "RT Done", DisplayOrder: 1},
				}},
			},
			Screens: []ConfigSetTplScreen{
				{Name: "RT Screen", Fields: []ConfigSetTplScreenField{
					{FieldKind: "default", FieldIdentifier: "title"},
					{FieldKind: "custom", CustomFieldName: "Risk Score", DisplayOrder: 1, IsRequired: true},
				}, SystemFields: []string{"priority_id"}},
			},
			CustomFields: []ConfigSetTplCustomField{
				{Name: "Risk Score", FieldType: "number"},
			},
			Priorities: []ConfigSetTplPriority{{Name: "RT Critical"}},
			ItemTypes:  []ConfigSetTplItemType{{Name: "RT Hazard", HierarchyLevel: 1}},
			LinkTypes: []ConfigSetTplLinkType{
				{Name: "mitigates-risk", ForwardLabel: "mitigated by", ReverseLabel: "mitigates", Color: "#0ea5e9"},
			},
			ConditionSets: []ConfigSetTplConditionSet{
				{Name: "RT Conditions", WorkflowName: "RT Workflow", TransitionConditions: []ConfigSetTplTransitionCondition{
					{ToStatusName: "RT Open", LogicMode: "and", Conditions: []ConfigSetTplCondition{
						{Type: "user_in_role", Mode: "hide", Config: map[string]any{"role_name": "Admins"}},
					}},
				}},
			},
			ApprovalSets: []ConfigSetTplApprovalSet{
				{Name: "RT Approvals", WorkflowName: "RT Workflow", SetStatuses: []ConfigSetTplApprovalSetStatus{
					{StatusName: "RT Open", StepMode: "sequential",
						ApproveTransition: ConfigSetTplTransitionRef{FromStatusName: confStrPtr("RT Open"), ToStatusName: "RT Done"},
						DenyTransition:    ConfigSetTplTransitionRef{FromStatusName: confStrPtr("RT Open"), ToStatusName: "RT Open"},
						Steps: []ConfigSetTplApprovalStep{
							{DisplayOrder: 0, QuorumMode: "any", ApproverSource: "role", ApproverRoleName: "Admins", RejectionPolicy: "any_rejection_fails", OnLeaveStrategy: "use_substitute"},
						}},
				}},
			},
			Links: ConfigSetTplLinks{
				WorkflowName:     "RT Workflow",
				CreateScreenName: "RT Screen",
				EditScreenName:   "RT Screen",
				ViewScreenName:   "RT Screen",
				PriorityNames:    []string{"RT Critical"},
				ItemTypeConfigs: []ConfigSetTplItemTypeConfig{
					{ItemTypeName: "RT Hazard", WorkflowName: "RT Workflow"},
				},
			},
			ConfigurationSet: ConfigSetTplConfigSet{Name: "RT Full Set", DefaultItemTypeName: "RT Hazard"},
		},
	}
}

func confStrPtr(s string) *string { return &s }

// liveLikeCanonical returns a deep-enough copy of the canonical template so
// individual cases can mutate one section.
func liveLikeCanonical(t *testing.T, canonical *ConfigSetTemplate) *ConfigSetTemplate {
	t.Helper()
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}
	live := &ConfigSetTemplate{}
	if err := json.Unmarshal(raw, live); err != nil {
		t.Fatalf("unmarshal live: %v", err)
	}
	return live
}

func findDrift(drifts []ConfigSetConformanceDrift, id string) *ConfigSetConformanceDrift {
	for i := range drifts {
		if drifts[i].ID == id {
			return &drifts[i]
		}
	}
	return nil
}

func TestConformanceDiffCleanTemplatesProduceNoDrift(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	drifts := DiffConformanceTemplates(canonical, live)
	if len(drifts) != 0 {
		t.Fatalf("clean templates produced drift: %+v", drifts)
	}
}

func TestConformanceDiffStatusDrift(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	live.Payload.Statuses = live.Payload.Statuses[:1] // drop RT Done

	drifts := DiffConformanceTemplates(canonical, live)
	d := findDrift(drifts, "statuses/missing/rt done")
	if d == nil {
		t.Fatalf("missing status not reported: %+v", drifts)
	}
	if !d.Repairable || d.Detail != `status "RT Done" is missing` {
		t.Fatalf("unexpected row: %+v", d)
	}
}

func TestConformanceDiffStatusCategoryDrift(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	live.Payload.Statuses[0].CategoryName = "Done"

	drifts := DiffConformanceTemplates(canonical, live)
	d := findDrift(drifts, "statuses/differing/rt open/category")
	if d == nil {
		t.Fatalf("category drift not reported: %+v", drifts)
	}
	if !strings.Contains(d.Detail, `is in category "Done", template requires "To Do"`) {
		t.Fatalf("detail does not name the difference: %+v", d)
	}
}

func TestConformanceDiffExtraStatusReportedNotRepairable(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	live.Payload.Statuses = append(live.Payload.Statuses, ConfigSetTplStatus{Name: "Rogue", CategoryName: "To Do"})

	drifts := DiffConformanceTemplates(canonical, live)
	d := findDrift(drifts, "statuses/extra/rogue")
	if d == nil {
		t.Fatalf("extra status not reported: %+v", drifts)
	}
	if d.Repairable {
		t.Fatalf("extra status must not be auto-repairable: %+v", d)
	}
}

func TestConformanceDiffWorkflowTransitionDrift(t *testing.T) {
	canonical := canonicalTemplate()

	t.Run("missing_transition", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Workflows[0].Transitions = live.Payload.Workflows[0].Transitions[:1]
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "workflows/missing/rt workflow/transition/rt open→rt done")
		if d == nil {
			t.Fatalf("missing transition not reported: %+v", drifts)
		}
		if d.Detail != `workflow "RT Workflow" is missing the "RT Open" → "RT Done" transition` {
			t.Fatalf("detail must name the transition: %+v", d)
		}
	})

	t.Run("differing_display_order", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Workflows[0].Transitions[1].DisplayOrder = 9
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "workflows/differing/rt workflow/transition/rt open→rt done")
		if d == nil {
			t.Fatalf("differing transition not reported: %+v", drifts)
		}
		if !strings.Contains(d.Detail, "order/handles changed") {
			t.Fatalf("detail must name the difference: %+v", d)
		}
	})

	t.Run("extra_transition_not_repairable", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Workflows[0].Transitions = append(live.Payload.Workflows[0].Transitions,
			ConfigSetTplWorkflowTransition{FromStatusName: confStrPtr("RT Done"), ToStatusName: "RT Open"})
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "workflows/extra/rt workflow/transition/rt open")
		if d == nil {
			t.Fatalf("extra transition not reported: %+v", drifts)
		}
		if d.Repairable {
			t.Fatalf("extra transitions must not be auto-repairable: %+v", d)
		}
	})
}

func TestConformanceDiffScreenFieldDrift(t *testing.T) {
	canonical := canonicalTemplate()

	t.Run("missing_custom_field", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Screens[0].Fields = live.Payload.Screens[0].Fields[:1]
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "screens/missing/rt screen/field/custom:risk score")
		if d == nil {
			t.Fatalf("missing screen field not reported: %+v", drifts)
		}
		if !d.Repairable {
			t.Fatalf("missing screen field must be repairable: %+v", d)
		}
	})

	t.Run("differing_required_flag", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Screens[0].Fields[1].IsRequired = false
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "screens/differing/rt screen/field/custom:risk score")
		if d == nil {
			t.Fatalf("differing screen field not reported: %+v", drifts)
		}
	})

	t.Run("system_field_drift", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Screens[0].SystemFields = nil
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "screens/missing/rt screen/system/priority_id")
		if d == nil {
			t.Fatalf("missing system field not reported: %+v", drifts)
		}
	})
}

func TestConformanceDiffCustomFieldTypeDrift(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	live.Payload.CustomFields[0].FieldType = "text"

	drifts := DiffConformanceTemplates(canonical, live)
	d := findDrift(drifts, "custom_fields/differing/risk score")
	if d == nil {
		t.Fatalf("custom field drift not reported: %+v", drifts)
	}
	if !strings.Contains(d.Detail, `type "text" → "number"`) {
		t.Fatalf("detail must name the type change: %+v", d)
	}
}

func TestConformanceDiffLinkTypeDrift(t *testing.T) {
	canonical := canonicalTemplate()

	t.Run("missing", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.LinkTypes = nil
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "link_types/missing/mitigates-risk")
		if d == nil {
			t.Fatalf("missing link type not reported: %+v", drifts)
		}
		if !d.Repairable {
			t.Fatalf("missing link type must be repairable: %+v", d)
		}
	})

	t.Run("differing_label", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.LinkTypes[0].ReverseLabel = "blocks"
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "link_types/differing/mitigates-risk")
		if d == nil {
			t.Fatalf("link type drift not reported: %+v", drifts)
		}
		if !strings.Contains(d.Detail, `reverse label "blocks" → "mitigates"`) {
			t.Fatalf("detail must name the label change: %+v", d)
		}
	})
}

func TestConformanceDiffConditionSetDrift(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	live.Payload.ConditionSets[0].TransitionConditions[0].Conditions[0].Config = map[string]any{"role_name": "Editors"}

	drifts := DiffConformanceTemplates(canonical, live)
	d := findDrift(drifts, "condition_sets/differing/rt conditions/binding/initial→rt open")
	if d == nil {
		t.Fatalf("condition drift not reported: %+v", drifts)
	}
	if !strings.Contains(d.Detail, `conditions for initial→RT Open differ`) {
		t.Fatalf("detail must name the binding: %+v", d)
	}
}

func TestConformanceDiffApprovalStepDrift(t *testing.T) {
	canonical := canonicalTemplate()
	live := liveLikeCanonical(t, canonical)
	live.Payload.ApprovalSets[0].SetStatuses[0].Steps[0].DisplayOrder = 3

	drifts := DiffConformanceTemplates(canonical, live)
	d := findDrift(drifts, "approval_sets/differing/rt approvals/status/rt open")
	if d == nil {
		t.Fatalf("approval drift not reported: %+v", drifts)
	}
	if !strings.Contains(d.Detail, `approval configuration for status "RT Open" differs`) {
		t.Fatalf("detail must name the status: %+v", d)
	}
}

func TestConformanceDiffLinksGlueDrift(t *testing.T) {
	canonical := canonicalTemplate()

	t.Run("workflow_assignment", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Links.WorkflowName = ""
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "links/differing/workflow")
		if d == nil {
			t.Fatalf("glue drift not reported: %+v", drifts)
		}
		if !strings.Contains(d.Detail, `workflow is "", template requires "RT Workflow"`) {
			t.Fatalf("detail must name the difference: %+v", d)
		}
	})

	t.Run("extra_priority_assignment", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Links.PriorityNames = []string{"RT Critical", "Low"}
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "links/extra/priority/low")
		if d == nil {
			t.Fatalf("extra assignment not reported: %+v", drifts)
		}
		if !d.Repairable {
			t.Fatalf("extra assignment must be repairable (junction rows only): %+v", d)
		}
	})

	t.Run("item_type_override", func(t *testing.T) {
		live := liveLikeCanonical(t, canonical)
		live.Payload.Links.ItemTypeConfigs[0].WorkflowName = ""
		drifts := DiffConformanceTemplates(canonical, live)
		d := findDrift(drifts, "links/differing/item_type_config/rt hazard/workflow")
		if d == nil {
			t.Fatalf("override drift not reported: %+v", drifts)
		}
	})
}

// TestConformanceEntityNameResolvesSlashNames verifies WI-1561: a canonical
// entity name may itself contain the drift-path separator, so resolution must
// match the longest whole canonical name instead of truncating at the first
// slash (which made "Ops/A" and "Ops/B" collide).
func TestConformanceEntityNameResolvesSlashNames(t *testing.T) {
	canonical := &ConfigSetTemplate{
		Payload: ConfigSetTplPayload{
			Workflows: []ConfigSetTplWorkflow{{Name: "Ops/A"}, {Name: "Ops/B"}},
		},
	}

	cases := []struct {
		drift string
		want  string
	}{
		{"Ops/A", "Ops/A"},
		{"Ops/A/description", "Ops/A"},
		{"Ops/B/transitions/0", "Ops/B"},
		{"Ops/C", "Ops"}, // not canonical: falls back to the first path segment
		{"Standalone", "Standalone"},
	}
	for _, tc := range cases {
		if got := resolveConformanceEntityName(canonical, "workflows", tc.drift); got != tc.want {
			t.Errorf("resolveConformanceEntityName(%q) = %q, want %q", tc.drift, got, tc.want)
		}
	}

	planA := &conformanceEntityPlan{section: "workflows", name: "Ops/A"}
	if !entityCovers(planA, "workflows", "Ops/A") {
		t.Error("plan for Ops/A does not cover Ops/A")
	}
	if entityCovers(planA, "workflows", "Ops/B") {
		t.Error("plan for Ops/A wrongly covers Ops/B")
	}
	if entityCovers(planA, "workflows", "Ops") {
		t.Error("plan for Ops/A wrongly covers the Ops prefix")
	}
}
