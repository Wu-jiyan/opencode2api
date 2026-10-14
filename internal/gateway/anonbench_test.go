package gateway

import (
	"encoding/json"
	"testing"

	wire "opencode2api/internal/protocol"
)

// benchClientBody is the shape an anonymous request has after the gateway has
// prepared it: the client's messages plus the five injected official tool
// definitions, which is where nearly all of the bytes live.
func benchClientBody(b testing.TB) []byte {
	b.Helper()
	rec := map[string]any{
		"model":            "space-bunny-free",
		"max_tokens":       32000,
		"stream":           true,
		"stream_options":   map[string]any{"include_usage": true},
		"reasoning_effort": "high",
		"messages":         []any{map[string]any{"role": "user", "content": "Reply exactly: OK"}},
		"tools":            anonymousToolset(wire.Chat, nil),
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		b.Fatalf("marshal failed: %v", err)
	}
	return encoded
}

// Time to first token is dominated by the upstream prefill, so the number that
// matters for the tools is how many prompt tokens the gateway adds per request.
func TestInjectedToolBytesAreReported(t *testing.T) {
	body := benchClientBody(t)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	tools, err := json.Marshal(parsed["tools"])
	if err != nil {
		t.Fatalf("marshal tools failed: %v", err)
	}
	t.Logf("request body %d bytes, of which the injected tools are %d bytes (~%d tokens)",
		len(body), len(tools), len(tools)/4)
}

// The anonymous lane marshals the prepared payload, then prepareAnonymousBody
// parses it back, rewrites it and marshals it again. This measures that round
// trip, which is the per-request cost of the normalization.
func BenchmarkPrepareAnonymousBody(b *testing.B) {
	body := benchClientBody(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := prepareAnonymousBody(body, wire.Chat); len(out) == 0 {
			b.Fatal("normalization produced an empty body")
		}
	}
}

// Splitting the same work shows what one parse and one marshal of this payload
// cost, which is what the round trip above pays for twice.
func BenchmarkJSONRoundTrip(b *testing.B) {
	body := benchClientBody(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			b.Fatal(err)
		}
		if _, err := json.Marshal(payload); err != nil {
			b.Fatal(err)
		}
	}
}

// ensureAnonymousTools has to decide whether the tools array already matches the
// official one. Two shapes matter: a caller that brings its own tools (the
// common case for an agent harness, and the cheap one because the lengths differ)
// and a caller that already carries exactly the official five (the only case
// that has to be compared entry by entry).
func BenchmarkEnsureAnonymousTools(b *testing.B) {
	b.Run("client_brings_own_tools", func(b *testing.B) {
		clientTool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "list_dir",
				"description": "List a directory",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}
		original := map[string]any{"tools": []any{clientTool}}
		payload := map[string]any{}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for k, v := range original {
				payload[k] = v
			}
			ensureAnonymousTools(payload, wire.Chat)
		}
	})

	b.Run("already_official", func(b *testing.B) {
		payload := map[string]any{}
		if !ensureAnonymousTools(payload, wire.Chat) {
			b.Fatal("first call should have injected the tools")
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ensureAnonymousTools(payload, wire.Chat)
		}
	})
}
