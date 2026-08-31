//go:build test

package database

import "testing"

func TestIsPostgresDriverUsesCanonicalDatabaseName(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		want   bool
	}{
		{name: "canonical postgres driver", driver: "postgres", want: true},
		{name: "postgresql URL scheme", driver: "postgresql", want: false},
		{name: "sqlite driver", driver: "sqlite", want: false},
		{name: "legacy sqlite spelling", driver: "sqlite3", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsPostgresDriver(test.driver); got != test.want {
				t.Fatalf("IsPostgresDriver(%q) = %v, want %v", test.driver, got, test.want)
			}
		})
	}
}
