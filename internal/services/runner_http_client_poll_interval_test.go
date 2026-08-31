package services

import (
	"testing"
	"time"
)

func TestHTTPOrchestratorClientPollInterval(t *testing.T) {
	t.Run("uses default", func(t *testing.T) {
		client := &HTTPOrchestratorClient{}
		if got := client.pollInterval(); got != 10*time.Second {
			t.Fatalf("pollInterval() = %s, want 10s", got)
		}
	})

	t.Run("uses configured interval", func(t *testing.T) {
		client := &HTTPOrchestratorClient{PollInterval: 3 * time.Second}
		if got := client.pollInterval(); got != 3*time.Second {
			t.Fatalf("pollInterval() = %s, want 3s", got)
		}
	})
}
