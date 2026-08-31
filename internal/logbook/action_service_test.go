package logbook

import (
	"strings"
	"testing"

	"windshift/internal/models"
)

func TestExecuteActionRejectsDisabledAction(t *testing.T) {
	t.Parallel()

	err := (&LogbookActionService{}).executeAction(&models.LogbookAction{ID: 6}, nil)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("error = %v, want disabled", err)
	}
}
