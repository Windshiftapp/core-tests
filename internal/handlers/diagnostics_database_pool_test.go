package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
)

func TestDiagnosticsDatabasePool(t *testing.T) {
	db, err := database.NewSQLiteDBWithPoolSizes("file:diagnostics-pool?mode=memory&cache=shared", 4, 1)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := repository.NewDatabaseDiagnosticsRepository(db)
	repo.SetCapacityBudget(repository.DatabaseCapacityBudget{
		ServerMaxConnections:      100,
		MainConnectionsPerReplica: 4,
		ConnectionsPerReplica:     4,
		ReplicaCount:              1,
		HeadroomConnections:       10,
		RequiredConnections:       14,
		RemainingConnections:      86,
		UtilizationPercent:        14,
		Safe:                      true,
	})
	h := &DiagnosticsHandler{databaseDiagRepo: repo}
	recorder := httptest.NewRecorder()
	h.GetDatabasePool(recorder, httptest.NewRequest("GET", "/api/admin/diagnostics/database-pool", nil))

	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		Pool     DatabasePoolSnapshot           `json:"pool"`
		Pools    []DatabasePoolSnapshot         `json:"pools"`
		Capacity DatabaseCapacityBudgetSnapshot `json:"capacity"`
		Process  DatabaseProcessSnapshot        `json:"process"`
		Instance string                         `json:"instance"`
		Healthy  bool                           `json:"healthy"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Pool.Driver != "sqlite" {
		t.Fatalf("driver = %q, want sqlite", response.Pool.Driver)
	}
	if response.Pool.MaxOpenConnections != 4 {
		t.Fatalf("max open connections = %d, want 4", response.Pool.MaxOpenConnections)
	}
	if response.Pool.SaturationThresholdPct != databasePoolSaturationThresholdPercent {
		t.Fatalf("threshold = %d, want %d", response.Pool.SaturationThresholdPct, databasePoolSaturationThresholdPercent)
	}
	if len(response.Pools) != 1 || response.Pools[0].Name != "main" {
		t.Fatalf("pools = %+v", response.Pools)
	}
	if !response.Capacity.Safe || response.Capacity.RequiredConnections != 14 {
		t.Fatalf("capacity = %+v", response.Capacity)
	}
	if response.Process.Goroutines <= 0 || response.Process.HeapAllocBytes == 0 || response.Instance == "" {
		t.Fatalf("runtime diagnostics missing: process=%+v instance=%q", response.Process, response.Instance)
	}
	if !response.Healthy {
		t.Fatal("healthy = false, want true")
	}
}

func TestDiagnosticsDatabasePoolReportsControlledSaturation(t *testing.T) {
	db, err := database.NewSQLiteDBWithPoolSizes("file:diagnostics-saturation?mode=memory&cache=shared", 1, 1)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	held, err := db.GetDB().Conn(context.Background())
	if err != nil {
		t.Fatalf("hold pool connection: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })
	queryCtx, cancelQuery := context.WithCancel(context.Background())
	t.Cleanup(cancelQuery)
	queryDone := make(chan error, 1)
	go func() {
		rows, queryErr := db.QueryContext(queryCtx, `SELECT 1`)
		if queryErr == nil {
			queryErr = rows.Close()
		}
		queryDone <- queryErr
	}()

	waitDeadline := time.NewTimer(time.Second)
	defer waitDeadline.Stop()
	waitTicker := time.NewTicker(time.Millisecond)
	defer waitTicker.Stop()
	for db.GetDB().Stats().WaitCount == 0 {
		select {
		case <-waitTicker.C:
		case <-waitDeadline.C:
			_ = held.Close()
			t.Fatal("query did not wait for the saturated pool")
		}
	}

	h := &DiagnosticsHandler{databaseDiagRepo: repository.NewDatabaseDiagnosticsRepository(db)}
	recorder := httptest.NewRecorder()
	h.GetDatabasePool(recorder, httptest.NewRequest("GET", "/api/admin/diagnostics/database-pool", nil))

	var response struct {
		Pool    DatabasePoolSnapshot `json:"pool"`
		Healthy bool                 `json:"healthy"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Pool.Saturated || response.Healthy || response.Pool.InUse != 1 || response.Pool.WaitCount == 0 {
		t.Fatalf("saturated response = %+v healthy=%v", response.Pool, response.Healthy)
	}

	if err := held.Close(); err != nil {
		t.Fatalf("release pool connection: %v", err)
	}
	select {
	case err := <-queryDone:
		if err != nil {
			t.Fatalf("waiting query: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting query did not complete after pool release")
	}
}
