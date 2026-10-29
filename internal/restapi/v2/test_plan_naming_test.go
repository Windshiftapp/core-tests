//go:build test

package v2

import (
	"encoding/json"
	"strings"
	"testing"

	apispec "windshift/api"
	"windshift/internal/models"
)

func TestTestRunPublicDTOUsesPlanID(t *testing.T) {
	encoded, err := json.Marshal(mapTestRun(&models.TestRun{ID: 7, SetID: 11}))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"plan_id":11`) {
		t.Fatalf("test-run response = %s, want plan_id", text)
	}
	if strings.Contains(text, `"set_id"`) {
		t.Fatalf("test-run response leaks legacy set_id: %s", text)
	}

	template, err := json.Marshal(mapTestRunTemplate(&models.TestRunTemplate{ID: 3, SetID: 11}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(template), `"set_id"`) || !strings.Contains(string(template), `"plan_id":11`) {
		t.Fatalf("test-run-template response = %s, want plan_id only", template)
	}
}

func TestOpenAPITestRunContractsUsePlanID(t *testing.T) {
	var spec struct {
		Paths      map[string]json.RawMessage `json:"paths"`
		Schemas    map[string]json.RawMessage `json:"-"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(apispec.V2SpecJSON, &spec); err != nil {
		t.Fatal(err)
	}

	for path, value := range spec.Paths {
		if !strings.Contains(path, "/test-run") {
			continue
		}
		if strings.Contains(string(value), "set_id") {
			t.Errorf("OpenAPI path %s exposes legacy set_id", path)
		}
	}
	for _, name := range []string{"TestRun", "TestRunCreateRequest", "TestRunTemplate", "TestRunTemplateCreateRequest"} {
		value, ok := spec.Components.Schemas[name]
		if !ok {
			t.Errorf("OpenAPI schema %s is missing", name)
			continue
		}
		if strings.Contains(string(value), "set_id") || !strings.Contains(string(value), "plan_id") {
			t.Errorf("OpenAPI schema %s does not use plan_id exclusively", name)
		}
	}
}
