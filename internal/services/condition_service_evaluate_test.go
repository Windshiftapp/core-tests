//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/models"
)

// TestEvaluateConditionsValidatorsAreHardGates pins the rule that
// validator-mode conditions gate every transition regardless of the binding's
// logic mode, while condition-mode rules decide availability under AND/OR.
func TestEvaluateConditionsValidatorsAreHardGates(t *testing.T) {
	service := NewConditionService(nil, nil, NewScriptEngine())
	item := map[string]any{}

	tests := []struct {
		name      string
		logicMode string
		validator string
		condition string
		wantAllow bool
		wantMsg   string
	}{
		{
			name:      "or validator fails condition passes",
			logicMode: "or",
			validator: "return false",
			condition: "return true",
			wantAllow: false,
			wantMsg:   "validator must pass",
		},
		{
			name:      "or validator passes condition fails",
			logicMode: "or",
			validator: "return true",
			condition: "return false",
			wantAllow: false,
		},
		{
			name:      "or both pass",
			logicMode: "or",
			validator: "return true",
			condition: "return true",
			wantAllow: true,
		},
		{
			name:      "and validator fails condition passes",
			logicMode: "and",
			validator: "return false",
			condition: "return true",
			wantAllow: false,
			wantMsg:   "validator must pass",
		},
		{
			name:      "and both pass",
			logicMode: "and",
			validator: "return true",
			condition: "return true",
			wantAllow: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := []conditionRow{
				{
					ConditionType: models.ConditionTypeScript,
					Mode:          models.ConditionModeValidator,
					Config:        `{"script":"` + tc.validator + `"}`,
					ErrorMessage:  "validator must pass",
				},
				{
					ConditionType: models.ConditionTypeScript,
					Mode:          models.ConditionModeCondition,
					Config:        `{"script":"` + tc.condition + `"}`,
				},
			}

			allowed, msg, err := service.evaluateConditions(context.Background(), rows, tc.logicMode, 1, item)
			if err != nil {
				t.Fatalf("evaluateConditions: %v", err)
			}
			if allowed != tc.wantAllow {
				t.Fatalf("allowed = %v, want %v (message %q)", allowed, tc.wantAllow, msg)
			}
			if tc.wantMsg != "" && msg != tc.wantMsg {
				t.Fatalf("message = %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}

// TestEvaluateConditionsValidatorsOnlyBinding verifies a binding with only
// validator rules is allowed once every validator passes, including under OR
// where there are no condition-mode rules to satisfy.
func TestEvaluateConditionsValidatorsOnlyBinding(t *testing.T) {
	service := NewConditionService(nil, nil, NewScriptEngine())
	rows := []conditionRow{
		{
			ConditionType: models.ConditionTypeScript,
			Mode:          models.ConditionModeValidator,
			Config:        `{"script":"return true"}`,
		},
	}

	allowed, msg, err := service.evaluateConditions(context.Background(), rows, "or", 1, map[string]any{})
	if err != nil {
		t.Fatalf("evaluateConditions: %v", err)
	}
	if !allowed {
		t.Fatalf("allowed = false, want true (message %q)", msg)
	}
}
