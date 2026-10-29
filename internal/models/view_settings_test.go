//go:build test

package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestViewSettingsUnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		payload       string
		wantErr       bool
		wantKeyAbsent bool
		wantReset     bool
		wantViews     []string
	}{
		{name: "empty object means inherit", payload: `{}`, wantKeyAbsent: true},
		{name: "omitted key means inherit", payload: `{"unrelated": false}`, wantErr: true},
		{name: "explicit null means reset to default", payload: `{"enabled_views": null}`, wantReset: true},
		{name: "array means override", payload: `{"enabled_views": ["board", "list"]}`, wantViews: []string{"board", "list"}},
		{name: "unknown key rejected", payload: `{"enabled_views": ["board"], "collapsed": true}`, wantErr: true},
		{name: "non-array rejected", payload: `{"enabled_views": "board"}`, wantErr: true},
		{name: "non-object rejected", payload: `["board"]`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var settings ViewSettings
			err := json.Unmarshal([]byte(tt.payload), &settings)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Unmarshal(%s) error = nil, want failure", tt.payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s) = %v", tt.payload, err)
			}
			if tt.wantKeyAbsent {
				if settings.EnabledViews != nil {
					t.Fatalf("EnabledViews = %v, want nil for inherit", settings.EnabledViews)
				}
				return
			}
			if tt.wantReset {
				if settings.EnabledViews == nil || len(*settings.EnabledViews) != 0 {
					t.Fatalf("EnabledViews = %v, want pointer to empty slice for reset", settings.EnabledViews)
				}
				return
			}
			if settings.EnabledViews == nil {
				t.Fatalf("EnabledViews = nil, want override %v", tt.wantViews)
			}
			if !reflect.DeepEqual(*settings.EnabledViews, tt.wantViews) {
				t.Fatalf("EnabledViews = %v, want %v", *settings.EnabledViews, tt.wantViews)
			}
		})
	}
}

func TestViewSettingsMarshalJSONKeepsExplicitReset(t *testing.T) {
	t.Parallel()

	empty := []string{}
	settings := ViewSettings{EnabledViews: &empty}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"enabled_views":null}` {
		t.Fatalf("Marshal = %s, want explicit null", encoded)
	}

	var decoded ViewSettings
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	if decoded.EnabledViews == nil || len(*decoded.EnabledViews) != 0 {
		t.Fatalf("re-decoded EnabledViews = %v, want explicit reset", decoded.EnabledViews)
	}

	inherit, err := json.Marshal(ViewSettings{})
	if err != nil {
		t.Fatalf("Marshal inherit: %v", err)
	}
	if string(inherit) != `{}` {
		t.Fatalf("inherit Marshal = %s, want empty object", inherit)
	}
}

func TestViewSettingsHelpers(t *testing.T) {
	t.Parallel()

	var inherit *ViewSettings
	if inherit.HasEnabledViewsOverride() {
		t.Fatal("nil settings must not report an override")
	}
	if inherit.EnabledViewSet() != nil {
		t.Fatal("nil settings must have no enabled set")
	}

	empty := []string{}
	reset := &ViewSettings{EnabledViews: &empty}
	if reset.HasEnabledViewsOverride() {
		t.Fatal("explicit reset must not report an override")
	}

	views := []string{"board"}
	override := &ViewSettings{EnabledViews: &views}
	if !override.HasEnabledViewsOverride() {
		t.Fatal("override must report an override")
	}
	set := override.EnabledViewSet()
	if _, ok := set["board"]; !ok {
		t.Fatalf("EnabledViewSet = %v, want board", set)
	}
}

func TestViewSettingsCompletedVisibility(t *testing.T) {
	t.Parallel()

	// Omitted key means inherit, an explicit null resets to the legacy
	// behavior, and a value round-trips unchanged.
	var inherit ViewSettings
	if err := json.Unmarshal([]byte(`{}`), &inherit); err != nil {
		t.Fatalf("Unmarshal({}): %v", err)
	}
	if inherit.CompletedVisibility != nil {
		t.Fatalf("CompletedVisibility = %v, want nil for inherit", inherit.CompletedVisibility)
	}

	var reset ViewSettings
	if err := json.Unmarshal([]byte(`{"completed_visibility": null}`), &reset); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if reset.CompletedVisibility == nil || *reset.CompletedVisibility != "" {
		t.Fatalf("CompletedVisibility = %v, want pointer to empty string for reset", reset.CompletedVisibility)
	}

	var override ViewSettings
	if err := json.Unmarshal([]byte(`{"completed_visibility": "last_week"}`), &override); err != nil {
		t.Fatalf("Unmarshal(value): %v", err)
	}
	if override.CompletedVisibility == nil || *override.CompletedVisibility != "last_week" {
		t.Fatalf("CompletedVisibility = %v, want last_week", override.CompletedVisibility)
	}
	encoded, err := json.Marshal(override)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"completed_visibility":"last_week"}` {
		t.Fatalf("Marshal = %s, want completed_visibility last_week", encoded)
	}

	// Unknown keys stay rejected so typos do not silently persist.
	if err := json.Unmarshal([]byte(`{"completed_visibilty": "all"}`), &ViewSettings{}); err == nil {
		t.Fatal("Unmarshal(unknown key) = nil, want failure")
	}
}

func TestIsCompletedVisibility(t *testing.T) {
	t.Parallel()

	for _, value := range CompletedVisibilityValues {
		if !IsCompletedVisibility(value) {
			t.Fatalf("IsCompletedVisibility(%q) = false, want true", value)
		}
	}
	if IsCompletedVisibility("fortnight") {
		t.Fatal("IsCompletedVisibility(fortnight) = true, want false")
	}
}
