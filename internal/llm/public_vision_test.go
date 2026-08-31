package llm

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
)

func TestListEnabledPublic_EmptyListMarshalsAsArray(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/pubempty.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	list, err := NewConnectionManager(db, nil, nil).ListEnabledPublic()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("empty connection list JSON = %s, want []", encoded)
	}
}

// TestListEnabledPublic_ResolvesVision verifies the user-facing connection list
// carries each connection's effective vision capability: the model's catalog
// capability by default, and the per-connection vision_mode override when set.
func TestListEnabledPublic_ResolvesVision(t *testing.T) {
	dsn := fmt.Sprintf("file:%s/pubvision.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	cache := NewModelCache(db)
	if err := cache.SaveSuccess("openrouter", []ModelInfo{
		{ID: "vendor/vision-x", SupportsVision: true},
		{ID: "vendor/text-y", SupportsVision: false},
	}, time.Now()); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if _, err := db.ExecWrite(`INSERT INTO llm_connections(name, provider_type, model, is_enabled, provider_config) VALUES
		('A', 'openrouter', 'vendor/vision-x', TRUE, NULL),
		('B', 'openrouter', 'vendor/text-y',   TRUE, NULL),
		('C', 'openrouter', 'vendor/text-y',   TRUE, '{"vision_mode":"on"}'),
		('D', 'openrouter', 'vendor/vision-x', TRUE, '{"vision_mode":"off"}')`); err != nil {
		t.Fatalf("seed connections: %v", err)
	}

	m := NewConnectionManager(db, nil, nil)
	m.SetModelCache(cache)

	list, err := m.ListEnabledPublic()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]bool{}
	for _, c := range list {
		byName[c.Name] = c.SupportsVision
	}
	want := map[string]bool{
		"A": true,  // catalog vision model, auto
		"B": false, // catalog text model, auto
		"C": true,  // text model, override on
		"D": false, // vision model, override off
	}
	for name, exp := range want {
		if byName[name] != exp {
			t.Errorf("connection %s supports_vision = %v, want %v", name, byName[name], exp)
		}
	}
}
