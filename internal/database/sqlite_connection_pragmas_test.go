//go:build test

package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSQLiteReplacementConnectionsPreserveRequiredPragmas(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "connections.db"), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(); _ = db.writeConn.Close() })
	for name, pool := range map[string]*sql.DB{"read": db.DB, "write": db.writeConn} {
		t.Run(name, func(t *testing.T) {
			pool.SetMaxIdleConns(0)
			conn, err := pool.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var enabled int
			if err := conn.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&enabled); err != nil {
				t.Fatal(err)
			}
			if enabled != 1 {
				t.Fatalf("replacement connection foreign_keys = %d, want 1", enabled)
			}
			var busyTimeout int
			if err := conn.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
				t.Fatal(err)
			}
			if busyTimeout != 5000 {
				t.Fatalf("replacement connection busy_timeout = %d, want 5000", busyTimeout)
			}
		})
	}
}
