//go:build test

package cql

import (
	"slices"
	"testing"
)

func TestItemCompletionFieldsDescribeMilestoneValueHelp(t *testing.T) {
	fields := ItemCompletionFields()
	index := slices.IndexFunc(fields, func(field CompletionField) bool {
		return field.Name == "milestone"
	})
	if index < 0 {
		t.Fatal("milestone completion field is missing")
	}

	field := fields[index]
	if field.ValueType != "string" || field.ValueSource != CompletionValuesMilestones {
		t.Fatalf("milestone completion = %#v", field)
	}
	for _, operator := range []string{"=", "!=", "~", "IN", "NOT IN"} {
		if !slices.Contains(field.Operators, operator) {
			t.Errorf("milestone operators %v do not contain %q", field.Operators, operator)
		}
	}
}

func TestItemCompletionFieldsReturnsIndependentCopies(t *testing.T) {
	first := ItemCompletionFields()
	first[0].Aliases = append(first[0].Aliases, "changed")
	first[0].Operators[0] = "changed"

	second := ItemCompletionFields()
	if slices.Contains(second[0].Aliases, "changed") || second[0].Operators[0] == "changed" {
		t.Fatalf("catalog mutation leaked into a later call: %#v", second[0])
	}
}
