package handlers

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRunnerInstanceRateLimiterIsolatesRunnerBudgets(t *testing.T) {
	limiter := newRunnerInstanceRateLimiter(rate.Limit(0), 2)

	if !limiter.Allow(11) || !limiter.Allow(11) {
		t.Fatal("runner 11 exhausted its budget before the configured burst")
	}
	if limiter.Allow(11) {
		t.Fatal("runner 11 exceeded its configured burst")
	}
	if !limiter.Allow(22) {
		t.Fatal("runner 11 consumed runner 22's independent budget")
	}
}

func TestRunnerInstanceRateLimiterForgetsInactiveRunners(t *testing.T) {
	now := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	limiter := newRunnerInstanceRateLimiter(rate.Limit(0), 1)
	limiter.now = func() time.Time { return now }

	if !limiter.Allow(11) || limiter.Allow(11) {
		t.Fatal("runner 11 did not exhaust its initial budget")
	}
	now = now.Add(runnerLimiterEntryTTL + time.Second)
	if !limiter.Allow(22) {
		t.Fatal("runner 22 should receive an independent budget")
	}
	if !limiter.Allow(11) {
		t.Fatal("inactive runner 11 retained an expired budget")
	}
}
