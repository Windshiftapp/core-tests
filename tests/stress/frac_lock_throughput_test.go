package stress

import (
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"windshift/internal/database"
	"windshift/internal/repository"
	tests "windshift/tests"
)

// TestFracIndexAppendThroughputPostgres measures concurrent item-create (append)
// throughput against Postgres. The append path (GenerateFracIndexForNewItem)
// reads MAX(frac_index) and appends a jitter suffix so concurrent appends don't
// collide — with NO global lock. This test guards that the path scales with
// concurrency rather than serializing.
//
// Postgres-only: on SQLite the single global writer lock serializes all writers
// anyway, so concurrency is moot. The other stress tests open NewSQLiteDB
// unconditionally and never exercise this.
//
// For each concurrency level it runs two paths over the same total op count:
//   - PROD:    the real append path (read MAX + jitter, no lock), with the
//     unique-violation retry the create paths use.
//   - CONTROL: the same INSERT with a valid, pre-generated key — no read, no
//     jitter — the lock-free ceiling for raw concurrent inserts.
//
// PROD tracking near CONTROL (and retries staying ~0) is the property we want:
// the jitter approach buys concurrency the old advisory lock prevented.
func TestFracIndexAppendThroughputPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping throughput test in short mode")
	}
	if tests.GetDBType() != "postgres" {
		t.Skip("postgres-only: the append path serializes under SQLite's writer lock regardless")
	}

	server, _ := tests.StartTestServer(t, "postgres")
	tests.CreateBearerToken(t, server)

	// A real workspace gives us a valid FK for items.workspace_id.
	wsResp := tests.MakeAuthRequest(t, server, "POST", "/workspaces", map[string]interface{}{
		"name": "Frac Append Bench",
		"key":  "FAB",
	})
	var ws map[string]interface{}
	tests.DecodeJSON(t, wsResp, &ws)
	wsResp.Body.Close()
	workspaceID := int(ws["id"].(float64))

	// Our own pool with plenty of write connections so concurrency is real and
	// not capped by the server's MaxWriteConns:1 test config.
	const poolConns = 64
	db, err := database.NewPostgresDB(server.DBPath, poolConns)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}
	defer db.Close()

	// Global, monotonically increasing so workspace_item_number stays unique
	// across the whole run. Keeps the per-workspace number lock OUT of the
	// picture so we isolate the frac_index append path.
	var seq int64 = 1000

	concurrencies := []int{1, 2, 4, 8, 16, 32}
	const opsPerLevel = 4000

	t.Logf("Postgres frac_index append throughput (pool=%d, %d ops/level)", poolConns, opsPerLevel)
	t.Logf("%-6s | %-26s | %-22s", "conc", "PROD (read MAX + jitter)", "CONTROL (no lock)")
	t.Logf("-------+----------------------------+----------------------")

	for _, conc := range concurrencies {
		prodTPS, prodRetries := runAppendThroughput(t, db, workspaceID, &seq, conc, opsPerLevel, "prod")
		controlTPS, _ := runAppendThroughput(t, db, workspaceID, &seq, conc, opsPerLevel, "control")

		t.Logf("%-6d | %8.0f ops/sec (%d retries) | %8.0f ops/sec",
			conc, prodTPS, prodRetries, controlTPS)
	}
}

const insertItem = `INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, ?, ?, ?)`

// runAppendThroughput runs `totalOps` inserts across `conc` goroutines and
// returns (ops/sec, total retries) for the timed insert phase. mode is "prod"
// or "control".
func runAppendThroughput(t *testing.T, db database.Database, workspaceID int, seq *int64, conc, totalOps int, mode string) (float64, int64) {
	t.Helper()

	// Control: pre-generate valid, ascending keys (untimed, single-threaded)
	// continuing after the current MAX so concurrent inserts collide neither
	// with each other nor with existing rows, and stay valid order keys.
	var controlKeys []string
	if mode == "control" {
		controlKeys = make([]string, totalOps)
		prev := currentMaxFracIndex(t, db)
		rank, err := repository.ParseGlobalRank(prev)
		if err != nil {
			t.Fatalf("parse current global rank %q: %v", prev, err)
		}
		for i := 0; i < totalOps; i++ {
			fraction, err := repository.KeyBetween(rank.Fraction, "")
			if err != nil {
				t.Fatalf("pre-generate control key %d (prev=%q): %v", i, rank.Fraction, err)
			}
			k, err := repository.EncodeGlobalRank(rank.Bucket, fraction)
			if err != nil {
				t.Fatalf("encode control key %d: %v", i, err)
			}
			controlKeys[i] = k
			rank.Fraction = fraction
		}
	}

	perGoroutine := totalOps / conc
	var wg sync.WaitGroup
	var failed, retries int64
	var errOnce sync.Once
	var firstErr string

	start := time.Now()
	for g := 0; g < conc; g++ {
		base := g * perGoroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				num := atomic.AddInt64(seq, 1)
				var err error
				if mode == "control" {
					err = doAppendControl(db, workspaceID, num, controlKeys[base+i])
				} else {
					var r int
					r, err = doAppendProd(db, workspaceID, num)
					atomic.AddInt64(&retries, int64(r))
				}
				if err != nil {
					atomic.AddInt64(&failed, 1)
					errOnce.Do(func() { firstErr = err.Error() })
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if failed > 0 {
		t.Logf("  (conc=%d mode=%s) %d ops failed; first error: %s", conc, mode, failed, firstErr)
	}
	done := int64(perGoroutine*conc) - failed
	return float64(done) / elapsed.Seconds(), retries
}

// currentMaxFracIndex reads the lexicographically greatest frac_index, or ""
// if the table is empty. Mirrors the read GenerateFracIndexForNewItem does.
func currentMaxFracIndex(t *testing.T, db database.Database) string {
	t.Helper()
	var max sql.NullString
	err := db.GetDB().QueryRow(
		`SELECT frac_index FROM items WHERE frac_index IS NOT NULL ORDER BY frac_index DESC LIMIT 1`,
	).Scan(&max)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("read max frac_index: %v", err)
	}
	return max.String
}

// doAppendProd exercises the real append path (read MAX + jitter, no lock) with
// the same unique-violation retry the create paths use. Returns retry count.
func doAppendProd(db database.Database, workspaceID int, num int64) (int, error) {
	var lastErr error
	for attempt := 0; attempt < repository.FracIndexMaxRetries; attempt++ {
		lastErr = database.WithTx(db, func(tx database.Tx) error {
			key, err := repository.GenerateFracIndexForNewItem(tx)
			if err != nil {
				return err
			}
			_, err = tx.Exec(insertItem, workspaceID, num, "bench", key)
			return err
		})
		if lastErr == nil {
			return attempt, nil
		}
		if !repository.IsFracIndexUniqueViolation(lastErr) {
			return attempt, lastErr
		}
	}
	return repository.FracIndexMaxRetries, lastErr
}

// doAppendControl inserts a pre-generated valid key with no lock and no read —
// the lock-free ceiling for concurrent inserts into items.
func doAppendControl(db database.Database, workspaceID int, num int64, key string) error {
	return database.WithTx(db, func(tx database.Tx) error {
		_, err := tx.Exec(insertItem, workspaceID, num, "bench", key)
		return err
	})
}
