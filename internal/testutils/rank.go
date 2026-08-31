//go:build test

package testutils

import (
	"fmt"
	"sync/atomic"
)

// rankCounter provides unique ranks for direct-SQL test fixtures.
var rankCounter int64

// NextTestFracIndex returns a unique, deterministic canonical rank suitable for
// direct item inserts that intentionally bypass the production creation path.
func NextTestFracIndex() string {
	n := atomic.AddInt64(&rankCounter, 1) - 1
	return fmt.Sprintf("0|a0%016X1", n)
}
