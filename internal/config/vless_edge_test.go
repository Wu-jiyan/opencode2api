package config

import (
	"fmt"
	"testing"
)

// edge_ips fans one fixed link out across anycast entry addresses, so a typo
// there would look like a flaky worker rather than a bad address.
func TestNormalizeVlessEdgeIPs(t *testing.T) {
	base := func(edges ...string) Config {
		return Config{
			Vless: VlessConfig{
				Enabled:               true,
				Subscription:          "https://example.com/subscription",
				EdgeIPs:               edges,
				Count:                 2,
				RefreshSeconds:        300,
				RotateBatch:           1,
				PortBase:              24000,
				Host:                  "127.0.0.1",
				StartupTimeoutSeconds: 15,
			},
		}
	}

	cfg := base(" 198.51.100.7 ", "198.51.100.7", "2001:db8::7")
	if err := normalizeVless(&cfg); err != nil {
		t.Fatalf("normalizeVless: %v", err)
	}
	if len(cfg.Vless.EdgeIPs) != 2 {
		t.Fatalf("edge_ips = %v, want the duplicate dropped and both families kept", cfg.Vless.EdgeIPs)
	}
	if cfg.Vless.EdgeIPs[0] != "198.51.100.7" {
		t.Fatalf("edge_ips[0] = %q, want the value trimmed", cfg.Vless.EdgeIPs[0])
	}

	bad := base("edge.example.com")
	if err := normalizeVless(&bad); err == nil {
		t.Fatal("a hostname in edge_ips must be rejected")
	}

	// A disabled pool is inert: the same typo must not break a configuration
	// that never turns the feature on.
	off := base("edge.example.com")
	off.Vless.Enabled = false
	if err := normalizeVless(&off); err != nil {
		t.Fatalf("a disabled pool must not validate edge_ips: %v", err)
	}

	many := make([]string, 0, maxVlessEdgeIPs+1)
	for i := 0; i <= maxVlessEdgeIPs; i++ {
		many = append(many, fmt.Sprintf("198.51.100.%d", i+1))
	}
	if err := normalizeVless(func() *Config { c := base(many...); return &c }()); err == nil {
		t.Fatalf("more than %d edge addresses must be rejected", maxVlessEdgeIPs)
	}
}
