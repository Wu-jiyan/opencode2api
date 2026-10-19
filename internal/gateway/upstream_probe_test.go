package gateway

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

const freeTierRejectionBody = `{"type":"error","error":{"type":"FreeTierError",` +
	`"message":"OpenCode's free tier can only be used from within OpenCode."}}`

// The peek exists so a 403 can be classified without losing the response: the
// same bytes must still reach whoever reads the body next, the value must stay
// a *releaseOnClose so the exit attribution survives, and the in-flight slot
// must still be released exactly once.
func TestPeekResponsePrefixReplaysBody(t *testing.T) {
	payload := freeTierRejectionBody + "\n" + strings.Repeat("padding", 2000)
	releases := 0
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body: &releaseOnClose{
			ReadCloser: io.NopCloser(strings.NewReader(payload)),
			release:    func() { releases++ },
			proxy:      &proxyTransport{},
		},
	}

	prefix, err := peekResponsePrefix(resp, 64)
	if err != nil {
		t.Fatalf("peek failed: %v", err)
	}
	if !bytes.Contains(prefix, []byte(freeTierRejectionMarker)) {
		t.Fatalf("peeked prefix %q does not carry the marker", prefix)
	}

	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading after the peek failed: %v", err)
	}
	if string(rest) != payload {
		t.Fatalf("peek consumed the body: got %d bytes, want %d", len(rest), len(payload))
	}
	if attemptTransport(resp) == nil {
		t.Fatalf("peek lost the exit attribution; the body is no longer a *releaseOnClose")
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if releases != 1 {
		t.Fatalf("the in-flight slot was released %d times, want exactly 1", releases)
	}
}

// A lane refusal is a verdict on the anonymous lane's single identity and its
// address block, so it stops the pool scan and is worth keeping for requests
// that arrive while every exit cools; anything that could still be a statement
// about this request or this exit keeps rotating.
func TestAnonymousLaneRefusal(t *testing.T) {
	quota := `{"type":"error","error":{"type":"FreeUsageLimitError",` +
		`"message":"Free usage limit reached."}}`
	rateLimited := `{"error":{"code":null,"message":"Rate limit exceeded. ` +
		`Please try again later.","param":null,"type":"upstream_error"}}`
	cases := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		want       bool
	}{
		{"403 free-tier identity rejection", http.StatusForbidden, freeTierRejectionBody, "", true},
		{"403 quota body names another condition and keeps rotating", http.StatusForbidden, quota, "", false},
		{"403 with an unrecognisable body keeps rotating", http.StatusForbidden, "<html>cloudflare</html>", "", false},
		{"429 allowance verdict is kept, window included", http.StatusTooManyRequests, rateLimited, "3294", true},
		{"400 shape rejection is about the request, not the lane", http.StatusBadRequest, rateLimited, "3294", false},
		{"500 is about the exit and keeps rotating", http.StatusInternalServerError, rateLimited, "3294", false},
		{"200 is not a refusal", http.StatusOK, rateLimited, "12", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.status,
				Header:     http.Header{"Retry-After": []string{tc.retryAfter}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			refusal, got := anonymousLaneRefusal(resp)
			if got != tc.want {
				t.Fatalf("anonymousLaneRefusal(%d) = %v, want %v", tc.status, got, tc.want)
			}
			if got {
				if refusal.status != tc.status {
					t.Fatalf("kept status = %d, want %d", refusal.status, tc.status)
				}
				if refusal.retryAfter != tc.retryAfter {
					t.Fatalf("kept Retry-After = %q, want %q", refusal.retryAfter, tc.retryAfter)
				}
				if string(refusal.body) != tc.body {
					t.Fatalf("kept %d body bytes, want %d", len(refusal.body), len(tc.body))
				}
			}
			// Classifying a response must never cost the caller its body,
			// whichever way the verdict went.
			rest, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading after classification failed: %v", err)
			}
			if string(rest) != tc.body {
				t.Fatalf("classification consumed the body: got %d bytes, want %d", len(rest), len(tc.body))
			}
		})
	}
	if _, ok := anonymousLaneRefusal(nil); ok {
		t.Fatal("a nil response must not be classified as a refusal")
	}
}

// A refusal only speaks for the whole lane when the gateway owns the address
// upstream saw. Pinned sources all sit in one block, so their verdict covers
// every exit; a proxy exit reaches upstream through an egress the gateway does
// not own, so the next exit may well succeed and the scan has to continue.
func TestLaneLevelVerdict(t *testing.T) {
	cases := []struct {
		name  string
		proxy string
		want  bool
	}{
		{"direct exit uses the host's own address", "direct", true},
		{"pinned IPv6 source", "ipv6://2602:f32a:3:6200::1000", true},
		{"socks5 proxy exit", "socks5://127.0.0.1:24000", false},
		{"http proxy exit", "http://user:pass@198.51.100.9:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := laneLevelVerdict(&proxyTransport{name: tc.proxy}); got != tc.want {
				t.Fatalf("laneLevelVerdict(%q) = %v, want %v", tc.proxy, got, tc.want)
			}
		})
	}
	if !laneLevelVerdict(nil) {
		t.Fatal("a response without exit attribution must count as a lane-level verdict")
	}
}
