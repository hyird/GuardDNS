package statewire

type Component struct {
	Enabled        bool    `json:"enabled"`
	Up             bool    `json:"up"`
	Restarts       uint64  `json:"restarts"`
	BackoffSeconds float64 `json:"backoff_seconds"`
}

type Snapshot struct {
	Timestamp  int64                `json:"timestamp"`
	Components map[string]Component `json:"components"`
}
