package supervisorplugin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hyird/GuardDNS/internal/statewire"
)

func TestHealthStates(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   statewire.Snapshot
		receivedAt time.Time
		status     int
		body       string
	}{
		{
			name:   "stale",
			status: http.StatusServiceUnavailable,
			body:   "stale",
		},
		{
			name: "mosdns restarting",
			snapshot: statewire.Snapshot{
				Timestamp: time.Now().Unix(),
				Components: map[string]statewire.Component{
					"mosdns":  {Enabled: true},
					"unbound": {Enabled: true, Up: true},
				},
			},
			receivedAt: time.Now(),
			status:     http.StatusServiceUnavailable,
			body:       "mosdns",
		},
		{
			name: "unbound degraded",
			snapshot: statewire.Snapshot{
				Timestamp: time.Now().Unix(),
				Components: map[string]statewire.Component{
					"mosdns":  {Enabled: true, Up: true},
					"unbound": {Enabled: true},
				},
			},
			receivedAt: time.Now(),
			status:     http.StatusOK,
			body:       "degraded",
		},
		{
			name: "healthy",
			snapshot: statewire.Snapshot{
				Timestamp: time.Now().Unix(),
				Components: map[string]statewire.Component{
					"mosdns":  {Enabled: true, Up: true},
					"unbound": {Enabled: true, Up: true},
				},
			},
			receivedAt: time.Now(),
			status:     http.StatusOK,
			body:       "ok",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugin := &Plugin{snapshot: test.snapshot, receivedAt: test.receivedAt}
			recorder := httptest.NewRecorder()
			plugin.health(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if !strings.Contains(recorder.Body.String(), test.body) {
				t.Fatalf("body = %q, want substring %q", recorder.Body.String(), test.body)
			}
		})
	}
}
