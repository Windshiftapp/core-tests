package scm

import (
	"os"
	"testing"

	"windshift/internal/utils"
)

// TestMain keeps the production default of allowing loopback/private dialing
// for the whole scm test package.
// The provider tests drive the real provider HTTP client (newSCMHTTPClient,
// which uses utils.SafeNetDialer) against httptest servers on 127.0.0.1, which
// mirrors self-hosted Gitea / GitHub Enterprise on private networks.
func TestMain(m *testing.M) {
	utils.SetAllowLocalConnections(true)
	os.Exit(m.Run())
}
