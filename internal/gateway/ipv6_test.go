package gateway

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"opencode2api/internal/config"
)

func TestDialerForPinsTheLocalSourceAddress(t *testing.T) {
	dialer, isSource := dialerFor("ipv6://2001:db8:1234:5600::1002", config.PerformanceConfig{ConnectTimeoutSeconds: 5})
	if !isSource {
		t.Fatal("an ipv6:// entry must be recognized as a local source")
	}
	local, ok := dialer.LocalAddr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("LocalAddr = %#v, want a *net.TCPAddr", dialer.LocalAddr)
	}
	if got := local.IP.String(); got != "2001:db8:1234:5600::1002" {
		t.Errorf("LocalAddr = %s, want 2001:db8:1234:5600::1002", got)
	}
}

func TestDialerForLeavesOtherEntriesAlone(t *testing.T) {
	for _, raw := range []string{"direct", "socks5://127.0.0.1:24100", "http://127.0.0.1:8080"} {
		dialer, isSource := dialerFor(raw, config.PerformanceConfig{ConnectTimeoutSeconds: 5})
		if isSource {
			t.Errorf("%q must not be treated as a local source", raw)
		}
		if dialer.LocalAddr != nil {
			t.Errorf("%q bound a local address %v", raw, dialer.LocalAddr)
		}
	}
}

func TestTransportPoolTreatsALocalSourceAsDirect(t *testing.T) {
	pool, err := newTransportPool([]string{"ipv6://2001:db8:1234:5600::1002", "socks5://127.0.0.1:24100", "direct"}, config.PerformanceConfig{}, 0)
	if err != nil {
		t.Fatalf("newTransportPool: %v", err)
	}
	items := pool.snapshot()
	if len(items) != 3 {
		t.Fatalf("got %d transports, want 3", len(items))
	}
	proxyFunc := func(index int) func(*http.Request) (*url.URL, error) {
		return items[index].client.Transport.(*http.Transport).Proxy
	}
	// A source address binds the outgoing connection; it must never also be
	// handed to the proxy machinery, which would try to reach it as a server.
	if proxyFunc(0) != nil {
		t.Error("an ipv6:// transport must not install a proxy function")
	}
	if proxyFunc(1) == nil {
		t.Error("a socks5 transport must route through a proxy function")
	}
	if proxyFunc(2) != nil {
		t.Error("a direct transport must not install a proxy function")
	}
}

// TestLocalSourceTransportDialsFromThePinnedAddress proves the mechanism end to
// end against a loopback listener: the connection must arrive from the address
// the entry named, not from an address the kernel picked.
func TestLocalSourceTransportDialsFromThePinnedAddress(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	pool, err := newTransportPool([]string{"ipv6://::1"}, config.PerformanceConfig{ConnectTimeoutSeconds: 5}, 0)
	if err != nil {
		t.Fatalf("newTransportPool: %v", err)
	}
	transport := pool.snapshot()[0].client.Transport.(*http.Transport)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.DialContext(ctx, "tcp6", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial through the pinned source address: %v", err)
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("local address = %#v, want a *net.TCPAddr", conn.LocalAddr())
	}
	if local.IP.To4() != nil || !local.IP.IsLoopback() {
		t.Errorf("the connection left from %s, want the IPv6 loopback", local.IP)
	}
}
