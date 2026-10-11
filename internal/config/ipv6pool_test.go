package config

import (
	"net/netip"
	"testing"
)

func TestGenerateIPv6AddressesWalksTheHostIndex(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8:1234:5600::/56")
	addresses, ok := GenerateIPv6Addresses(prefix, 4096, 3)
	if !ok {
		t.Fatal("GenerateIPv6Addresses reported that the range does not fit")
	}
	// 4096 is 0x1000, so the pool starts at ::1000 and steps by one.
	want := []string{
		"2001:db8:1234:5600::1000",
		"2001:db8:1234:5600::1001",
		"2001:db8:1234:5600::1002",
	}
	if len(addresses) != len(want) {
		t.Fatalf("got %d addresses, want %d", len(addresses), len(want))
	}
	for i, address := range addresses {
		if address.String() != want[i] {
			t.Errorf("address %d = %s, want %s", i, address, want[i])
		}
		if !prefix.Contains(address) {
			t.Errorf("address %d = %s lies outside %s", i, address, prefix)
		}
	}
}

func TestGenerateIPv6AddressesIsDeterministic(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8:1234:5600::/56")
	first, firstOK := GenerateIPv6Addresses(prefix, 4096, 8)
	second, secondOK := GenerateIPv6Addresses(prefix, 4096, 8)
	if !firstOK || !secondOK {
		t.Fatal("the range must fit")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("address %d differs between runs: %s vs %s", i, first[i], second[i])
		}
	}
}

func TestGenerateIPv6AddressesRejectsUnusableRanges(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8:1234:5600::/56")
	if _, ok := GenerateIPv6Addresses(prefix, 0, 0); ok {
		t.Error("a zero count must not produce a range")
	}
	if _, ok := GenerateIPv6Addresses(prefix, -1, 1); ok {
		t.Error("a negative offset must not produce a range")
	}
	// A /120 leaves only eight host bits, so an offset of 4096 steps straight
	// out of the prefix and must be refused rather than silently wrapped.
	narrow := netip.MustParsePrefix("2001:db8:1234:5600::/120")
	if _, ok := GenerateIPv6Addresses(narrow, 4096, 1); ok {
		t.Error("an offset outside a /120 must not produce a range")
	}
	if _, ok := GenerateIPv6Addresses(netip.MustParsePrefix("2001:db8:1234:5600::/64"), 0, 1); !ok {
		t.Error("the base address of a /64 is a usable range")
	}
}

func TestParseIPv6ProxyAddr(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"ipv6://2001:db8:1234:5600::1002", "2001:db8:1234:5600::1002", true},
		{"IPV6://2001:db8:1234:5600::1002", "2001:db8:1234:5600::1002", true},
		{"ipv6://::1", "::1", true},
		{"ipv6://127.0.0.1", "", false},
		{"ipv6://::ffff:127.0.0.1", "", false},
		{"ipv6://", "", false},
		{"ipv6://not-an-address", "", false},
		{"socks5://127.0.0.1:24100", "", false},
		{"http://127.0.0.1:8080", "", false},
		{"direct", "", false},
	}
	for _, testCase := range cases {
		address, ok := ParseIPv6ProxyAddr(testCase.raw)
		if ok != testCase.ok {
			t.Errorf("ParseIPv6ProxyAddr(%q) ok = %v, want %v", testCase.raw, ok, testCase.ok)
			continue
		}
		if ok && address.String() != testCase.want {
			t.Errorf("ParseIPv6ProxyAddr(%q) = %s, want %s", testCase.raw, address, testCase.want)
		}
	}
}

func TestIPv6ProxyURLRoundTrips(t *testing.T) {
	address := netip.MustParseAddr("2001:db8:1234:5600::1002")
	raw := IPv6ProxyURL(address)
	if raw != "ipv6://2001:db8:1234:5600::1002" {
		t.Fatalf("IPv6ProxyURL = %q", raw)
	}
	parsed, ok := ParseIPv6ProxyAddr(raw)
	if !ok || parsed != address {
		t.Fatalf("round trip = %s, %v", parsed, ok)
	}
}

