package services

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"windshift/internal/repository"
)

type staticDatabasePoolStatsSource struct {
	stats []repository.DatabasePoolStats
}

func (s *staticDatabasePoolStatsSource) PoolStats() []repository.DatabasePoolStats {
	return append([]repository.DatabasePoolStats(nil), s.stats...)
}

func TestEvaluateDatabasePoolSampleAlertsAndRecovers(t *testing.T) {
	t.Parallel()
	config := DefaultDatabasePoolMonitorConfig()
	stats := repository.DatabasePoolStats{Name: "main", MaxOpenConnections: 10, InUse: 9, WaitCount: 20, WaitDurationMillis: 100}

	state, event := evaluateDatabasePoolSample(databasePoolAlertState{}, stats, config)
	if event.kind != "" || event.waitCountDelta != 0 {
		t.Fatalf("initial sample event = %+v", event)
	}
	state, event = evaluateDatabasePoolSample(state, stats, config)
	if event.kind != "saturated" {
		t.Fatalf("second high sample event = %+v, want saturated", event)
	}

	stats.InUse = 5
	state, event = evaluateDatabasePoolSample(state, stats, config)
	if event.kind != "recovered" || state.alerting {
		t.Fatalf("recovery event = %+v state=%+v", event, state)
	}

	stats.WaitCount++
	stats.WaitDurationMillis += 75
	state, event = evaluateDatabasePoolSample(state, stats, config)
	if event.kind != "waiting" || event.waitCountDelta != 1 || event.waitDurationMillisDelta != 75 {
		t.Fatalf("wait event = %+v", event)
	}
}

func TestEvaluateDatabasePoolSampleUsesHysteresis(t *testing.T) {
	t.Parallel()
	config := DefaultDatabasePoolMonitorConfig()
	state := databasePoolAlertState{initialized: true, alerting: true}
	stats := repository.DatabasePoolStats{MaxOpenConnections: 100, InUse: 80}

	state, event := evaluateDatabasePoolSample(state, stats, config)
	if event.kind != "" || !state.alerting {
		t.Fatalf("80%% sample should remain alerting: event=%+v state=%+v", event, state)
	}
	stats.InUse = 75
	state, event = evaluateDatabasePoolSample(state, stats, config)
	if event.kind != "recovered" || state.alerting {
		t.Fatalf("75%% sample should recover: event=%+v state=%+v", event, state)
	}
}

func TestDatabasePoolMonitorEmitsStructuredThresholdEvent(t *testing.T) {
	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	source := &staticDatabasePoolStatsSource{stats: []repository.DatabasePoolStats{{
		Name: "main", Driver: "postgres", MaxOpenConnections: 10, InUse: 9,
	}}}
	monitor := NewDatabasePoolMonitor(source, DefaultDatabasePoolMonitorConfig())
	monitor.sample()
	monitor.sample()

	logs := output.String()
	if !strings.Contains(logs, `"event":"database_pool_saturated"`) ||
		!strings.Contains(logs, `"pool":"main"`) ||
		!strings.Contains(logs, `"utilization_percent":90`) {
		t.Fatalf("structured saturation log missing fields: %s", logs)
	}
}
