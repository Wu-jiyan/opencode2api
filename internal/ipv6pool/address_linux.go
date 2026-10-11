//go:build linux

package ipv6pool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"strings"
)

// assign adds the addresses that are not on the interface yet. The iproute2
// command is used rather than a netlink call so the pool does exactly what an
// operator would run by hand and surfaces the same errors.
func assign(ctx context.Context, iface string, prefix netip.Prefix, addresses []netip.Addr, logger *slog.Logger) error {
	present, err := presentAddresses(iface)
	if err != nil {
		return fmt.Errorf("ipv6 pool: %w", err)
	}
	for _, addr := range addresses {
		if _, ok := present[addr]; ok {
			continue
		}
		command := exec.CommandContext(ctx, "ip", "-6", "addr", "add", fmt.Sprintf("%s/%d", addr, prefix.Bits()), "dev", iface)
		output, err := command.CombinedOutput()
		if err == nil {
			continue
		}
		// "File exists" means the address appeared between the listing and this
		// call, which is the outcome we wanted anyway.
		if strings.Contains(string(output), "File exists") {
			continue
		}
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("ipv6 pool needs the ip command to assign addresses on %s: %w", iface, err)
		}
		// A single address that cannot be added does not sink the pool: the
		// ones already present still rotate, and the readiness probe decides
		// what is actually usable.
		if logger != nil {
			logger.Warn("IPv6 pool could not assign an address", "component", "proxy", "event", "ipv6_pool_assign_failed",
				"address", addr.String(), "interface", iface, "error", err, "output", strings.TrimSpace(string(output)))
		}
	}
	return nil
}
