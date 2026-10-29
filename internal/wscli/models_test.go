//go:build test

package wscli

import (
	"encoding/json"
	"testing"
)

func TestItemCustomFieldValuesUseV2WireName(t *testing.T) {
	request := ItemCreateRequest{CustomFields: map[string]any{"42": "created"}}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal create request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode create request: %v", err)
	}
	if _, ok := body["custom_fields"]; ok {
		t.Fatalf("legacy custom_fields key present: %s", encoded)
	}
	values, ok := body["custom_field_values"].(map[string]any)
	if !ok || values["42"] != "created" {
		t.Fatalf("custom_field_values = %#v, want field 42=created", body["custom_field_values"])
	}

	var item Item
	if err := json.Unmarshal([]byte(`{"custom_field_values":{"42":"read"}}`), &item); err != nil {
		t.Fatalf("unmarshal item response: %v", err)
	}
	if item.CustomFields["42"] != "read" {
		t.Fatalf("decoded custom fields = %#v, want field 42=read", item.CustomFields)
	}
}
