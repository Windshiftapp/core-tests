package database

import "testing"

func countRows(t *testing.T, db Database, query string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}
