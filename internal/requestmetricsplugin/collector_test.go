package requestmetricsplugin

import (
	"context"
	"errors"
	"testing"

	"github.com/IrineSistiana/mosdns/v5/pkg/query_context"
	"github.com/IrineSistiana/mosdns/v5/plugin/executable/sequence"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

func TestCollectorSeparatesCancellationFromErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := newCollector(registry, "main")
	if err != nil {
		t.Fatal(err)
	}
	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)

	canceled := sequence.NewChainWalker([]*sequence.ChainNode{{
		E: sequence.ExecutableFunc(func(context.Context, *query_context.Context) error {
			return errors.New("connection ctx canceled")
		}),
	}}, nil)
	if err := metrics.Exec(context.Background(), query_context.NewContext(query), canceled); err == nil {
		t.Fatal("cancellation error was swallowed")
	}

	failed := sequence.NewChainWalker([]*sequence.ChainNode{{
		E: sequence.ExecutableFunc(func(context.Context, *query_context.Context) error {
			return errors.New("upstream failure")
		}),
	}}, nil)
	if err := metrics.Exec(context.Background(), query_context.NewContext(query), failed); err == nil {
		t.Fatal("upstream error was swallowed")
	}

	if got := gatheredCounterValue(t, registry, "query_total"); got != 2 {
		t.Fatalf("query_total = %v, want 2", got)
	}
	if got := gatheredCounterValue(t, registry, "canceled_total"); got != 1 {
		t.Fatalf("canceled_total = %v, want 1", got)
	}
	if got := gatheredCounterValue(t, registry, "err_total"); got != 1 {
		t.Fatalf("err_total = %v, want 1", got)
	}
}

func gatheredCounterValue(t *testing.T, registry *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name && len(family.Metric) == 1 {
			return family.Metric[0].GetCounter().GetValue()
		}
	}
	t.Fatalf("counter %s was not gathered", name)
	return 0
}
