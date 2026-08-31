package actionutil

import (
	"strings"
	"testing"

	"windshift/internal/models"
)

func validateCoreFlow(nodes []models.ActionNode, edges []models.ActionEdge) error {
	return ValidateFlowAcyclic[
		models.ActionNode, *models.ActionNode,
		models.ActionEdge, *models.ActionEdge,
	](nodes, edges)
}

func TestValidateFlowAcyclicRejectsEdgesWithoutNodes(t *testing.T) {
	t.Parallel()

	err := validateCoreFlow(nil, []models.ActionEdge{{SourceNodeID: 1, TargetNodeID: 2}})
	if err == nil || !strings.Contains(err.Error(), "edges but no nodes") {
		t.Fatalf("error = %v, want edges-without-nodes validation", err)
	}
}

func TestValidateFlowAcyclicRejectsDuplicateNodeIDsWithoutEdges(t *testing.T) {
	t.Parallel()

	err := validateCoreFlow([]models.ActionNode{{ID: 7}, {ID: 7}}, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate node ID 7") {
		t.Fatalf("error = %v, want duplicate-node validation", err)
	}
}

func TestCreateFlowNodesAndEdgesRejectsAmbiguousInputBeforeWrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		nodes []models.ActionNode
		edges []models.ActionEdge
	}{
		{
			name:  "edges without nodes",
			edges: []models.ActionEdge{{SourceNodeID: 1, TargetNodeID: 2}},
		},
		{
			name:  "duplicate node IDs",
			nodes: []models.ActionNode{{ID: 4}, {ID: 4}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writes := 0
			rollbacks := 0
			err := CreateFlowNodesAndEdges[
				models.ActionNode, *models.ActionNode,
				models.ActionEdge, *models.ActionEdge,
			](
				12, test.nodes, test.edges,
				func(*models.ActionNode) (int, error) { writes++; return 1, nil },
				func(*models.ActionEdge) (int, error) { writes++; return 1, nil },
				func() { rollbacks++ },
			)
			if err == nil {
				t.Fatal("invalid flow unexpectedly succeeded")
			}
			if writes != 0 {
				t.Fatalf("performed %d writes before rejecting invalid flow", writes)
			}
			if rollbacks != 1 {
				t.Fatalf("rollbacks = %d, want 1", rollbacks)
			}
		})
	}
}
