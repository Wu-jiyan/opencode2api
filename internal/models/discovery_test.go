package models

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"opencode2api/internal/config"
	wire "opencode2api/internal/protocol"
)

// The endpoint tables live in the console documentation. This is that table's
// layout: a display name, the model id, then the endpoint in backticks. A System
// One model is declared nowhere else, so the parser has to keep matching after
// the document moves — which is exactly what broke: the tables used to sit in
// packages/web/src/content/docs/{zen,go}.mdx and now answer 404 there.
func TestFetchProtocolDocsReadsTheConsoleEndpointTable(t *testing.T) {
	body := "" +
		"| Model         | ID            | Endpoint                                                  | Notes |\n" +
		"| ------------- | ------------- | --------------------------------------------------------- | ----- |\n" +
		"| Jev 1.13      | jev-1.13      | `https://opencode.ai/inference/v1/systemone`               | -     |\n" +
		"| Jev 1.13 Free | jev-1.13-free | `https://opencode.ai/inference/v1/systemone`               | -     |\n" +
		"| Sol           | gpt-6-sol     | `https://opencode.ai/inference/v1/responses`               | -     |\n" +
		"| Some Chat     | plain-chat    | `https://opencode.ai/inference/v1/chat/completions`        | -     |\n" +
		"| Some Claude   | claude-x      | `https://opencode.ai/inference/v1/messages`                | -     |\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	got, err := FetchProtocolDocs(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("FetchProtocolDocs failed: %v", err)
	}
	want := map[string]wire.Protocol{
		"jev-1.13":      wire.SystemOne,
		"jev-1.13-free": wire.SystemOne,
		"gpt-6-sol":     wire.Responses,
		"plain-chat":    wire.Chat,
		"claude-x":      wire.Anthropic,
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d rows, want %d: %v", len(got), len(want), got)
	}
	for model, protocol := range want {
		if got[model] != protocol {
			t.Fatalf("protocol for %q = %q, want %q", model, got[model], protocol)
		}
	}
}

// localOnly blocks every host but the test server, which is how this reproduces
// an unreachable endpoint table without touching the network.
type localOnly struct{ host string }

func (l localOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != l.host {
		return nil, errors.New("blocked in test")
	}
	return http.DefaultTransport.RoundTrip(req)
}

// A tier whose endpoint table cannot be read must say so. Those models keep no
// protocol, get advertised as chat models, and the upstream then refuses them
// with ModelProtocolUnsupported — which reads like an upstream fault and is why
// the failure is recorded rather than dropped.
func TestFetchCapabilitiesRecordsUnreadableEndpointTables(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"opencode":{"api":"https://example.com/api",` +
			`"npm":"@ai-sdk/openai-compatible","models":{"tier-model":{"id":"tier-model"}}}}`))
	}))
	defer server.Close()

	client := &http.Client{Transport: localOnly{host: server.Listener.Addr().String()}}
	capabilities, err := FetchCapabilities(context.Background(), client, server.URL)
	if err != nil {
		t.Fatalf("FetchCapabilities failed: %v", err)
	}

	// The machine catalog survives an unreadable table.
	if got := capabilities.Protocols[config.TierZen]["tier-model"]; got != wire.Chat {
		t.Fatalf("machine catalog lost an entry: protocol = %q, want chat", got)
	}
	if len(capabilities.Docs[config.TierZen]) != 0 {
		t.Fatalf("a blocked table produced rows: %v", capabilities.Docs[config.TierZen])
	}
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		if capabilities.DocsError[tier] == nil {
			t.Fatalf("tier %q has no recorded DocsError, so the failure would go unreported", tier)
		}
	}
}

// The tables outrank the machine catalog's SDK guess, which is how a System One
// model gets its protocol back.
func TestApplyDocsPromotesEndpointTables(t *testing.T) {
	capabilities := Capabilities{
		Protocols: map[config.Tier]map[string]wire.Protocol{
			config.TierZen: {"jev-1.13-free": wire.Chat},
			config.TierGo:  {},
		},
		Unsupported: map[config.Tier]map[string]bool{
			config.TierZen: {"jev-1.13-free": true},
			config.TierGo:  {},
		},
		Docs: map[config.Tier]map[string]wire.Protocol{
			config.TierZen: {"jev-1.13-free": wire.SystemOne},
			config.TierGo:  {},
		},
	}
	capabilities.ApplyDocs()

	if got := capabilities.Protocols[config.TierZen]["jev-1.13-free"]; got != wire.SystemOne {
		t.Fatalf("protocol = %q, want systemone", got)
	}
	if capabilities.Unsupported[config.TierZen]["jev-1.13-free"] {
		t.Fatalf("the endpoint table must clear the unsupported mark")
	}
}
