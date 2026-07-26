package circuitplugin

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

func testCircuit() *circuit {
	return &circuit{
		name:             "test",
		failureThreshold: 2,
		initialBackoff:   time.Second,
		maxBackoff:       5 * time.Minute,
		stateGauge:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_state"}),
		backoffGauge:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_backoff"}),
		failureCounter:   prometheus.NewCounter(prometheus.CounterOpts{Name: "test_failures"}),
		bypassCounter:    prometheus.NewCounter(prometheus.CounterOpts{Name: "test_bypass"}),
		logger:           zap.NewNop(),
		jitter:           func(d, _ time.Duration) time.Duration { return d },
	}
}

func TestCircuitRequiresConsecutiveFailuresAndBacksOff(t *testing.T) {
	c := testCircuit()
	p, ok := c.acquire()
	if !ok {
		t.Fatal("closed circuit rejected request")
	}
	c.fail(p, errors.New("first"))
	if c.backoffStep != 0 {
		t.Fatal("single failure opened circuit")
	}

	p, _ = c.acquire()
	c.fail(p, errors.New("second"))
	if c.backoffStep != 1 {
		t.Fatal("second consecutive failure did not open circuit")
	}

	c.retryAt = time.Now().Add(-time.Millisecond)
	p, ok = c.acquire()
	if !ok || !p.probe {
		t.Fatal("half-open probe was not granted")
	}
	c.fail(p, errors.New("probe"))
	if c.backoffStep != 2 {
		t.Fatal("failed probe did not increase backoff")
	}

	c.succeed()
	if c.backoffStep != 0 || c.consecutiveFailures != 0 {
		t.Fatal("success did not reset circuit")
	}
}

func TestExponentialDelayCaps(t *testing.T) {
	if got := exponentialDelay(time.Second, 30*time.Second, 10); got != 30*time.Second {
		t.Fatalf("delay = %s", got)
	}
}
