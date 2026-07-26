package main

import (
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/hyird/GuardDNS/internal/statewire"
)

type runtimeState struct {
	mu         sync.RWMutex
	components map[string]statewire.Component
}

func newRuntimeState() *runtimeState {
	return &runtimeState{components: map[string]statewire.Component{
		"doh_bridge":        {Enabled: true},
		"mosdns":            {Enabled: true},
		"unbound":           {Enabled: true},
		"unbound_recursive": {Enabled: true},
	}}
}

func (s *runtimeState) update(name string, fn func(*statewire.Component)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	component := s.components[name]
	fn(&component)
	s.components[name] = component
}

func (s *runtimeState) snapshot() statewire.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	components := make(map[string]statewire.Component, len(s.components))
	for name, component := range s.components {
		components[name] = component
	}
	return statewire.Snapshot{
		Timestamp:  time.Now().Unix(),
		Components: components,
	}
}

func sendStateLoop(ctx doneContext, state *runtimeState, socketPath string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		sendState(state, socketPath)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func sendState(state *runtimeState, socketPath string) {
	payload, err := json.Marshal(state.snapshot())
	if err != nil {
		return
	}
	addr, err := net.ResolveUnixAddr("unixgram", socketPath)
	if err != nil {
		return
	}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		return
	}
	_, _ = conn.Write(payload)
	_ = conn.Close()
}

type doneContext interface {
	Done() <-chan struct{}
}
