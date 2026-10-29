package actioncatalog_test

import (
	"testing"

	"windshift/internal/models"
	"windshift/internal/services/actioncatalog"
)

// Config failures must classify parse errors as invalid_config_json and
// shape errors as invalid_config through the error chain, not by matching
// message prefixes.
func TestValidateClassifiesConfigFailuresByType(t *testing.T) {
	cat := actioncatalog.Default()
	base := actioncatalog.ActionDefinition{
		TriggerType:   models.ActionTriggerManual,
		TriggerConfig: "{}",
		Nodes: []models.ActionNode{
			{ID: 1, NodeType: models.ActionNodeTrigger, NodeConfig: "{}"},
			{ID: 2, NodeType: models.ActionNodeSetField, NodeConfig: `{"field_name":"description","field_display_name":"Description","value":"x","value_display_name":"X"}`},
		},
		Edges: []models.ActionEdge{{SourceNodeID: 1, TargetNodeID: 2, EdgeType: "default"}},
	}

	parseFailure := base
	parseFailure.Nodes[1].NodeConfig = `{"send_notification": `
	errs := actioncatalog.Validate(cat, parseFailure, 0, nil)
	found := false
	for _, validation := range errs {
		if validation.Path == "nodes[1].node_config" && validation.Code == actioncatalog.CodeInvalidConfigJSON {
			found = true
		}
	}
	if !found {
		t.Fatalf("parse failure = %+v, want nodes[1].node_config with code %q", errs, actioncatalog.CodeInvalidConfigJSON)
	}

	shapeFailure := base
	shapeFailure.Nodes[1].NodeConfig = `{"field_name":"description","field_display_name":"Description","value":7,"value_display_name":"X"}`
	errs = actioncatalog.Validate(cat, shapeFailure, 0, nil)
	found = false
	for _, validation := range errs {
		if validation.Path == "nodes[1].node_config" && validation.Code == actioncatalog.CodeInvalidConfig {
			found = true
		}
	}
	if !found {
		t.Fatalf("shape failure = %+v, want nodes[1].node_config with code %q", errs, actioncatalog.CodeInvalidConfig)
	}
}
