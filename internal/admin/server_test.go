package admin

import (
	"encoding/json"
	"testing"

	"opencode2api/internal/config"
)

// TestConfigViewCarriesEveryBlock guards the projection from a resolved
// configuration into the management view.
//
// A block that takes effect in the running service but is left out of this
// projection is invisible to the operator: the console shows the block at its
// zero value, so an active feature reads as disabled. Reading the blocks back
// through JSON is what makes that failure mode visible.
func TestConfigViewCarriesEveryBlock(t *testing.T) {
	cfg := config.Config{
		Vless: config.VlessConfig{Enabled: true, Count: 7, RefreshSeconds: 120, Subscription: "https://example.com/sub"},
		IPv6Pool: config.IPv6PoolConfig{
			Enabled: true, Prefix: "2001:db8:1234:5600::/56", Interface: "eth0",
			Count: 64, Offset: 4096, ManageAddresses: true, ReadyTimeoutSeconds: 30,
		},
	}
	encoded, err := json.Marshal(configViewFrom(cfg, EffectiveView{}, nil))
	if err != nil {
		t.Fatalf("marshal the management view: %v", err)
	}
	var view map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &view); err != nil {
		t.Fatalf("unmarshal the management view: %v", err)
	}

	for _, block := range []string{"vless", "ipv6_pool"} {
		raw, ok := view[block]
		if !ok {
			t.Errorf("the management view does not expose %q at all", block)
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Errorf("decode %q: %v", block, err)
			continue
		}
		if enabled, _ := decoded["enabled"].(bool); !enabled {
			t.Errorf("%q reaches the console disabled although the configuration enables it: %v", block, decoded)
		}
	}

	var pool config.IPv6PoolConfig
	if err := json.Unmarshal(view["ipv6_pool"], &pool); err != nil {
		t.Fatalf("decode ipv6_pool: %v", err)
	}
	if pool != cfg.IPv6Pool {
		t.Errorf("ipv6_pool round trip = %+v, want %+v", pool, cfg.IPv6Pool)
	}
}
