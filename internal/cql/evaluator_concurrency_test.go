//go:build test

package cql

import (
	"testing"
	"time"
)

func TestAssetEvaluatorEvaluationDoesNotMutateSharedGenerator(t *testing.T) {
	tests := []struct {
		name     string
		evaluate func(*AssetEvaluator) error
	}{
		{
			name: "current time",
			evaluate: func(evaluator *AssetEvaluator) error {
				_, _, err := evaluator.EvaluateToSQL(`linkedOf("relates", "workspace = TEST")`)
				return err
			},
		},
		{
			name: "fixed time",
			evaluate: func(evaluator *AssetEvaluator) error {
				_, _, err := evaluator.EvaluateToSQLAt(
					`linkedOf("relates", "workspace = TEST")`,
					time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC),
				)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evaluator := NewAssetEvaluator(nil, map[string]int{"test": 7}, "sqlite")
			if evaluator.sqlGenerator.workspaceMap != nil {
				t.Fatal("asset generator unexpectedly starts with an item workspace map")
			}
			if err := tc.evaluate(evaluator); err != nil {
				t.Fatalf("evaluate asset CQL: %v", err)
			}
			if evaluator.sqlGenerator.workspaceMap != nil {
				t.Fatal("asset evaluation mutated the shared generator workspace map")
			}
		})
	}
}
