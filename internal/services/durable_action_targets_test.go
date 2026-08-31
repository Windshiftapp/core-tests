package services

import "testing"

type durableTargetFixture struct {
	id      int
	key     string
	matches bool
}

func TestSelectDurableActionTargetsEnforcesCascadeDepthAndChainDeduplication(t *testing.T) {
	actions := []durableTargetFixture{
		{id: 1, key: "first", matches: true},
		{id: 2, key: "second", matches: false},
		{id: 3, key: "third", matches: true},
	}
	chain := &ExecutionChain{ExecutedActions: map[string]bool{"first": true}}
	selectTargets := func(depth int) []int {
		return selectDurableActionTargets(
			actions,
			depth,
			chain,
			func(action durableTargetFixture) int { return action.id },
			func(action durableTargetFixture) string { return action.key },
			func(action durableTargetFixture) bool { return action.matches },
		)
	}

	if targets := selectTargets(MaxCascadeDepth - 1); len(targets) != 1 || targets[0] != 3 {
		t.Fatalf("targets below depth limit = %v, want [3]", targets)
	}
	if targets := selectTargets(MaxCascadeDepth); len(targets) != 0 {
		t.Fatalf("targets at depth limit = %v, want none", targets)
	}
}
