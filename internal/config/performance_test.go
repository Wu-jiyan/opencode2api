package config

import (
	"testing"
	"time"
)

// The first-byte bound is what keeps a silent exit from holding a request for
// the whole request budget, so its default has to be bounded and the operator
// value has to win over it — while never exceeding the request budget itself.
func TestFirstByteTimeout(t *testing.T) {
	cases := []struct {
		name      string
		seconds   int
		request   time.Duration
		want      time.Duration
		wantError bool
	}{
		{name: "unset selects the bounded default", seconds: 0, request: 30 * time.Minute, want: defaultFirstByteTimeout},
		{name: "an explicit value wins", seconds: 90, request: 30 * time.Minute, want: 90 * time.Second},
		{name: "the request budget caps it", seconds: 600, request: 120 * time.Second, want: 120 * time.Second},
		{name: "an unlimited request budget does not cap it", seconds: 600, request: 0, want: 600 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := PerformanceConfig{FirstByteTimeoutSeconds: tc.seconds}
			if got := cfg.FirstByteTimeout(tc.request); got != tc.want {
				t.Fatalf("FirstByteTimeout = %s, want %s", got, tc.want)
			}
		})
	}
}

// A negative value is a configuration mistake, not a way to turn the bound off:
// the default is already bounded, and an unbounded wait is what this bound
// exists to prevent.
func TestFirstByteTimeoutMustNotBeNegative(t *testing.T) {
	cfg := Config{
		Listen:   "127.0.0.1:8080",
		Prefer:   TierZen,
		Upstream: UpstreamConfig{Zen: "https://opencode.ai/inference", Go: "https://opencode.ai/zen/go"},
		Retry:    RetryConfig{MaxAttempts: 3, TimeoutSeconds: 300},
		Models:   ModelsConfig{RefreshSeconds: 300, Protocols: map[string]string{}},
		Performance: PerformanceConfig{
			MaxIdleConns: 2048, MaxIdleConnsPerHost: 256, IdleConnTimeoutSeconds: 120,
			ConnectTimeoutSeconds: 5, FailureCooldownSeconds: 15, FirstByteTimeoutSeconds: -1,
		},
		Logging:    LoggingConfig{Level: "info", RingSize: 2000},
		ServerKeys: []string{"local-key-0123456789"},
		Anonymous:  true,
	}
	if _, err := Normalize("config.json", cfg); err == nil {
		t.Fatal("a negative first_byte_timeout_seconds must be rejected")
	}
}
