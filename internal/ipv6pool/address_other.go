//go:build !linux

package ipv6pool

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
)

// assign refuses to manage addresses outside Linux. The pool still works on
// other platforms when the addresses were provisioned beforehand, which is the
// supported configuration there.
func assign(_ context.Context, _ string, _ netip.Prefix, _ []netip.Addr, _ *slog.Logger) error {
	return errors.New("ipv6_pool.manage_addresses is only supported on Linux; set manage_addresses to false and assign the addresses outside opencode2api")
}
