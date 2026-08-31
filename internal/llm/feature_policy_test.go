package llm

import (
	"testing"

	"windshift/internal/models"
)

// TestDecideFeatureResolution covers the three feature-policy cells the
// security review flagged: disabled (regardless of override), pinned (ignore
// override), and default (honor override).
func TestDecideFeatureResolution(t *testing.T) {
	type want struct {
		disabled     bool
		connectionID int
	}
	cases := []struct {
		name     string
		fc       models.AIFeatureConfig
		override int
		want     want
	}{
		{
			name:     "no config + no override → default resolution",
			fc:       models.AIFeatureConfig{},
			override: 0,
			want:     want{connectionID: 0},
		},
		{
			name:     "no config + override → honor override",
			fc:       models.AIFeatureConfig{},
			override: 7,
			want:     want{connectionID: 7},
		},
		{
			name:     "default mode + override → honor override",
			fc:       models.AIFeatureConfig{Mode: models.AIFeatureModeDefault},
			override: 7,
			want:     want{connectionID: 7},
		},
		{
			name:     "disabled mode + override → still disabled",
			fc:       models.AIFeatureConfig{Mode: models.AIFeatureModeDisabled},
			override: 7,
			want:     want{disabled: true},
		},
		{
			name:     "disabled mode + no override → disabled",
			fc:       models.AIFeatureConfig{Mode: models.AIFeatureModeDisabled},
			override: 0,
			want:     want{disabled: true},
		},
		{
			name:     "pinned + matching override → pinned wins",
			fc:       models.AIFeatureConfig{Mode: models.AIFeatureModeSpecific, ConnectionID: 3},
			override: 3,
			want:     want{connectionID: 3},
		},
		{
			name:     "pinned + different override → pinned wins (the security case)",
			fc:       models.AIFeatureConfig{Mode: models.AIFeatureModeSpecific, ConnectionID: 3},
			override: 7,
			want:     want{connectionID: 3},
		},
		{
			name:     "pinned + no override → pinned",
			fc:       models.AIFeatureConfig{Mode: models.AIFeatureModeSpecific, ConnectionID: 3},
			override: 0,
			want:     want{connectionID: 3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideFeatureResolution(tc.fc, tc.override)
			if got.disabled != tc.want.disabled {
				t.Errorf("disabled: want %v, got %v", tc.want.disabled, got.disabled)
			}
			if got.connectionID != tc.want.connectionID {
				t.Errorf("connectionID: want %d, got %d", tc.want.connectionID, got.connectionID)
			}
		})
	}
}
