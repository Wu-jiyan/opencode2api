package config

import "testing"

// Upstream retired /zen for inference on 2026-10-14 while still serving the
// catalog GET there, so a configuration saved before the move answers 403
// FreeTierError on every free model. Normalize rewrites exactly that one host
// and path, and must leave everything else under the operator's control.
func TestRewriteRetiredZenBase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"retired inference base", "https://opencode.ai/zen", "https://opencode.ai/inference", true},
		{"retired base with trailing slash", "https://opencode.ai/zen/", "https://opencode.ai/inference", true},
		{"case-insensitive host", "https://OpenCode.AI/zen", "https://OpenCode.AI/inference", true},
		{"already migrated", "https://opencode.ai/inference", "https://opencode.ai/inference", false},
		{"go tier keeps its own base", "https://opencode.ai/zen/go", "https://opencode.ai/zen/go", false},
		{"another host is untouched", "https://proxy.example/zen", "https://proxy.example/zen", false},
		{"an operator's own path is untouched", "https://opencode.ai/v1", "https://opencode.ai/v1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewriteRetiredZenBase(tc.in)
			if ok != tc.ok {
				t.Fatalf("rewriteRetiredZenBase(%q) reported ok=%v, want %v", tc.in, ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("rewriteRetiredZenBase(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Normalize is the path both the JSON file and the management API go through, so
// the rewrite has to happen there for an existing deployment to heal itself.
func TestNormalizeMigratesRetiredZenBase(t *testing.T) {
	cfg := Config{
		Listen:      "127.0.0.1:8080",
		Prefer:      TierZen,
		Upstream:    UpstreamConfig{Zen: "https://opencode.ai/zen", Go: "https://opencode.ai/zen/go"},
		Retry:       RetryConfig{MaxAttempts: 3, TimeoutSeconds: 300},
		Models:      ModelsConfig{RefreshSeconds: 300, Protocols: map[string]string{}},
		Performance: PerformanceConfig{MaxIdleConns: 2048, MaxIdleConnsPerHost: 256, IdleConnTimeoutSeconds: 120, ConnectTimeoutSeconds: 5, FailureCooldownSeconds: 15},
		Logging:     LoggingConfig{Level: "info", RingSize: 2000},
		ServerKeys:  []string{"local-key-0123456789"},
		Anonymous:   true,
	}

	normalized, err := Normalize("config.json", cfg)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if normalized.Upstream.Zen != "https://opencode.ai/inference" {
		t.Fatalf("upstream.zen = %q, want https://opencode.ai/inference", normalized.Upstream.Zen)
	}
}
