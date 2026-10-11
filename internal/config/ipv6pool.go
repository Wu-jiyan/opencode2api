package config

import (
	"errors"
	"fmt"
	"math/big"
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

	// SpreadSubnet puts one address in each /64 before adding a second address
	// to any of them.
	//
	// Rate limiters key on the /64 rather than on the individual address,
	// because a /64 is the smallest block a provider hands out and the
	// smallest unit a client is expected to own. Measured against OpenCode
	// Zen: 256 addresses packed into one /64 shared a single allowance, while
	// the same addresses placed in separate /64s were metered separately — a
	// fresh /64 answered 200 while the hammered one returned 429. This is the
	// default because it is the layout that widens the budget; packing the
	// addresses together leaves the pool looking like one client.
	SpreadSubnet = "subnet"
	// SpreadAddress packs the addresses consecutively inside a single /64.
	// Choose it when the limiter really is per address, or when the prefix is
	// itself a /64 and there is nowhere to spread.
	SpreadAddress = "address"
	// DefaultIPv6PoolSpread is the layout used when none is configured.
	DefaultIPv6PoolSpread = SpreadSubnet
)

// normalizeIPv6Pool fills defaults and validates the pool settings. The block
// is inert unless enabled, so an unused section never breaks a configuration
// and a client that omits it entirely keeps the previous behavior.
func normalizeIPv6Pool(cfg *Config) error {
	pool := &cfg.IPv6Pool
	pool.Prefix = strings.TrimSpace(pool.Prefix)
	pool.Interface = strings.TrimSpace(pool.Interface)
	pool.Spread = strings.ToLower(strings.TrimSpace(pool.Spread))
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
	if pool.Spread == "" {
		pool.Spread = DefaultIPv6PoolSpread
	}
	if pool.Spread != SpreadSubnet && pool.Spread != SpreadAddress {
		return fmt.Errorf("ipv6_pool.spread must be %q or %q", SpreadSubnet, SpreadAddress)
	}
	if pool.ReadyTimeoutSeconds < 0 {
		return errors.New("ipv6_pool.ready_timeout_seconds must not be negative")
	}
	if pool.ReadyTimeoutSeconds == 0 {
		pool.ReadyTimeoutSeconds = DefaultIPv6ReadyTimeoutSeconds
	}
	if _, ok := GenerateIPv6Addresses(prefix, pool.Offset, pool.Count, pool.Spread); !ok {
		return fmt.Errorf("ipv6_pool.offset %d with count %d does not fit inside %s", pool.Offset, pool.Count, prefix)
	}
	return nil
}

// GenerateIPv6Addresses returns the addresses the pool uses for the requested
// layout. The choice is deterministic, so a restart reuses the same addresses
// instead of accumulating new ones on the interface. A false result means the
// requested range does not fit inside the prefix.
//
// With SpreadSubnet the addresses walk the /64 blocks the prefix contains, so
// a /56 with count 256 yields one address in each of its 256 blocks; beyond
// that, a second address lands in each block. With SpreadAddress they are
// consecutive inside the first block.
func GenerateIPv6Addresses(prefix netip.Prefix, offset, count int, spread string) ([]netip.Addr, bool) {
	if count < 1 || offset < 0 {
		return nil, false
	}
	blocks := subnetBlocks(prefix, count)
	blockStride := new(big.Int).Lsh(big.NewInt(1), 64)
	baseOctets := prefix.Masked().Addr().As16()
	base := new(big.Int).SetBytes(baseOctets[:])

	addresses := make([]netip.Addr, 0, count)
	for i := 0; i < count; i++ {
		blockIndex, hostIndex := int64(0), int64(offset+i)
		if spread == SpreadSubnet {
			blockIndex, hostIndex = int64(i%blocks), int64(offset+i/blocks)
		}
		value := new(big.Int).Add(base, new(big.Int).Mul(big.NewInt(blockIndex), blockStride))
		value.Add(value, big.NewInt(hostIndex))
		if value.BitLen() > 128 {
			return nil, false
		}
		var octets [16]byte
		value.FillBytes(octets[:])
		address := netip.AddrFrom16(octets)
		if !prefix.Contains(address) || address.Is4In6() {
			return nil, false
		}
		addresses = append(addresses, address)
	}
	return addresses, true
}

// subnetBlocks counts how many /64 blocks the subnet layout can spread over.
// A /64 prefix contains exactly one, and the count caps the value so the walk
// never needs more blocks than there are addresses to place.
func subnetBlocks(prefix netip.Prefix, count int) int {
	subnetBits := 64 - prefix.Bits()
	if subnetBits <= 0 {
		return 1
	}
	if subnetBits >= 31 {
		// Far more blocks than the pool can use; the count bounds the index and
		// keeps the shift in range.
		return count
	}
	if capacity := 1 << subnetBits; capacity < count {
		return capacity
	}
	return count
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
