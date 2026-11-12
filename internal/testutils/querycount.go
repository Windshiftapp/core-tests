//go:build test

package testutils

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"

	"windshift/internal/database"
)

// QueryCountingDB wraps a database.Database and records read queries issued
// through it. Tests use it to assert that a batched path issues a constant
// number of queries as its input grows, and to prove a specific per-row query
// is gone. Writes (Exec) are not counted, so fixture setup does not pollute the
// measurement.
type QueryCountingDB struct {
	database.Database
	queries atomic.Int64
	mu      sync.Mutex
	texts   []string
}

// CountQueries wraps db so Query, QueryRow, QueryContext and QueryRowContext
// calls are counted and recorded.
func CountQueries(db database.Database) *QueryCountingDB {
	return &QueryCountingDB{Database: db}
}

// Queries returns the number of read queries issued so far.
func (c *QueryCountingDB) Queries() int64 { return c.queries.Load() }

// QueryTexts returns the SQL text of every read query issued so far.
func (c *QueryCountingDB) QueryTexts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// CountQueriesContaining returns how many recorded queries contain substr.
func (c *QueryCountingDB) CountQueriesContaining(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, text := range c.texts {
		if strings.Contains(text, substr) {
			count++
		}
	}
	return count
}

// ResetQueries zeroes the counter and drops the recorded texts, so a test can
// measure one call at a time.
func (c *QueryCountingDB) ResetQueries() {
	c.mu.Lock()
	c.texts = nil
	c.mu.Unlock()
	c.queries.Store(0)
}

func (c *QueryCountingDB) record(query string) {
	c.queries.Add(1)
	c.mu.Lock()
	c.texts = append(c.texts, query)
	c.mu.Unlock()
}

func (c *QueryCountingDB) Query(query string, args ...any) (*sql.Rows, error) {
	c.record(query)
	return c.Database.Query(query, args...)
}

func (c *QueryCountingDB) QueryRow(query string, args ...any) *sql.Row {
	c.record(query)
	return c.Database.QueryRow(query, args...)
}

func (c *QueryCountingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.record(query)
	return c.Database.QueryContext(ctx, query, args...)
}

func (c *QueryCountingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.record(query)
	return c.Database.QueryRowContext(ctx, query, args...)
}
