package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"syscall"
	"testing"
	"time"

	"opencode2api/internal/config"
)

// A listener rotation stops the Xray process behind a transport, which tears down
// every tunnel it serves. The drain must therefore hold while a stream is still
// open, so the rotation postpones instead of cutting the response in half.
func TestTransportDrainWaitsForInFlightStream(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer unblock()

	proxy := newTestTransport(t, server.URL)
	resp, err := proxy.do(mustRequest(t, server.URL))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	budget := 2 * time.Second
	drained := make(chan bool, 1)
	go func() { drained <- proxy.drain(budget) }()

	select {
	case <-drained:
		t.Fatal("drain reported idle while a response body was still open")
	case <-time.After(150 * time.Millisecond):
	}

	unblock()
	_ = resp.Body.Close()
	select {
	case ok := <-drained:
		if !ok {
			t.Fatal("drain did not report idle after the stream finished")
		}
	case <-time.After(budget):
		t.Fatal("drain never completed after the stream finished")
	}
}

// An unused transport drains immediately, so a rotation is never delayed by a
// listener that simply has no traffic.
func TestTransportDrainReturnsImmediatelyWhenIdle(t *testing.T) {
	proxy := newTestTransport(t, "http://127.0.0.1:1")
	if !proxy.drain(time.Second) {
		t.Fatal("an idle transport must drain immediately")
	}
}

// Every exit can end up marked unavailable at once. The primary cursor then
// yields nothing, and if that were the end of it the request would fail without
// ever reaching an upstream, so no outcome could clear the flags that blocked
// it. The fallback must still hand out an exit for a real attempt.
func TestAnonymousCursorFallsBackWhenEveryExitUnavailable(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"http://127.0.0.1:1", "http://127.0.0.1:2"})
	for _, node := range pool.nodes {
		node.proxy.healthy.Store(false)
		node.cooldownUntil.Store(time.Now().Add(time.Hour).UnixNano())
	}

	cursor := pool.CursorFor("")
	if node := cursor.Next(); node != nil {
		t.Fatal("primary cursor must not yield an unavailable exit")
	}
	node := cursor.NextUnvetted()
	if node == nil {
		t.Fatal("fallback yielded nothing while every exit was unavailable")
	}
	if !node.proxy.healthy.Load() && node.proxy.name == "" {
		t.Fatalf("fallback returned an uninitialized node: %+v", node)
	}
}

// The fallback is a last resort only: while a usable exit exists it must not be
// chosen over one the primary pass would have picked.
func TestAnonymousCursorPrimaryStillSkipsUnavailable(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"http://127.0.0.1:1", "http://127.0.0.1:2"})
	pool.nodes[0].proxy.healthy.Store(false)

	cursor := pool.CursorFor("")
	for seen := 0; seen < len(pool.nodes); seen++ {
		node := cursor.Next()
		if node == nil {
			break
		}
		if node == pool.nodes[0] {
			t.Fatal("primary cursor yielded an unavailable exit")
		}
	}
}

func newTestAnonymousPool(t *testing.T, addresses []string) *anonymousPool {
	t.Helper()
	transports, err := newTransportPool(addresses, config.PerformanceConfig{
		MaxIdleConns: 8, MaxIdleConnsPerHost: 8, IdleConnTimeoutSeconds: 30, ConnectTimeoutSeconds: 5,
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("build transport pool: %v", err)
	}
	pool := newAnonymousPool(true, transports, 15*time.Second)
	if len(pool.nodes) != len(addresses) {
		t.Fatalf("anonymous pool size = %d, want %d", len(pool.nodes), len(addresses))
	}
	return pool
}

// Only a failure to reach the exit proves it is unusable. A slow upstream is a
// reason to fail over, not a reason to record the exit as down: recording it
// removes a working exit from service, and across the pool that removes every
// exit at once.
func TestIsProxyFailureOnlyReportsUnreachableExits(t *testing.T) {
	headerTimeout := &url.Error{Op: "Get", URL: "https://opencode.ai/zen", Err: timeoutFailure{}}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{"host unreachable", fmt.Errorf("dial: %w", syscall.EHOSTUNREACH), true},
		{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: timeoutFailure{}}, true},
		{"response header timeout", headerTimeout, false},
		{"plain error", errors.New("unexpected EOF"), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isProxyFailure(testCase.err); got != testCase.want {
				t.Fatalf("isProxyFailure(%v) = %v, want %v", testCase.err, got, testCase.want)
			}
		})
	}
}

type timeoutFailure struct{}

func (timeoutFailure) Error() string { return "timeout awaiting response headers" }

func (timeoutFailure) Timeout() bool { return true }

func newTestTransport(t *testing.T, address string) *proxyTransport {
	t.Helper()
	pool, err := newTransportPool([]string{address}, config.PerformanceConfig{
		MaxIdleConns: 8, MaxIdleConnsPerHost: 8, IdleConnTimeoutSeconds: 30, ConnectTimeoutSeconds: 5,
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("build transport pool: %v", err)
	}
	items := pool.snapshot()
	if len(items) != 1 {
		t.Fatalf("pool size = %d, want 1", len(items))
	}
	return items[0]
}

func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}
