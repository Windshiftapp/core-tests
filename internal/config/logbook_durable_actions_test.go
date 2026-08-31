package config

import "testing"

func TestLoadLogbookSidecarDurableActionCutover(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "false", want: false},
		{value: "true", want: true},
		{value: "1", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("LOGBOOK_ACTIVATE_DURABLE_ACTIONS", tt.value)
			if got := LoadLogbookSidecar().ActivateDurableActions; got != tt.want {
				t.Fatalf("ActivateDurableActions = %v, want %v", got, tt.want)
			}
		})
	}
}
