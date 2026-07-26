package decisionplugin

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestDecisionCountersShareOneMetricVector(t *testing.T) {
	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("mosdns_", registry)
	cn, err := decisionCounter(registerer, "classifier_cn")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := decisionCounter(registerer, "classifier_non_cn")
	if err != nil {
		t.Fatal(err)
	}
	cn.Inc()
	foreign.Add(2)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 || families[0].GetName() != "mosdns_guarddns_decisions_total" {
		t.Fatalf("metric families = %#v", families)
	}
	if len(families[0].Metric) != 2 {
		t.Fatalf("decision series = %d, want 2", len(families[0].Metric))
	}
}