func TestRedactURLPassesALocalSourceThrough(t *testing.T) {
	const raw = "ipv6://2001:db8:1234:5600::1002"
	if got := RedactURL(raw); got != raw {
		t.Errorf("RedactURL(%q) = %q, want it unchanged", raw, got)
	}
}

func TestNormalizeIPv6Pool(t *testing.T) {
	t.Run("a disabled block stays inert", func(t *testing.T) {
		cfg := Config{IPv6Pool: IPv6PoolConfig{}}
		if err := normalizeIPv6Pool(&cfg); err != nil {
			t.Fatalf("a disabled pool must not be validated: %v", err)
		}
	})

	t.Run("an enabled block fills its defaults", func(t *testing.T) {
		cfg := Config{IPv6Pool: IPv6PoolConfig{Enabled: true, Prefix: "2001:db8:1234:5600::/56"}}
		if err := normalizeIPv6Pool(&cfg); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if got := cfg.IPv6Pool.Interface; got != DefaultIPv6Interface {
			t.Errorf("interface = %q, want %q", got, DefaultIPv6Interface)
		}
		if got := cfg.IPv6Pool.Count; got != DefaultIPv6PoolCount {
			t.Errorf("count = %d, want %d", got, DefaultIPv6PoolCount)
		}
		if got := cfg.IPv6Pool.Offset; got != DefaultIPv6PoolOffset {
			t.Errorf("offset = %d, want %d", got, DefaultIPv6PoolOffset)
		}
		if got := cfg.IPv6Pool.ReadyTimeoutSeconds; got != DefaultIPv6ReadyTimeoutSeconds {
			t.Errorf("ready_timeout_seconds = %d, want %d", got, DefaultIPv6ReadyTimeoutSeconds)
		}
	})

	t.Run("the prefix is canonicalized", func(t *testing.T) {
		cfg := Config{IPv6Pool: IPv6PoolConfig{Enabled: true, Prefix: "  2001:db8:1234:5600:0001::/56  "}}
		if err := normalizeIPv6Pool(&cfg); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		// Masking drops the host bits the operator supplied, so the stored
		// prefix always names the block the addresses are drawn from.
		if got := cfg.IPv6Pool.Prefix; got != "2001:db8:1234:5600::/56" {
			t.Errorf("prefix = %q, want %q", got, "2001:db8:1234:5600::/56")
		}
	})

	rejected := map[string]IPv6PoolConfig{
		"an IPv4 prefix":            {Enabled: true, Prefix: "10.0.0.0/8"},
		"a prefix without a length": {Enabled: true, Prefix: "2001:db8:1234:5600::"},
		"an unparsable prefix":      {Enabled: true, Prefix: "not-a-prefix"},
		"an empty prefix":           {Enabled: true, Prefix: ""},
		"a prefix longer than /64":  {Enabled: true, Prefix: "2001:db8:1234:5600::/72"},
		"a negative offset":         {Enabled: true, Prefix: "2001:db8:1234:5600::/56", Offset: -1},
		"a negative ready timeout":  {Enabled: true, Prefix: "2001:db8:1234:5600::/56", ReadyTimeoutSeconds: -1},
	}
	for name, pool := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			cfg := Config{IPv6Pool: pool}
			if err := normalizeIPv6Pool(&cfg); err == nil {
				t.Errorf("normalizeIPv6Pool accepted %+v", pool)
			}
		})
	}

	t.Run("rejects a count above the ceiling", func(t *testing.T) {
		cfg := Config{IPv6Pool: IPv6PoolConfig{Enabled: true, Prefix: "2001:db8:1234:5600::/56", Count: MaxIPv6PoolCount + 1}}
		if err := normalizeIPv6Pool(&cfg); err == nil {
			t.Errorf("a count above %d must be refused", MaxIPv6PoolCount)
		}
	})
}
