package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// endpointUnavailableBody is what upstream answered on 2026-10-22 for a model
// whose endpoint was momentarily unavailable while the same exits kept serving
// other models.
const endpointUnavailableBody = `{"type":"error","error":{"type":"EndpointUnavailable","message":"Upstream request failed: Endpoint is unavailable."}}`

// A 402 "Endpoint is unavailable" is upstream speaking about that model right
// now, not about the request we sent: the next exit is a different connection
// and can serve it. Classifying it as a request-shape rejection ended the walk
// after a single attempt, which is how a three-minute free-tier flap reached the
// client as a run of 402s.
func TestAnonymousLaneRotatesPastPaymentRequired(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, endpointUnavailableBody)
			return
		}
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
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
		node.proxy.checking.Store(true)
	}

	ids := identity.RequestIDs{Session: "ses_16c1e0f5b0016", Request: "msg_16c1e0f5b0016", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"minimax-m3.1-flash-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "minimax-m3.1-flash-free", Tier: config.TierZen, Protocol: wire.Chat}

	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("walking past a 402 failed: %v", err)
	}
	if resp == nil {
		t.Fatal("no response after walking past a 402")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the next exit's 200: a 402 must not end the walk", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (the 402 exit, then the serving exit)", attempts)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream saw %d requests, want 2", got)
	}
}

// A 402 must not take the exit out of service either: the status names the
// endpoint's current state, so cooling every exit that reported it would turn
// one model's flap into a pool-wide cooldown.
func TestPaymentRequiredDoesNotCoolTheExit(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"direct", "direct"})
	node := pool.nodes[0]
	pool.MarkFailure(node, &http.Response{StatusCode: http.StatusPaymentRequired}, nil)
	if until := node.cooldownUntil.Load(); until != 0 {
		t.Fatalf("a 402 cooled the exit for %v", time.Until(time.Unix(0, until)))
	}
	if got := node.failures.Load(); got != 0 {
		t.Fatalf("failure counter = %d, want 0", got)
	}
}

// Rotating past one 402 recovers a single flaky exit, but a second identical
// verdict from a different exit is upstream describing the model, and the rest
// of the pool can only repeat it. Walking all 16 exits took 64.5s on 2026-10-22
// before the client heard what the first two had already said, so the walk stops
// after paymentRequiredAttempts.
func TestAnonymousLaneStopsWalkingAfterTwoPayments(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, endpointUnavailableBody)
	}))
	defer server.Close()

	gateway := &Gateway{
		cfg: config.Config{
			Upstream: config.UpstreamConfig{Zen: server.URL},
			Retry:    config.RetryConfig{MaxAttempts: 3, TimeoutSeconds: 30},
		},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		anonymous: newTestAnonymousPool(t, []string{"direct", "direct", "direct"}),
	}
	for _, node := range gateway.anonymous.nodes {
		node.proxy.checking.Store(true)
	}

	ids := identity.RequestIDs{Session: "ses_16c1e0f5b0017", Request: "msg_16c1e0f5b0017", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"longcat-2.5-preview-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "longcat-2.5-preview-free", Tier: config.TierZen, Protocol: wire.Chat}

	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("no response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want the upstream 402 handed back", resp.StatusCode)
	}
	if attempts != paymentRequiredAttempts {
		t.Fatalf("attempts = %d, want %d: a repeated 402 must not walk the whole pool", attempts, paymentRequiredAttempts)
	}
	if got := hits.Load(); got != int32(paymentRequiredAttempts) {
		t.Fatalf("upstream saw %d requests, want %d", got, paymentRequiredAttempts)
	}
}

// freeUsageLimitBody is what upstream answers once the free tier's shared budget
// for the lane's "public" credential is spent. Captured 2026-10-24 from five
// independent source addresses, two identities and every advertised model — all
// of them byte-identical and counting down to the same reset.
const freeUsageLimitBody = `{"type":"error","error":{"type":"FreeUsageLimitError",` +
	`"message":"Free usage limit reached. Subscribe to OpenCode Go to keep going: https://opencode.ai/go"}}`

func newFreeBudgetGateway(t *testing.T, server *httptest.Server, exits int) *Gateway {
	t.Helper()
	addresses := make([]string, exits)
	for i := range addresses {
		addresses[i] = server.URL
	}
	gateway := &Gateway{
		cfg: config.Config{
			Upstream: config.UpstreamConfig{Zen: server.URL},
			Retry:    config.RetryConfig{MaxAttempts: 3, TimeoutSeconds: 30},
		},
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		anonymous: newTestAnonymousPool(t, addresses),
	}
	// Keep the reachability recheck out of these tests: it talks to the network
	// and says nothing about the walk under test.
	for _, node := range gateway.anonymous.nodes {
		node.proxy.checking.Store(true)
	}
	return gateway
}

