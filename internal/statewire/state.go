package statewire

type Component struct {
	Enabled        bool    `json:"enabled"`
	Up             bool    `json:"up"`
	Restarts       uint64  `json:"restarts"`
	BackoffSeconds float64 `json:"backoff_seconds"`
}

type DoHUpstream struct {
	Requests             uint64 `json:"requests"`
	Successes            uint64 `json:"successes"`
	Failures             uint64 `json:"failures"`
	BackoffSkips         uint64 `json:"backoff_skips"`
	DurationMicroseconds uint64 `json:"duration_microseconds"`
	RetryAtUnixMilli     int64  `json:"retry_at_unix_milli"`
	LastSuccessUnix      int64  `json:"last_success_unix"`
	LastFailureUnix      int64  `json:"last_failure_unix"`
}

type Snapshot struct {
	Timestamp    int64                  `json:"timestamp"`
	Components   map[string]Component   `json:"components"`
	DoHUpstreams map[string]DoHUpstream `json:"doh_upstreams,omitempty"`
}
