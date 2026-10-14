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

// A free-tier rejection is a verdict on the anonymous lane's single identity,
// so it stops the pool scan; everything the exit address could plausibly change
// keeps rotating.
func TestAnonymousRejectionIsDeterministic(t *testing.T) {
	quota := `{"type":"error","error":{"type":"FreeUsageLimitError",` +
		`"message":"Free usage limit reached."}}`
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"403 free-tier identity rejection", http.StatusForbidden, freeTierRejectionBody, true},
		{"403 quota body is accounted per exit, so keep rotating", http.StatusForbidden, quota, false},
		{"403 with an unrecognisable body keeps rotating", http.StatusForbidden, "<html>cloudflare</html>", false},
		{"429 quota exhaustion must keep rotating", http.StatusTooManyRequests, freeTierRejectionBody, false},
		{"200 is not a rejection", http.StatusOK, freeTierRejectionBody, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.status,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			if got := anonymousRejectionIsDeterministic(resp); got != tc.want {
				t.Fatalf("anonymousRejectionIsDeterministic(%d) = %v, want %v", tc.status, got, tc.want)
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
	if anonymousRejectionIsDeterministic(nil) {
		t.Fatal("a nil response must not be classified as a rejection")
	}
}
