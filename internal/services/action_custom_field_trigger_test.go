//go:build test

package services

import (
	"testing"

	"windshift/internal/models"
)

// Regression (bughunt, actions): trigger field filters must accept the
// editor's spellings of the change keys events actually carry:
// - custom fields are stored keyed by numeric id, so itemevents.Changes emits
//   "cf_<id>", while the editor saves the filter as "custom_field_<id>" (the
//   spelling the condition/set_field executors resolve);
// - milestone changes are keyed "milestones" while the editor saves
//   "milestone_id".
// Either mismatch makes an item_updated trigger silently never fire.
func TestItemUpdatedTriggerMatchesCustomFieldFilterSpellings(t *testing.T) {
	tests := []struct {
		name        string
		triggerConf string
		event       *models.ActionEvent
		want        bool
	}{
		{
			name:        "editor spelling custom_field_5 matches cf_5 change",
			triggerConf: `{"field_name": "custom_field_5"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"cf_5": "new"},
			},
			want: true,
		},
		{
			name:        "canonical spelling cf_5 matches cf_5 change",
			triggerConf: `{"field_name": "cf_5"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"cf_5": "new"},
			},
			want: true,
		},
		{
			name:        "custom_field_5 filter ignores other custom fields",
			triggerConf: `{"field_name": "custom_field_5"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"cf_6": "new"},
			},
			want: false,
		},
		{
			name:        "milestone_id filter matches milestones change",
			triggerConf: `{"field_name": "milestone_id"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"milestones": []int{3}},
			},
			want: true,
		},
		{
			name:        "milestone_ids filter matches milestones change",
			triggerConf: `{"field_name": "milestone_ids"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"milestones": []int{3}},
			},
			want: true,
		},
		{
			name:        "milestone filter ignores other field changes",
			triggerConf: `{"field_name": "milestone_id"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"title": "new"},
			},
			want: false,
		},
		{
			name:        "custom_field_5 filter ignores standard field changes",
			triggerConf: `{"field_name": "custom_field_5"}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"title": "new"},
			},
			want: false,
		},
		{
			name:        "no field filter matches any change",
			triggerConf: `{}`,
			event: &models.ActionEvent{
				EventType: models.ActionTriggerItemUpdated,
				ItemID:    17,
				NewValues: map[string]any{"cf_5": "new"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &ActionService{}
			action := &models.Action{
				TriggerType:   models.ActionTriggerItemUpdated,
				TriggerConfig: tt.triggerConf,
			}
			if got := service.matchesTrigger(action, tt.event); got != tt.want {
				t.Fatalf("matchesTrigger = %v, want %v", got, tt.want)
			}
		})
	}
}
