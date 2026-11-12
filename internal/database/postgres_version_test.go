//go:build test

package database

import (
	"strings"
	"testing"
)

// Windshift refuses PostgreSQL servers older than 15 at connection time so an
// unsupported server fails with a clear message instead of a confusing schema
// or migration error.
func TestRequirePostgresVersion(t *testing.T) {
	tests := []struct {
		name       string
		versionNum int
		wantErr    string
	}{
		{name: "15.0 accepted", versionNum: 150000},
		{name: "15.19 accepted", versionNum: 150019},
		{name: "16 accepted", versionNum: 160000},
		{name: "17 accepted", versionNum: 170000},
		{name: "14.24 rejected", versionNum: 140024, wantErr: "PostgreSQL 14.24"},
		{name: "13.0 rejected", versionNum: 130000, wantErr: "PostgreSQL 13.0"},
		{name: "12.0 rejected", versionNum: 120000, wantErr: "PostgreSQL 12.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requirePostgresVersion(tt.versionNum)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("requirePostgresVersion(%d) = %v, want nil", tt.versionNum, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("requirePostgresVersion(%d) = nil, want an error", tt.versionNum)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tt.wantErr)
			}
			if !strings.Contains(err.Error(), "requires PostgreSQL 15 or newer") {
				t.Fatalf("error = %q, want the minimum version stated", err.Error())
			}
		})
	}
}
