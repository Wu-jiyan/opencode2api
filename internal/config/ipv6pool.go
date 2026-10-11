package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const (
	// DefaultIPv6Interface is assumed when the pool is enabled without naming
	// an interface. A Linux server reaches the internet through eth0 in the
	// overwhelming majority of deployments.
	DefaultIPv6Interface = "eth0"
	// DefaultIPv6PoolCount stays moderate on purpose: every address owns an
	// http.Transport with its own idle connection pool, so the count trades
	// rotation width against file descriptors.
	DefaultIPv6PoolCount = 64
	// DefaultIPv6PoolOffset skips the low host indices, where the address the
	// provider configured on the host and any router reservation live.
	DefaultIPv6PoolOffset = 4096
	// DefaultIPv6ReadyTimeoutSeconds covers duplicate address detection, which
	// takes a couple of seconds per address on a local link.
	DefaultIPv6ReadyTimeoutSeconds = 30
	// MaxIPv6PoolCount bounds the pool. Beyond a few hundred addresses the
	// extra rotation buys little while the transport and idle-connection cost
	// keeps growing linearly, so the ceiling protects the process.
	MaxIPv6PoolCount = 1024
	// IPv6ProxyScheme marks a transport that leaves through a chosen local
	// source address rather than through a proxy server.
	IPv6ProxyScheme = "ipv6"
)

// normalizeIPv6Pool fills defaults and validates the pool settings. The block
// is inert unless enabled, so an unused section never breaks a configuration
// and a client that omits it entirely keeps the previous behavior.
func normalizeIPv6Pool(cfg *Config) error {
	pool := &cfg.IPv6Pool
	pool.Prefix = strings.TrimSpace(pool.Prefix)
	pool.Interface = strings.TrimSpace(pool.Interface)
	if !pool.Enabled {
		return nil
	}
	prefix, err := netip.ParsePrefix(pool.Prefix)
	if err != nil {
		return fmt.Errorf("ipv6_pool.prefix must be an IPv6 CIDR: %w", err)
	}
	if !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
		return errors.New("ipv6_pool.prefix must be an IPv6 prefix")
	}
	prefix = prefix.Masked()
	// A prefix longer than /64 leaves too few bits for the host index the pool
	// walks, and the provider cannot route the result as one on-link block.
	if prefix.Bits() > 64 {
		return errors.New("ipv6_pool.prefix must be /64 or shorter")
	}
	pool.Prefix = prefix.String()
	if pool.Interface == "" {
		pool.Interface = DefaultIPv6Interface
	}
	if pool.Count == 0 {
		pool.Count = DefaultIPv6PoolCount
	}
	if pool.Count < 1 || pool.Count > MaxIPv6PoolCount {
		return fmt.Errorf("ipv6_pool.count must be between 1 and %d", MaxIPv6PoolCount)
	}
	if pool.Offset < 0 {
		return errors.New("ipv6_pool.offset must not be negative")
	}
	// Offset zero names the subnet-router anycast address, which is never a
	// usable source, so an unset (zero) value selects the default instead of
	// being honored. A pool that wants the very start of the prefix is not a
	// configuration that can work.
	if pool.Offset == 0 {
		pool.Offset = DefaultIPv6PoolOffset
	}
	if pool.ReadyTimeoutSeconds < 0 {
		return errors.New("ipv6_pool.ready_timeout_seconds must not be negative")
	}
	if pool.ReadyTimeoutSeconds == 0 {
		pool.ReadyTimeoutSeconds = DefaultIPv6ReadyTimeoutSeconds
	}
	if _, ok := GenerateIPv6Addresses(prefix, pool.Offset, pool.Count); !ok {
		return fmt.Errorf("ipv6_pool.offset %d with count %d does not fit inside %s", pool.Offset, pool.Count, prefix)
	}
	return nil
}

// GenerateIPv6Addresses returns the addresses the pool uses, starting Offset
// host indices into prefix. The choice is deterministic, so a restart reuses
// the same addresses instead of accumulating new ones on the interface. A
// false result means the requested range does not fit inside the prefix.
func GenerateIPv6Addresses(prefix netip.Prefix, offset, count int) ([]netip.Addr, bool) {
	if count < 1 || offset < 0 {
		return nil, false
	}
	current, ok := addHostIndex(prefix.Masked().Addr(), uint64(offset))
	if !ok {
		return nil, false
	}
	addresses := make([]netip.Addr, 0, count)
	for i := 0; i < count; i++ {
		if i > 0 {
			next, ok := addHostIndex(current, 1)
			if !ok {
				return nil, false
			}
			current = next
		}
		if !prefix.Contains(current) || current.Is4In6() {
			return nil, false
		}
		addresses = append(addresses, current)
	}
	return addresses, true
}

// addHostIndex adds a host-index delta to an address, reporting overflow past
// the end of the 128-bit space. The addition is big-endian because the host
// index occupies the low-order bytes.
func addHostIndex(addr netip.Addr, delta uint64) (netip.Addr, bool) {
	octets := addr.As16()
	for i := len(octets) - 1; i >= 0 && delta > 0; i-- {
		sum := uint64(octets[i]) + (delta & 0xff)
		octets[i] = byte(sum)
		delta = (delta >> 8) + (sum >> 8)
	}
	if delta != 0 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom16(octets), true
}

// IPv6ProxyURL renders the transport address the gateway publishes for one
// source address. The scheme keeps these entries distinguishable from proxy
// server URLs in the same list.
func IPv6ProxyURL(addr netip.Addr) string {
	return IPv6ProxyScheme + "://" + addr.String()
}

// ParseIPv6ProxyAddr reports whether raw names a local source address and
// returns that address. Entries may be written by hand in proxies as well as
// generated by the pool, so both paths share this parser.
func ParseIPv6ProxyAddr(raw string) (netip.Addr, bool) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || !strings.EqualFold(strings.TrimSpace(scheme), IPv6ProxyScheme) {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(rest))
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return netip.Addr{}, false
	}
	return addr, true
}
