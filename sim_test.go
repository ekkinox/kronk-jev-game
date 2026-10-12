package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSimStopsOnError checks that -sim stops as soon as a model call fails,
// instead of playing on without the model until the time is up.
func TestSimStopsOnError(t *testing.T) {
	calls := 0
	b := &Brain{ask: func(ctx context.Context, s State) (Decision, error) {
		if calls++; calls == 2 {
			return Decision{}, errors.New("boom")
		}
		return Decision{Action: Run, Latency: time.Millisecond}, nil
	}}
	start := time.Now()
	err := runSim(b, MaxSpeed, 1, Easy, 10*time.Second)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("runSim returned %v, want boom", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("runSim returned after %s, want right after the failure", d)
	}
}
