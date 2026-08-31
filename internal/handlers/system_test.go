//go:build test

package handlers

import (
	"os"
	"testing"
)

func TestTrySendShutdownNeverBlocksWhenChannelIsNotReady(t *testing.T) {
	unbuffered := make(chan os.Signal)
	if trySendShutdown(unbuffered) {
		t.Fatal("trySendShutdown reported delivery to an unread channel")
	}

	buffered := make(chan os.Signal, 1)
	if !trySendShutdown(buffered) {
		t.Fatal("trySendShutdown did not deliver to an available channel")
	}
	if trySendShutdown(buffered) {
		t.Fatal("trySendShutdown blocked or reported delivery to a full channel")
	}
}
