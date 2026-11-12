//go:build test

package models

import (
	"reflect"
	"testing"
)

func TestNotificationMetadataI18nRoundTrip(t *testing.T) {
	params := map[string]any{"itemKey": "WI-1", "itemTitle": "Fix login"}
	metadata := NotificationMetadataJSON("notification.dueReminder.title", "notification.dueReminder.message", params)
	if metadata == "" {
		t.Fatal("NotificationMetadataJSON returned empty metadata")
	}

	titleKey, messageKey, gotParams, ok := ParseNotificationI18n(metadata)
	if !ok {
		t.Fatalf("ParseNotificationI18n did not recognise %q", metadata)
	}
	if titleKey != "notification.dueReminder.title" || messageKey != "notification.dueReminder.message" {
		t.Fatalf("keys = (%q, %q)", titleKey, messageKey)
	}
	// JSON round-trips numbers to float64; these params are strings.
	if !reflect.DeepEqual(gotParams, params) {
		t.Fatalf("params = %#v, want %#v", gotParams, params)
	}
}

func TestNotificationMetadataEmptyIsOmitted(t *testing.T) {
	if got := NotificationMetadataJSON("", "", nil); got != "" {
		t.Fatalf("empty keys produced metadata %q, want \"\"", got)
	}
}

func TestParseNotificationI18nRejectsNonI18nMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
	}{
		{name: "empty", metadata: ""},
		{name: "whitespace", metadata: "   "},
		{name: "unrelated json", metadata: `{"foo":"bar"}`},
		{name: "invalid json", metadata: `{`},
		{name: "empty i18n object", metadata: `{"i18n":{}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, ok := ParseNotificationI18n(tc.metadata); ok {
				t.Fatalf("ParseNotificationI18n(%q) reported usable metadata", tc.metadata)
			}
		})
	}
}
