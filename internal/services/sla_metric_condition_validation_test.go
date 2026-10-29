//go:build test

package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/sla"
	"windshift/internal/testutils"
)

// A metric condition with syntactically valid JSON but the wrong typed shape
// must be rejected before persistence; otherwise the engine cannot compile the
// workspace configuration and every metric in the workspace stops evaluating.
func TestSLAMetricRejectsMalformedConditionConfig(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('SLA condition shape', 'SLAC') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var categoryID int
	if err := db.QueryRow(`INSERT INTO status_categories (name, color, is_completed) VALUES ('Active', '#888888', false) RETURNING id`).Scan(&categoryID); err != nil {
		t.Fatalf("insert status category: %v", err)
	}
	var statusID int
	if err := db.QueryRow(`INSERT INTO statuses (name, category_id) VALUES ('SLAC Open', ?) RETURNING id`, categoryID).Scan(&statusID); err != nil {
		t.Fatalf("insert status: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, status_id, frac_index) VALUES (?, 1, 'Ticket', ?, ?) RETURNING id`,
		workspaceID, statusID, testutils.NextTestFracIndex()).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	repo := repository.NewSLARepository(db)
	calendarID, err := repo.CreateCalendar(context.Background(), &models.WorkingCalendar{
		WorkspaceID: &workspaceID, Name: "Always open", Timezone: "UTC",
		WeeklyIntervals: json.RawMessage(`{"monday":[{"start":"00:00","end":"23:59"}]}`),
		Holidays:        json.RawMessage("[]"),
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}

	engine := sla.NewEngine(db)
	metricService := services.NewSLAMetricService(db, engine)
	validInput := func(name string) services.SLAMetricInput {
		return services.SLAMetricInput{
			Name: name, IsActive: slaConditionBoolPointer(true),
			Conditions: []models.SLACondition{{
				Phase: models.SLAPhaseStart, ConditionType: "status_current",
				Config: json.RawMessage(`{"status_ids":[` + strconv.Itoa(statusID) + `]}`),
			}},
			Goals: []models.SLAGoal{{Position: 0, QLQuery: "1 = 1", Targets: []models.SLAGoalTarget{{
				Position: 0, TargetMs: 3_600_000, CalendarID: calendarID,
			}}}},
		}
	}
	validMetric, err := metricService.Create(context.Background(), workspaceID, validInput("Valid metric"))
	if err != nil {
		t.Fatalf("create valid metric: %v", err)
	}
	if err := engine.RecalculateItem(context.Background(), itemID); err != nil {
		t.Fatalf("recalculate with valid config: %v", err)
	}

	malformed := validInput("Malformed metric")
	malformed.Conditions = []models.SLACondition{{
		Phase: models.SLAPhaseStart, ConditionType: "status_current",
		Config: json.RawMessage(`{"status_ids":"invalid"}`),
	}}

	if _, err := metricService.Create(context.Background(), workspaceID, malformed); !errors.Is(err, services.ErrSLAMetricInvalid) {
		t.Fatalf("Create with malformed condition config error = %v, want ErrSLAMetricInvalid", err)
	}
	var metricCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sla_metrics WHERE workspace_id = ?`, workspaceID).Scan(&metricCount); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if metricCount != 1 {
		t.Fatalf("metrics after rejected create = %d, want 1", metricCount)
	}
	if err := engine.RecalculateItem(context.Background(), itemID); err != nil {
		t.Fatalf("valid metrics must still evaluate after a rejected create: %v", err)
	}

	if _, err := metricService.Update(context.Background(), workspaceID, validMetric.ID, malformed); !errors.Is(err, services.ErrSLAMetricInvalid) {
		t.Fatalf("Update with malformed condition config error = %v, want ErrSLAMetricInvalid", err)
	}
	var storedConfig []byte
	if err := db.QueryRow(`SELECT config FROM sla_metric_conditions WHERE metric_id = ?`, validMetric.ID).Scan(&storedConfig); err != nil {
		t.Fatalf("load stored condition config: %v", err)
	}
	if string(storedConfig) != `{"status_ids":[`+strconv.Itoa(statusID)+`]}` {
		t.Fatalf("stored condition config after rejected update = %s, want unchanged valid config", storedConfig)
	}
	if err := engine.RecalculateItem(context.Background(), itemID); err != nil {
		t.Fatalf("valid metrics must still evaluate after a rejected update: %v", err)
	}
}

func slaConditionBoolPointer(value bool) *bool { return &value }
