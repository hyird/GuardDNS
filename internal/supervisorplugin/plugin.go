package supervisorplugin

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/IrineSistiana/mosdns/v5/coremain"
	"github.com/go-chi/chi/v5"
	"github.com/hyird/GuardDNS/internal/statewire"
	"github.com/prometheus/client_golang/prometheus"
)

const pluginType = "guarddns_supervisor"

type Args struct {
	Socket string `yaml:"socket"`
}

type Plugin struct {
	mu         sync.RWMutex
	snapshot   statewire.Snapshot
	receivedAt time.Time
	socketPath string
	conn       *net.UnixConn
	registerer prometheus.Registerer
}

func init() {
	coremain.RegNewPluginFunc(pluginType, initPlugin, func() any { return new(Args) })
}

func initPlugin(bp *coremain.BP, raw any) (any, error) {
	args := raw.(*Args)
	if args.Socket == "" {
		return nil, errors.New("socket is required")
	}
	_ = os.Remove(args.Socket)
	addr, err := net.ResolveUnixAddr("unixgram", args.Socket)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(args.Socket, 0660); err != nil {
		_ = conn.Close()
		return nil, err
	}

	p := &Plugin{
		socketPath: args.Socket,
		conn:       conn,
		registerer: bp.M().GetMetricsReg(),
	}
	if err := p.registerer.Register(p); err != nil {
		_ = conn.Close()
		return nil, err
	}
	mux := chi.NewRouter()
	mux.Get("/healthz", p.health)
	bp.RegAPI(mux)
	go p.readLoop()
	return p, nil
}

func (p *Plugin) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, _, err := p.conn.ReadFromUnix(buf)
		if err != nil {
			return
		}
		var snapshot statewire.Snapshot
		if json.Unmarshal(buf[:n], &snapshot) != nil {
			continue
		}
		p.mu.Lock()
		p.snapshot = snapshot
		p.receivedAt = time.Now()
		p.mu.Unlock()
	}
}

func (p *Plugin) health(w http.ResponseWriter, _ *http.Request) {
	p.mu.RLock()
	snapshot := p.snapshot
	age := time.Since(p.receivedAt)
	p.mu.RUnlock()

	if snapshot.Timestamp == 0 || age > 5*time.Second {
		http.Error(w, "unhealthy: supervisor state is stale", http.StatusServiceUnavailable)
		return
	}
	if mosdns, ok := snapshot.Components["mosdns"]; ok && mosdns.Enabled && !mosdns.Up {
		http.Error(w, "unhealthy: mosdns is restarting", http.StatusServiceUnavailable)
		return
	}
	if bridge, ok := snapshot.Components["doh_bridge"]; ok && bridge.Enabled && !bridge.Up {
		http.Error(w, "unhealthy: encrypted DNS bridge is unavailable", http.StatusServiceUnavailable)
		return
	}
	if unbound, ok := snapshot.Components["unbound"]; ok && unbound.Enabled && !unbound.Up {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("degraded: unbound is restarting\n"))
		return
	}
	// Losing the CN classifier costs mainland names their local answer, not
	// their resolution: they fall through to the encrypted path.
	if cn, ok := snapshot.Components["unbound_recursive"]; ok && cn.Enabled && !cn.Up {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("degraded: CN classification resolver is restarting\n"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (p *Plugin) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc(
		"guarddns_component_up",
		"Whether a supervised GuardDNS component is running.",
		[]string{"component"}, nil,
	)
	ch <- prometheus.NewDesc(
		"guarddns_component_enabled",
		"Whether a supervised GuardDNS component is enabled.",
		[]string{"component"}, nil,
	)
	ch <- prometheus.NewDesc(
		"guarddns_component_restarts_total",
		"Total restarts of a supervised GuardDNS component.",
		[]string{"component"}, nil,
	)
	ch <- prometheus.NewDesc(
		"guarddns_component_backoff_seconds",
		"Current restart delay for a supervised GuardDNS component.",
		[]string{"component"}, nil,
	)
	ch <- prometheus.NewDesc(
		"guarddns_supervisor_state_age_seconds",
		"Age of the last supervisor state update.",
		nil, nil,
	)
}

func (p *Plugin) Collect(ch chan<- prometheus.Metric) {
	p.mu.RLock()
	snapshot := p.snapshot
	receivedAt := p.receivedAt
	p.mu.RUnlock()

	for name, component := range snapshot.Components {
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("guarddns_component_up", "Whether a supervised GuardDNS component is running.", []string{"component"}, nil),
			prometheus.GaugeValue, boolFloat(component.Up), name,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("guarddns_component_enabled", "Whether a supervised GuardDNS component is enabled.", []string{"component"}, nil),
			prometheus.GaugeValue, boolFloat(component.Enabled), name,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("guarddns_component_restarts_total", "Total restarts of a supervised GuardDNS component.", []string{"component"}, nil),
			prometheus.CounterValue, float64(component.Restarts), name,
		)
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("guarddns_component_backoff_seconds", "Current restart delay for a supervised GuardDNS component.", []string{"component"}, nil),
			prometheus.GaugeValue, component.BackoffSeconds, name,
		)
	}
	age := 0.0
	if !receivedAt.IsZero() {
		age = time.Since(receivedAt).Seconds()
	}
	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("guarddns_supervisor_state_age_seconds", "Age of the last supervisor state update.", nil, nil),
		prometheus.GaugeValue, age,
	)
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func (p *Plugin) Close() error {
	p.registerer.Unregister(p)
	err := p.conn.Close()
	_ = os.Remove(p.socketPath)
	return err
}
