package protocol

import (
	"errors"
	"strings"
	"testing"
)

// An upstream that answers with a success status and then reports the failure
// inside the stream is not a conversion failure. The collapse has to preserve the
// upstream's own message and type so the caller learns why the request was
// refused, instead of getting a generic unsupported-response error that hides
// both the cause and the fact that the upstream rejected it.
func TestCollapseStreamPreservesUpstreamStreamError(t *testing.T) {
	const upstreamMessage = "Streaming response failed: [503] Upstream error from Nvidia: Service temporarily overloaded"
	stream := strings.Join([]string{
		`data: {"id":"gen-1","model":"nemotron-3-ultra-free","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		"",
		`data: {"error":{"message":"` + upstreamMessage + `","type":"upstream_error"}}`,
		"",
		"",
	}, "\n")

	_, err := CollapseStream(strings.NewReader(stream), Chat, "nemotron-3-ultra-free")
	if err == nil {
		t.Fatal("expected the upstream stream error to be reported")
	}
	var upstreamErr *UpstreamStreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T (%v), want *UpstreamStreamError", err, err)
	}
	if upstreamErr.Message != upstreamMessage {
		t.Fatalf("message = %q, want %q", upstreamErr.Message, upstreamMessage)
	}
	if upstreamErr.Type != "upstream_error" {
		t.Fatalf("type = %q, want upstream_error", upstreamErr.Type)
	}
}

// A stream that simply stops without a terminal event is a different problem
// from an error the upstream reported, so it must not be reported as one.
func TestCollapseStreamRejectsTruncatedStream(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"gen-1","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
		"",
		"",
	}, "\n")

	_, err := CollapseStream(strings.NewReader(stream), Chat, "muse-spark-1.3")
	if err == nil {
		t.Fatal("expected a truncated stream to be rejected")
	}
	var upstreamErr *UpstreamStreamError
	if errors.As(err, &upstreamErr) {
		t.Fatalf("a truncated stream must not be reported as an upstream stream error: %v", upstreamErr)
	}
}
