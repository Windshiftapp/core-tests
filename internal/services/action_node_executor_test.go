package services

import (
	"testing"

	"windshift/internal/models"
)

// fakeNodeExecutor records its calls so the dispatch path can be
// asserted without standing up the wider action engine.
type fakeNodeExecutor struct {
	nodeType models.ActionNodeType
	calls    int
}

func (f *fakeNodeExecutor) NodeType() models.ActionNodeType { return f.nodeType }
func (f *fakeNodeExecutor) Execute(_ *models.ActionNode, _ *models.ExecutionContext, _ *models.StepResult) error {
	f.calls++
	return nil
}

func TestRegisterNodeExecutor_DispatchesViaRegistry(t *testing.T) {
	as := &ActionService{}
	exec := &fakeNodeExecutor{nodeType: models.ActionNodeCreateMilestone}
	as.RegisterNodeExecutor(exec)

	got, ok := as.lookupNodeExecutor(models.ActionNodeCreateMilestone)
	if !ok {
		t.Fatal("registry miss after RegisterNodeExecutor")
	}
	if got != exec {
		t.Fatal("registry returned wrong executor")
	}

	// Re-registration replaces the prior entry (last-call-wins, used by
	// tests to swap an executor for a stub).
	exec2 := &fakeNodeExecutor{nodeType: models.ActionNodeCreateMilestone}
	as.RegisterNodeExecutor(exec2)
	got, _ = as.lookupNodeExecutor(models.ActionNodeCreateMilestone)
	if got != exec2 {
		t.Fatal("re-registration did not replace prior executor")
	}
}

func TestLookupNodeExecutor_MissOnEmptyRegistry(t *testing.T) {
	as := &ActionService{}
	if _, ok := as.lookupNodeExecutor(models.ActionNodeCreateMilestone); ok {
		t.Fatal("expected registry miss on zero-value service")
	}
}
