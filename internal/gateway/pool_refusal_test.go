package gateway

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

const rateLimitBody = `{"error":{"code":null,"message":"Rate limit exceeded. ` +
	`Please try again later.","param":null,"type":"upstream_error"}}`

// A later verdict taken inside the same window reaches less far than the first
// one, so it must not shorten the record: shortening it would make the lane
// resume probing an upstream that has already said when it may be used again.
func TestAnonymousRefusalNeverShortensItsWindow(t *testing.T) {
	pool := &anonymousPool{cooldown: 15 * time.Second}
	start := time.Now()

	if !pool.RecordRefusal(start, http.StatusTooManyRequests, "3294", []byte(rateLimitBody)) {
		t.Fatal("the first refusal must be recorded")
	}
	if pool.RecordRefusal(start.Add(time.Minute), http.StatusTooManyRequests, "5", []byte(rateLimitBody)) {
		t.Fatal("a shorter window must not replace a longer one")
	}

	refusal, ok := pool.Refusal(start.Add(time.Minute))
	if !ok {
		t.Fatal("the refusal must still describe the lane a minute into the window")
	}
	if refusal.status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", refusal.status, http.StatusTooManyRequests)
	}
	if got := refusal.retryAfter; got != "3234" {
		t.Fatalf("Retry-After = %q, want the remaining 3234 seconds of the original window", got)
	}
	if string(refusal.body) != rateLimitBody {
		t.Fatal("the recorded body must survive into the replay")
	}
}

// The record describes a condition, not a moment: once the window upstream named
// has passed it must stop answering for the lane, so attempts resume.
func TestAnonymousRefusalExpiresWithItsWindow(t *testing.T) {
	pool := &anonymousPool{cooldown: 0}
	start := time.Now()
	pool.RecordRefusal(start, http.StatusTooManyRequests, "60", []byte(rateLimitBody))

	if _, ok := pool.Refusal(start.Add(59 * time.Second)); !ok {
		t.Fatal("the refusal must hold for the window upstream named")
	}
	if _, ok := pool.Refusal(start.Add(61 * time.Second)); ok {
		t.Fatal("an expired refusal must not answer for the lane")
	}
	if _, ok := pool.Refusal(start.Add(time.Hour)); ok {
		t.Fatal("an expired refusal must stay expired")
	}
}

// A pool with no cooldown configured still has to bound a verdict that carried
// no Retry-After, or the record would answer for the lane forever.
func TestAnonymousRefusalWithoutRetryAfterUsesPoolCooldown(t *testing.T) {
	pool := &anonymousPool{cooldown: 20 * time.Second}
	start := time.Now()
	pool.RecordRefusal(start, http.StatusForbidden, "", []byte(freeTierRejectionBody))

	refusal, ok := pool.Refusal(start.Add(10 * time.Second))
	if !ok {
		t.Fatal("a verdict without Retry-After must still be usable inside the pool cooldown")
	}
	if refusal.retryAfter != "" {
		t.Fatalf("Retry-After = %q, want it left empty when upstream sent none", refusal.retryAfter)
	}
	if _, ok := pool.Refusal(start.Add(21 * time.Second)); ok {
		t.Fatal("the record must not outlive the pool cooldown it fell back to")
	}
}

// The record is retired by a served inference request and by nothing else. The
// catalog refresh must not retire it: that route answers 200 while inference is
// rate-limited, so clearing on it would send the next request back to probing an
// upstream that is still refusing — every five minutes, for as long as the
// window lasts.
func TestAnonymousRefusalRetiredOnlyByServedRequest(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"http://127.0.0.1:1", "http://127.0.0.1:2"})
	start := time.Now()
	pool.RecordRefusal(start, http.StatusTooManyRequests, "3294", []byte(rateLimitBody))
	for _, node := range pool.nodes {
		pool.MarkFailure(node, &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"3294"}},
		}, nil)
	}
	if !pool.FullyCooling(start) {
		t.Fatal("every exit must read as cooling while each one sits inside its Retry-After window")
	}

	pool.MarkSuccess(pool.nodes[0])
	if _, ok := pool.Refusal(start); !ok {
		t.Fatal("a catalog refresh that happens to answer 200 must not clear the refusal record")
	}

	gateway := &Gateway{
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		anonymous: pool,
	}
	gateway.observeAnonymousResult(context.Background(), pool.nodes[0], &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}, nil)
	if _, ok := pool.Refusal(start); ok {
		t.Fatal("a served inference request must clear the recorded refusal")
	}
}

// Cooling is what makes a probe pointless: the exit cannot serve before its
// deadline. Health is not: nothing about an unhealthy exit says when it may work
// again, so it keeps the probe that clears the flag.
func TestAnonymousFullyCoolingIgnoresHealth(t *testing.T) {
	pool := newTestAnonymousPool(t, []string{"http://127.0.0.1:1", "http://127.0.0.1:2"})
	now := time.Now()

	if pool.FullyCooling(now) {
		t.Fatal("a fresh pool is not cooling")
	}
	pool.nodes[0].cooldownUntil.Store(now.Add(time.Minute).UnixNano())
	if pool.FullyCooling(now) {
		t.Fatal("one cooling exit must not make the whole pool read as cooling")
	}
	pool.nodes[1].cooldownUntil.Store(now.Add(time.Minute).UnixNano())
	if !pool.FullyCooling(now) {
		t.Fatal("two cooling exits must make the pool read as cooling")
	}
	pool.nodes[1].proxy.healthy.Store(false)
	if !pool.FullyCooling(now) {
		t.Fatal("health must not change what cooling means")
	}
	pool.nodes[0].cooldownUntil.Store(0)
	pool.nodes[1].cooldownUntil.Store(0)
	pool.nodes[0].proxy.healthy.Store(false)
	if pool.FullyCooling(now) {
		t.Fatal("unhealthy but cooling-free exits still keep their probe")
	}
	if (&anonymousPool{}).FullyCooling(now) {
		t.Fatal("a pool without exits cannot claim that every exit is cooling")
	}
}

// A replayed refusal has to be indistinguishable from the one upstream sent,
// except that its Retry-After counts down rather than repeating a stale window.
func TestRefusalResponseKeepsUpstreamAnswer(t *testing.T) {
	refusal := anonymousRefusal{
		status:     http.StatusTooManyRequests,
		retryAfter: "3210",
		body:       []byte(rateLimitBody),
	}
	resp := refusalResponse(refusal)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}
	if got := resp.Header.Get("Retry-After"); got != "3210" {
		t.Fatalf("Retry-After = %q, want 3210", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the replayed body failed: %v", err)
	}
	if !bytes.Equal(body, []byte(rateLimitBody)) {
		t.Fatal("the replayed body must be the upstream error document")
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing the replayed body failed: %v", err)
	}

	plain := refusalResponse(anonymousRefusal{status: http.StatusForbidden, body: []byte(freeTierRejectionBody)})
	if got := plain.Header.Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want none when upstream sent none", got)
	}
}
