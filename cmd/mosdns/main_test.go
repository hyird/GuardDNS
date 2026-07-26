package main

import (
	"testing"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/IrineSistiana/mosdns/v5/plugin/executable/sequence"
	"github.com/IrineSistiana/mosdns/v5/plugin/server/tcp_server"
)

func TestGuardDNSPluginsInitializeTogether(t *testing.T) {
	entry := sequence.Args{
		{Exec: "guarddns_metrics_collector main"},
		{Exec: "guarddns_decision first"},
		{Exec: "guarddns_decision second"},
		{Exec: "reject 0"},
	}
	secureEntry := sequence.Args{
		{Exec: "guarddns_metrics_collector secure"},
		{Exec: "guarddns_decision secure"},
		{Exec: "reject 0"},
	}
	cfg := &coremain.Config{Plugins: []coremain.PluginConfig{
		{Tag: "entry", Type: "sequence", Args: &entry},
		{Tag: "secure_entry", Type: "sequence", Args: &secureEntry},
		{
			Tag:  "main_tcp",
			Type: "guarddns_tcp_server",
			Args: &tcp_server.Args{
				Entry:  "entry",
				Listen: "127.0.0.1:0",
			},
		},
		{
			Tag:  "secure_tcp",
			Type: "guarddns_tcp_server",
			Args: &tcp_server.Args{
				Entry:  "secure_entry",
				Listen: "127.0.0.1:0",
			},
		},
	}}
	instance, err := coremain.NewMosdns(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instance.CloseWithErr(nil)
	if err := instance.GetSafeClose().WaitClosed(); err != nil {
		t.Fatal(err)
	}
}
