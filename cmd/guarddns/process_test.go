package main

import (
	"testing"
	"time"
)

func TestRestartDelayBoundsAndGrowth(t *testing.T) {
	for step := uint(1); step <= 10; step++ {
		delay := restartDelay(step)
		if delay < 800*time.Millisecond {
			t.Fatalf("step %d delay too short: %s", step, delay)
		}
		if delay > restartMaximum {
			t.Fatalf("step %d delay exceeded maximum: %s", step, delay)
		}
	}
}