// A spent free budget is the lane's one credential speaking, not the exit that
// delivered the body: the first answer already covers every exit. Walking the
// pool instead spends one request per exit against the budget that is gone —
// on 2026-10-24 the lane answered 439 client requests with 429 over six hours
// and walked the pool for each of them.
func TestAnonymousStopsAtSpentFreeBudget(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "44521")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, freeUsageLimitBody)
	}))
	defer server.Close()

	gateway := newFreeBudgetGateway(t, server, 3)
	ids := identity.RequestIDs{Session: "ses_16c1e0f5b0018", Request: "msg_16c1e0f5b0018", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"deepseek-v4.1-flash-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "deepseek-v4.1-flash-free", Tier: config.TierZen, Protocol: wire.Chat}

	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("no response")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the upstream 429 handed back", resp.StatusCode)
	}
	if attempts != 1 || hits.Load() != 1 {
		t.Fatalf("attempts = %d, upstream hits = %d, want exactly one probe", attempts, hits.Load())
	}

	recorded, ok := gateway.anonymous.Refusal(time.Now())
	if !ok {
		t.Fatal("a spent budget must be recorded for the lane, not only for the exit that reported it")
	}
	if !recorded.ExhaustedBudget() {
		t.Fatalf("recorded body = %q, want the free usage limit document", recorded.body)
	}
	if recorded.retryAfter == "" {
		t.Fatal("the record must keep upstream's reset window so clients learn when to come back")
	}
}

// Once the budget verdict is recorded it answers for the lane until upstream's
// reset, without probing an exit at all — including while exits the stopped walk
// never touched are still available and would otherwise be walked.
func TestSpentFreeBudgetAnswersLaterRequestsFromTheRecord(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "44521")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, freeUsageLimitBody)
	}))
	defer server.Close()

	gateway := newFreeBudgetGateway(t, server, 3)
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"deepseek-v4.1-flash-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "deepseek-v4.1-flash-free", Tier: config.TierZen, Protocol: wire.Chat}

	first := identity.RequestIDs{Session: "ses_16c1e0f5b0019", Request: "msg_16c1e0f5b0019", Project: "global"}
	if resp, _, _ := gateway.doAnonymousUpstream(context.Background(), route, bodies, first, 0); resp != nil {
		_ = resp.Body.Close()
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream saw %d requests, want 1 probe before the record exists", got)
	}

	second := identity.RequestIDs{Session: "ses_16c1e0f5b001a", Request: "msg_16c1e0f5b001a", Project: "global"}
	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, second, 0)
	if err != nil {
		t.Fatalf("replaying the recorded budget failure failed: %v", err)
	}
	if resp == nil {
		t.Fatal("replaying the recorded budget failure returned no response")
	}
	defer resp.Body.Close()
	if attempts != 0 || hits.Load() != 1 {
		t.Fatalf("attempts = %d, upstream hits = %d, want the record replayed without a probe", attempts, hits.Load())
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the recorded 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("a replayed refusal must still tell the client when the budget resets")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the replayed body failed: %v", err)
	}
	if !strings.Contains(string(body), freeUsageLimitMarker) {
		t.Fatalf("replayed body = %q, want the upstream free usage limit document", body)
	}
}

// A plain rate limit is one exit's own answer — a different exit is a different
// connection and may serve — so it keeps rotating instead of stopping the walk.
func TestPlainRateLimitStillRotatesPastTheExit(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, rateLimitBody)
			return
		}
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	gateway := newFreeBudgetGateway(t, server, 2)
	ids := identity.RequestIDs{Session: "ses_16c1e0f5b001b", Request: "msg_16c1e0f5b001b", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"deepseek-v4.1-flash-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "deepseek-v4.1-flash-free", Tier: config.TierZen, Protocol: wire.Chat}

	resp, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("walking past a rate limit failed: %v", err)
	}
	if resp == nil {
		t.Fatal("no response after walking past a rate limit")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the next exit's 200: a plain rate limit must not stop the walk", resp.StatusCode)
	}
	if attempts != 2 || hits.Load() != 2 {
		t.Fatalf("attempts = %d, upstream hits = %d, want the second exit tried", attempts, hits.Load())
	}
}

// The client has to be able to tell a spent budget from a throttled call, so the
// upstream's own error type survives the error document the gateway writes.
func TestErrorResponseKeepsUpstreamType(t *testing.T) {
	recorder := httptest.NewRecorder()
	copyErrorResponse(recorder, wire.Chat, &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"44521"}},
		Body:       io.NopCloser(strings.NewReader(freeUsageLimitBody)),
	}, "msg_16c1e0f5b001c")

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if got := recorder.Header().Get("Retry-After"); got != "44521" {
		t.Fatalf("Retry-After = %q, want the upstream window", got)
	}
	var document struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("decoding the error document failed: %v", err)
	}
	if document.Error.Type != freeUsageLimitMarker {
		t.Fatalf("error type = %q, want %q", document.Error.Type, freeUsageLimitMarker)
	}
	if !strings.Contains(document.Error.Message, "Free usage limit reached") {
		t.Fatalf("error message = %q, want upstream's message", document.Error.Message)
	}

	// A document with no type of its own keeps the generic name.
	plain := httptest.NewRecorder()
	copyErrorResponse(plain, wire.Chat, &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"message":"boom"}`)),
	}, "msg_16c1e0f5b001d")
	var fallback struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(plain.Body.Bytes(), &fallback); err != nil {
		t.Fatalf("decoding the fallback document failed: %v", err)
	}
	if fallback.Error.Type != "upstream_error" {
		t.Fatalf("error type = %q, want upstream_error", fallback.Error.Type)
	}
}
