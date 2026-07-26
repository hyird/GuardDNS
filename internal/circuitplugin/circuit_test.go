package circuitplugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IrineSistiana/mosdns/v5/pkg/query_context"
	"github.com/IrineSistiana/mosdns/v5/plugin/executable/sequence"
	"github.com/miekg/dns"
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

func TestLostAttemptIsRetriedBeforeDowngradingRouting(t *testing.T) {
	c := testCircuit()
	c.failureThreshold = 1
	var calls atomic.Int32
	p := &Plugin{
		primary: sequence.ExecutableFunc(func(_ context.Context, qCtx *query_context.Context) error {
			// Model a dropped datagram: the first attempt outlives its budget.
			if calls.Add(1) == 1 {
				time.Sleep(200 * time.Millisecond)
				return nil
			}
			response := new(dns.Msg)
			response.SetReply(qCtx.Q())
			qCtx.SetResponse(response)
			return nil
		}),
		circuit:        c,
		attempts:       3,
		attemptTimeout: 10 * time.Millisecond,
		failureRCodes:  map[int]struct{}{},
	}
	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)
	qCtx := query_context.NewContext(query)

	if err := p.Exec(context.Background(), qCtx); err != nil {
		t.Fatalf("Exec error = %v, want nil", err)
	}
	if qCtx.R() == nil {
		t.Fatal("retry did not deliver the fake-IP response")
	}
	if c.backoffStep != 0 || c.consecutiveFailures != 0 {
		t.Fatal("a retried attempt counted as a circuit failure")
	}
}

func TestAllAttemptsExhaustedStillFailsTheCircuit(t *testing.T) {
	c := testCircuit()
	c.failureThreshold = 1
	var calls atomic.Int32
	p := &Plugin{
		primary: sequence.ExecutableFunc(func(_ context.Context, _ *query_context.Context) error {
			calls.Add(1)
			return errors.New("upstream down")
		}),
		circuit:        c,
		attempts:       3,
		attemptTimeout: time.Second,
		failureRCodes:  map[int]struct{}{},
	}
	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)

	if err := p.Exec(context.Background(), query_context.NewContext(query)); err == nil {
		t.Fatal("exhausted attempts returned success")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if c.backoffStep != 1 {
		t.Fatal("exhausted attempts did not open the circuit")
	}
}

func TestExponentialDelayCaps(t *testing.T) {
	if got := exponentialDelay(time.Second, 30*time.Second, 10); got != 30*time.Second {
		t.Fatalf("delay = %s", got)
	}
}

func TestAttemptTimeoutOpensCircuitWithoutLateResponseMutation(t *testing.T) {
	c := testCircuit()
	c.failureThreshold = 1
	workerFinished := make(chan struct{})
	p := &Plugin{
		primary: sequence.ExecutableFunc(func(_ context.Context, qCtx *query_context.Context) error {
			defer close(workerFinished)
			time.Sleep(30 * time.Millisecond)
			response := new(dns.Msg)
			response.SetReply(qCtx.Q())
			response.Answer = append(response.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: qCtx.Q().Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET},
			})
			qCtx.SetResponse(response)
			return nil
		}),
		circuit:        c,
		attemptTimeout: time.Millisecond,
		failureRCodes:  map[int]struct{}{},
	}
	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)
	qCtx := query_context.NewContext(query)

	err := p.Exec(context.Background(), qCtx)
	if !errors.Is(err, errAttemptTimeout) {
		t.Fatalf("Exec error = %v, want %v", err, errAttemptTimeout)
	}
	if qCtx.R() != nil {
		t.Fatal("timed-out attempt mutated caller response")
	}
	if c.backoffStep != 1 {
		t.Fatal("timed-out attempt did not open circuit")
	}
	<-workerFinished
	if qCtx.R() != nil {
		t.Fatal("late upstream response mutated caller response")
	}
}
