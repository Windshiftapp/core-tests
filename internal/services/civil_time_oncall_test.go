package services

import (
	"testing"
	"time"
)

func TestOnCallHandoffBoundaryDoesNotCrossCivilDate(t *testing.T) {
	location, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Fatalf("load Pacific/Apia: %v", err)
	}
	date := time.Date(2011, time.December, 30, 0, 0, 0, 0, time.UTC)

	if boundary, err := onCallHandoffBoundary(date, "23:59", location); err == nil {
		t.Fatalf("boundary = %s, want error for skipped civil date", boundary)
	}
}
