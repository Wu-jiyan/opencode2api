package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
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
