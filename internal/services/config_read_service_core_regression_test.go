package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestItemTypeResultUsesFrontendAPIFieldNames(t *testing.T) {
	payload, err := json.Marshal(ItemTypeResult{ID: 7, Name: "Request", HierarchyLevel: 1, IsDefault: true})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	encoded := string(payload)
	for _, field := range []string{`"id":7`, `"name":"Request"`, `"hierarchy_level":1`, `"is_default":true`} {
		if !strings.Contains(encoded, field) {
			t.Fatalf("encoded item type %s does not contain %s", encoded, field)
		}
	}
	if strings.Contains(encoded, `"ID"`) || strings.Contains(encoded, `"Name"`) {
		t.Fatalf("encoded item type leaked Go field names: %s", encoded)
	}
}
