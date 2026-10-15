package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"opencode2api/internal/config"
	"opencode2api/internal/identity"
	"opencode2api/internal/models"
	wire "opencode2api/internal/protocol"
	"opencode2api/internal/telemetry"
)

// While every exit is cooling there is nothing left to try, so the request must
// be answered from the recorded refusal instead of probing an upstream that has
// already spoken about the lane. Before this, every request in an exhausted
// lane fired one probe per request for as long as the window lasted.
func TestAnonymousLaneAnswersFromRecordWhileEveryExitCools(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "3294")
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
		anonymous: newTestAnonymousPool(t, []string{"direct", "direct"}),
	}
	ids := identity.RequestIDs{Session: "ses_141c82d9b0014ZhDz12Rtev8ya", Request: "msg_141c82d9b0014ZhDz12Rtev8ya", Project: "global"}
	bodies := map[config.Tier][]byte{config.TierZen: []byte(
		`{"model":"space-bunny-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`)}
	route := models.Route{ID: "space-bunny-free", Tier: config.TierZen, Protocol: wire.Chat}

	// The first request scans the exits it has, learns the verdict from each and
	// leaves them all cooling.
	first, err, attempts := gateway.doAnonymousUpstream(context.Background(), route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("first anonymous attempt failed: %v", err)
	}
	if first.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("first status = %d, want %d", first.StatusCode, http.StatusTooManyRequests)
	}
	if attempts != 2 {
		t.Fatalf("first attempt count = %d, want both exits probed", attempts)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream saw %d requests, want 2: one per exit", got)
	}
	body, err := io.ReadAll(first.Body)
	if err != nil {
		t.Fatalf("reading the relayed refusal failed: %v", err)
	}
	if err := first.Body.Close(); err != nil {
		t.Fatalf("closing the relayed refusal failed: %v", err)
	}
	if string(body) != rateLimitBody {
		t.Fatalf("relayed body = %q, want the upstream error document", body)
	}

	// The next request finds nothing to try and answers immediately, without
	// reaching upstream, keeping the status and the retry window.
	ctx := telemetry.WithRequestMeta(context.Background(), &telemetry.RequestMeta{})
	second, err, attempts := gateway.doAnonymousUpstream(ctx, route, bodies, ids, 0)
	if err != nil {
		t.Fatalf("replayed anonymous attempt failed: %v", err)
	}
	defer func() { _ = second.Body.Close() }()
	// An answered request must still be attributable, or an outage would look
	// like a gateway that received nothing: the access log only logs requests
	// that carry a channel.
	meta := telemetry.MetaFromContext(ctx)
	if meta.Channel != "anonymous" || meta.KeyID != "anonymous" || !meta.Anonymous {
		t.Fatalf("replayed request credential = %q/%q anon=%v, want the anonymous lane's", meta.Channel, meta.KeyID, meta.Anonymous)
	}
	if meta.Tier != string(config.TierZen) {
		t.Fatalf("replayed tier = %q, want %q", meta.Tier, config.TierZen)
	}
	if meta.Attempts != 0 {
		t.Fatalf("replayed attempts = %d, want 0: answering from the record contacts no upstream", meta.Attempts)
	}
	if attempts != 0 {
		t.Fatalf("replayed attempt count = %d, want no upstream attempt", attempts)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream saw %d requests, want no further probe", got)
	}
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("replayed status = %d, want %d", second.StatusCode, http.StatusTooManyRequests)
	}
	if retryAfter := second.Header.Get("Retry-After"); retryAfter == "" {
		t.Fatal("a replayed refusal must keep the retry window upstream named")
	} else if seconds, err := strconv.Atoi(retryAfter); err != nil || seconds < 1 || seconds > 3294 {
		t.Fatalf("replayed Retry-After = %q, want the remaining part of the 3294s window", retryAfter)
	}
	replayed, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("reading the replayed body failed: %v", err)
	}
	if string(replayed) != rateLimitBody {
		t.Fatalf("replayed body = %q, want the recorded error document", replayed)
	}
}
