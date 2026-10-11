package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// captureHandler records the structured events a component logs, so a test can
// assert what was and was not claimed.
type captureHandler struct {
	mu     sync.Mutex
	events []string
	attrs  []map[string]string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	fields := make(map[string]string, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		fields[attr.Key] = attr.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, fields["event"])
	h.attrs = append(h.attrs, fields)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) has(event string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, seen := range h.events {
		if seen == event {
			return true
		}
	}
	return false
}

func (h *captureHandler) attrFor(event, key string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, seen := range h.events {
		if seen == event {
			return h.attrs[i][key]
		}
	}
	return ""
}

// TestServeHTTPDoesNotClaimSuccessBeforeBinding pins the defect the current
// implementation replaced. The startup line used to be emitted before the
// listener existed, so a port that was already taken still produced a
// "server listening" entry: a failed start read as a successful one and the
// real cause had to be inferred from the following error line.
func TestServeHTTPDoesNotClaimSuccessBeforeBinding(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer blocker.Close()

	sink := &captureHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveHTTP(cancel, slog.New(sink), &http.Server{Addr: blocker.Addr().String()}, "webui")

	if sink.has("server_started") {
		t.Error("a failed bind still logged server_started")
	}
	if !sink.has("server_bind_failed") {
		t.Error("a failed bind was not reported as server_bind_failed")
	}
	if ctx.Err() == nil {
		t.Error("a failed bind must cancel the root context so the process stops instead of running headless")
	}
}

// TestServeHTTPLogsTheBoundAddress covers the positive path: the reported
// address is the one the listener actually holds rather than the requested
// string, which is what makes the line trustworthy when port 0 is configured.
func TestServeHTTPLogsTheBoundAddress(t *testing.T) {
	sink := &captureHandler{}
	// Only the cancel function is needed here: a successful bind must not
	// cancel anything, and the test waits on the logged event instead.
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveHTTP(cancel, slog.New(sink), server, "api")
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !sink.has("server_started") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !sink.has("server_started") {
		t.Fatal("a successful bind did not log server_started")
	}
	if got := sink.attrFor("server_started", "address"); got == "" || got == "127.0.0.1:0" {
		t.Errorf("logged address = %q, want the address the kernel assigned", got)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("serveHTTP did not return after Shutdown")
	}
}
