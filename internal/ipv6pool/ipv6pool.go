// Package ipv6pool owns the set of local IPv6 source addresses the gateway
// uses as exits. A provider that routes an on-link prefix to the host makes
// every address inside it a valid source for an outbound connection, and the
// upstream sees each one as a different client. That widens the rotation
// without a proxy process, a tunnel handshake, or a subscription that expires.
package ipv6pool

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"opencode2api/internal/config"
)

// settleInterval is how often the pool re-probes addresses that duplicate
// address detection has not released yet.
const settleInterval = 300 * time.Millisecond

// Pool holds the addresses drawn from the configured prefix and the subset
// that is currently usable as a source.
type Pool struct {
	cfg    config.IPv6PoolConfig
	logger *slog.Logger

	mu        sync.RWMutex
	addresses []netip.Addr
	ready     []string
}

// New returns an inert pool when the configuration block is disabled, so the
// gateway can hold one unconditionally.
func New(cfg config.IPv6PoolConfig, logger *slog.Logger) *Pool {
	return &Pool{cfg: cfg, logger: logger}
}

// Enabled reports whether the pool contributes exits.
func (p *Pool) Enabled() bool {
	return p != nil && p.cfg.Enabled
}

// Proxies returns the transport addresses for the usable exits. It is empty
// before Prepare has run, which keeps an unprepared pool from widening the
// desired list with addresses that cannot be bound.
func (p *Pool) Proxies() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.ready...)
}

// Addresses returns every address the pool owns, including any that never
// became usable.
func (p *Pool) Addresses() []netip.Addr {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]netip.Addr(nil), p.addresses...)
}

// Prepare assigns the addresses when the pool manages them and waits for the
// kernel to accept each one as a local endpoint. It must run before any
// transport is built on the result: binding a tentative address fails with
// "cannot assign requested address", so a transport created too early would be
// dead on arrival.
//
// A partially ready pool is usable and kept; a pool that yields no usable
// address at all is an error, because silently falling back to the direct or
// vless route would hide a misconfiguration behind working traffic.
func (p *Pool) Prepare(ctx context.Context) error {
	if p == nil || !p.cfg.Enabled {
		return nil
	}
	prefix, err := netip.ParsePrefix(p.cfg.Prefix)
	if err != nil {
		return fmt.Errorf("ipv6 pool prefix: %w", err)
	}
	addresses, ok := config.GenerateIPv6Addresses(prefix, p.cfg.Offset, p.cfg.Count)
	if !ok {
		return fmt.Errorf("ipv6 pool range starting at offset %d with count %d does not fit inside %s", p.cfg.Offset, p.cfg.Count, prefix)
	}
	p.mu.Lock()
	p.addresses = addresses
	p.mu.Unlock()

	if p.cfg.ManageAddresses {
		if err := assign(ctx, p.cfg.Interface, prefix, addresses, p.logger); err != nil {
			return err
		}
	}
	ready := p.collectReady(ctx, addresses)
	p.mu.Lock()
	p.ready = ready
	p.mu.Unlock()

	if len(ready) == 0 {
		return fmt.Errorf("ipv6 pool produced no usable address on %s: none of the %d addresses in %s could be bound (check the interface, the prefix, and whether manage_addresses may assign them)", p.cfg.Interface, len(addresses), prefix)
	}
	if p.logger != nil {
		p.logger.Info("IPv6 source pool ready", "component", "proxy", "event", "ipv6_pool_ready",
			"interface", p.cfg.Interface, "prefix", prefix.String(), "requested", len(addresses), "ready", len(ready))
	}
	return nil
}

// collectReady waits until every address can be bound, or until the readiness
// budget runs out, and returns the ones that made it. Addresses still tentative
// when the budget expires are dropped individually rather than failing the
// pool, so one slow address never costs the whole rotation.
func (p *Pool) collectReady(ctx context.Context, addresses []netip.Addr) []string {
	budget := time.Duration(p.cfg.ReadyTimeoutSeconds) * time.Second
	deadline := time.Now().Add(budget)
	pending := append([]netip.Addr(nil), addresses...)
	ready := make([]string, 0, len(addresses))
	for len(pending) > 0 {
		remaining := pending[:0]
		for _, addr := range pending {
			if bindable(addr) {
				ready = append(ready, config.IPv6ProxyURL(addr))
				continue
			}
			remaining = append(remaining, addr)
		}
		pending = remaining
		if len(pending) == 0 || !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ready
		case <-time.After(settleInterval):
		}
	}
	if len(pending) > 0 && p.logger != nil {
		unusable := make([]string, 0, len(pending))
		for _, addr := range pending {
			unusable = append(unusable, addr.String())
		}
		p.logger.Warn("some IPv6 pool addresses never became usable", "component", "proxy", "event", "ipv6_pool_partial",
			"unusable", len(pending), "addresses", unusable)
	}
	return ready
}

// bindable reports whether the kernel currently accepts the address as a local
// endpoint. A tentative address, whose duplicate address detection is still
// running, rejects the bind, and that is exactly the condition that would make
// a transport built on the address fail.
func bindable(addr netip.Addr) bool {
	listener, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: addr.AsSlice()})
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// presentAddresses lists the addresses already assigned to the interface. It
// reads them through the standard library so a pool that does not manage its
// own addresses needs no external command.
func presentAddresses(iface string) (map[netip.Addr]struct{}, error) {
	device, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("look up interface %s: %w", iface, err)
	}
	assigned, err := device.Addrs()
	if err != nil {
		return nil, fmt.Errorf("list addresses on %s: %w", iface, err)
	}
	present := make(map[netip.Addr]struct{}, len(assigned))
	for _, item := range assigned {
		prefix, err := netip.ParsePrefix(item.String())
		if err != nil {
			continue
		}
		present[prefix.Addr().Unmap()] = struct{}{}
	}
	return present, nil
}
