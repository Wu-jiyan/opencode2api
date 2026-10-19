package gateway

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/identity"
	"opencode2api/internal/models"
	wire "opencode2api/internal/protocol"
)

// A connection that breaks before a status line carries no verdict about the
// exit: the request reached upstream and the answer never came back, which is
// what a flapping model does to one connection and not a statement that this
// exit is unusable. Recording it as an exit failure is what let a single model's
// upstream flap escalate into a self-sustaining outage — every drop cooled an
// exit, the whole pool ended up inside its cooldown windows, a cooling pool
// hands a request a single probe, and that probe's own failure re-armed the
// window it had just run out of.
func TestAnonymousTransportBreakDoesNotCoolTheExit(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"direct", "direct"})
	node := pool.nodes[0]
	broken := &url.Error{
		Op:  "Post",
		URL: "https://opencode.ai/inference/v1/chat/completions",
		Err: io.ErrUnexpectedEOF,
	}

	pool.MarkFailure(node, nil, broken)
	if until := node.cooldownUntil.Load(); until != 0 {
		t.Fatalf("a broken connection cooled the exit for %v", time.Until(time.Unix(0, until)))
	}
	if got := node.failures.Load(); got != 0 {
		t.Fatalf("failure counter = %d, want 0: a broken connection is not an exit failure", got)
	}

	// An exit that cannot be reached is a different verdict, and one the lane has
	// to keep acting on.
	pool.MarkFailure(node, nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})
	if until := node.cooldownUntil.Load(); until <= time.Now().UnixNano() {
		t.Fatal("a refused connection must still cool the exit")
	}
}

// A status verdict is the exit speaking about itself, so it keeps the weight it
// always had: the fix above only excuses a connection that carried no answer.
func TestAnonymousStatusVerdictStillCoolsTheExit(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"direct", "direct"})
	node := pool.nodes[1]

	pool.MarkFailure(node, &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"30"}},
	}, nil)
	if until := node.cooldownUntil.Load(); until <= time.Now().UnixNano() {
		t.Fatal("a rate-limited exit must still cool")
	}
}

// With no verdict describing the lane, a pool that is entirely cooling must not
// reduce a request to a single probe: the exits behind it are different
// connections and one of them can still serve the request. Before this, such a
// request made exactly one attempt, and its failure re-armed the cooldown, so a
// transient upstream flap reached the client as a run of 502s.
func TestAnonymousLaneKeepsWalkingWhileEveryExitCools(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits.Add(1)
		// Upstream accepting the request and then dropping the connection: the
		// shape an overloaded free tier produces, and the one that used to cool
		// every exit in the pool.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()

	gateway := &Gateway{
		cfg: config.Config{
			Upstream: config.UpstreamConfig{Zen: server.URL},
			Retry:    config.RetryConfig{MaxAttempts: 3, TimeoutSeconds: 30},
		},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		anonymous: newTestAnonymousPool(t, []string{"direct", "direct"}),
	}
	for _, node := range gateway.anonymous.nodes {
		node.cooldownUntil.Store(time.Now().Add(time.Minute).UnixNano())
		// Hold the reachability recheck out of this test: it talks to the network
		// and says nothing about the attempt walk under test.
		node.proxy.checking.Store(true)
	}

	ids := identity.RequestIDs{Session: "ses_16c1e0f5b0014", Request: "msg_16c1e0f5b0014", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"minimax-m3.1-flash-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "minimax-m3.1-flash-free", Tier: config.TierZen, Protocol: wire.Chat}

	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("a broken upstream must not read as a served request")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want every cooling exit walked once", attempts)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream saw %d requests, want 2: one per cooling exit", got)
	}
}

// The record still outranks the walk: when the lane itself has been refused and
// every exit is cooling, a later request is answered from the record without
// probing, and a request that already collected the verdict stops instead of
// walking the exits that can only repeat it.
func TestAnonymousRecordedRefusalStillStopsTheWalk(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, rateLimitBody)
	}))
	defer server.Close()

	gateway := &Gateway{
		cfg: config.Config{
			Upstream: config.UpstreamConfig{Zen: server.URL},
			Retry:    config.RetryConfig{MaxAttempts: 3, TimeoutSeconds: 30},
		},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		anonymous: newTestAnonymousPool(t, []string{server.URL, server.URL}),
	}
	for _, node := range gateway.anonymous.nodes {
		node.proxy.checking.Store(true)
	}
	gateway.anonymous.RecordRefusal(time.Now(), http.StatusTooManyRequests, "3294", []byte(rateLimitBody))
	for _, node := range gateway.anonymous.nodes {
		gateway.anonymous.MarkFailure(node, &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"3294"}},
		}, nil)
	}

	ids := identity.RequestIDs{Session: "ses_16c1e0f5b0015", Request: "msg_16c1e0f5b0015", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"minimax-m3.1-flash-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "minimax-m3.1-flash-free", Tier: config.TierZen, Protocol: wire.Chat}

	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("replaying the recorded refusal failed: %v", err)
	}
	if resp == nil {
		t.Fatal("replaying the recorded refusal returned no response")
	}
	_ = resp.Body.Close()
	if attempts != 0 || hits.Load() != 0 {
		t.Fatalf("attempts = %d, upstream hits = %d, want the record replayed without a probe", attempts, hits.Load())
	}
}
