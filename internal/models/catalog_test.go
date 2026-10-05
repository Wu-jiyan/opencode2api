package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opencode2api/internal/config"
	wire "opencode2api/internal/protocol"
)

// FetchCapabilities looks up the endpoint tables in parallel and joins them at
// the end. That join has to be released on every path, failure included: a
// waiter that is never released blocks this function forever, and with it the
// entire catalog refresh, so the service reports an empty model list while
// logging nothing at all.
func TestFetchCapabilitiesReturnsWhenEndpointTablesAreUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"opencode":{"api":"https://example.com/api","models":{"tier-model":{"id":"tier-model"}}}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = FetchCapabilities(ctx, server.Client(), server.URL)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("FetchCapabilities never returned: the endpoint-table wait is not released")
	}
}

func number(v float64) *float64 { return &v }

func nowUTC() time.Time { return time.Now().UTC() }

// A refresh whose endpoint tables came back empty must not unpublish the System
// One models: they are declared only in those tables, so replacing the protocol
// maps wholesale dropped them from the routing table until a later refresh
// happened to reach the table host.
func TestRefreshKeepsSystemOneWhenEndpointTablesFail(t *testing.T) {
	catalog := NewCatalog(config.TierGo, nil)
	zen := []string{"jev-1.13", "jev-1.13-free", "muse-spark-1.3"}
	docs := map[config.Tier]map[string]wire.Protocol{
		config.TierZen: {"jev-1.13": wire.SystemOne, "jev-1.13-free": wire.SystemOne},
		config.TierGo:  {},
	}
	native := map[config.Tier]map[string]wire.Protocol{
		config.TierZen: {"muse-spark-1.3": wire.Responses},
		config.TierGo:  {},
	}
	catalog.ReplaceWithCapabilities(zen, nil, native, map[config.Tier]map[string]bool{}, nil, docs)
	if got := catalog.nativeProtocols[config.TierZen]["jev-1.13"]; got != wire.SystemOne {
		t.Fatalf("jev-1.13 protocol = %q, want systemone", got)
	}

	// Second refresh: the table fetch failed, so Docs is empty while the machine
	// catalog answered normally.
	catalog.ReplaceWithCapabilities(zen, nil, native, map[config.Tier]map[string]bool{}, nil,
		map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}})

	if !catalog.Supported("jev-1.13") || !catalog.Supported("jev-1.13-free") {
		t.Fatal("System One models disappeared after a refresh with no endpoint tables")
	}
	if got := catalog.nativeProtocols[config.TierZen]["jev-1.13-free"]; got != wire.SystemOne {
		t.Fatalf("jev-1.13-free protocol = %q, want systemone", got)
	}
}

// A capability refresh that carries metadata must land it without panicking.
// The per-tier metadata maps are written to on the first refresh, so a catalog
// that was only constructed — never loaded from a cache — must already own them.
func TestReplaceWithCapabilitiesAppliesMetadata(t *testing.T) {
	catalog := NewCatalog(config.TierGo, nil)
	metadata := map[config.Tier]map[string]Metadata{
		config.TierZen: {"muse-spark-1.3": {ContextWindow: 200000, Reasoning: true}},
		config.TierGo:  {},
	}
	catalog.ReplaceWithCapabilities([]string{"muse-spark-1.3"}, nil,
		map[config.Tier]map[string]wire.Protocol{config.TierZen: {"muse-spark-1.3": wire.Responses}, config.TierGo: {}},
		map[config.Tier]map[string]bool{config.TierZen: {}, config.TierGo: {}},
		metadata, nil)

	if got := catalog.MetadataForTier("muse-spark-1.3", config.TierZen); got.ContextWindow != 200000 || !got.Reasoning {
		t.Fatalf("metadata = %+v, want context_window 200000 with reasoning", got)
	}
}

// A Zen-only System One model must report its published protocol, not the Chat
// default the operator's preferred tier would have fallen back to.
func TestDiagnosticReportsDeclaredProtocolNotPreferredTierDefault(t *testing.T) {
	catalog := NewCatalog(config.TierGo, nil)
	catalog.ReplaceWithCapabilities([]string{"jev-1.13"}, nil,
		map[config.Tier]map[string]wire.Protocol{config.TierZen: {"jev-1.13": wire.SystemOne}, config.TierGo: {}},
		map[config.Tier]map[string]bool{}, nil,
		map[config.Tier]map[string]wire.Protocol{config.TierZen: {"jev-1.13": wire.SystemOne}, config.TierGo: {}})

	diagnostic := catalog.Diagnostic("jev-1.13", "", false, false, true)
	if diagnostic.NativeProtocol != wire.SystemOne {
		t.Fatalf("native protocol = %q, want systemone", diagnostic.NativeProtocol)
	}
	if got := diagnostic.NativeProtocols[config.TierGo]; got != "" {
		t.Fatalf("Go protocol = %q, want empty for a Zen-only model", got)
	}
}

// A model the machine catalog explicitly rejects stays unroutable, so relaxing
// the unknown-model case must not also admit the ones upstream cannot serve.
func TestUnsupportedModelStaysUnroutable(t *testing.T) {
	catalog := NewCatalog(config.TierGo, nil)
	catalog.ReplaceWithCapabilities([]string{"gemini-3-flash"}, nil,
		map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}},
		map[config.Tier]map[string]bool{config.TierZen: {"gemini-3-flash": true}, config.TierGo: {}}, nil, nil)

	if catalog.Supported("gemini-3-flash") {
		t.Fatal("a model the capability catalog marks unsupported must stay unroutable")
	}
}

// A zero-cost model stays usable on the anonymous lane even once the upstream
// marks it deprecated; deprecation is reported on its own field and must not
// downgrade the model to "name says free only".
func TestDeprecatedZeroCostModelIsStillMetadataFree(t *testing.T) {
	store := &PricingStore{models: map[string]Price{
		"mimo-v2.5-free": {ID: "mimo-v2.5-free", Input: number(0), Output: number(0), Deprecated: true},
		"paid-model":     {ID: "paid-model", Input: number(3), Output: number(6)},
	}}
	store.updatedAt = nowUTC()

	free := store.Decide("mimo-v2.5-free")
	if !free.Allowed {
		t.Fatal("a zero-cost deprecated model must remain anonymous-eligible")
	}
	if free.Source != "name_and_metadata_free" {
		t.Fatalf("source = %q, want name_and_metadata_free", free.Source)
	}
	if !free.Deprecated {
		t.Fatal("deprecation must still be reported on its own field")
	}

	paid := store.Decide("paid-model")
	if paid.Allowed || paid.Source != "metadata_paid" {
		t.Fatalf("paid model decision = %+v, want a rejected paid decision", paid)
	}
}
