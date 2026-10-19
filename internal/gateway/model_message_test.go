package gateway

import (
	"strings"
	"testing"

	"opencode2api/internal/config"
	modelcatalog "opencode2api/internal/models"
)

// A model the catalog knows but cannot expose is a protocol problem; an id nobody
// declared is simply unavailable here. Reporting the wrong one sends callers after
// a bug that does not exist — that is how cat-jam, a model upstream serves, was
// reported as "the model uses an upstream protocol that opencode2api does not
// expose" (measured 2026-10-19).
func TestUnsupportedModelMessageDistinguishesUnknownIDs(t *testing.T) {
	catalog := modelcatalog.NewCatalog(config.TierZen, nil)
	catalog.ReplaceWithCapabilities(
		[]string{"gemini-3-flash"}, nil, nil,
		map[config.Tier]map[string]bool{config.TierZen: {"gemini-3-flash": true}}, nil, nil,
	)
	gateway := &Gateway{catalog: catalog}

	if msg := gateway.unsupportedModelMessage("gemini-3-flash"); !strings.Contains(msg, "protocol") {
		t.Fatalf("a known-but-unsupported model message = %q, want the protocol explanation", msg)
	}
	msg := gateway.unsupportedModelMessage("definitely-not-a-model")
	if strings.Contains(msg, "protocol") {
		t.Fatalf("an unknown id message = %q, must not blame the protocol", msg)
	}
	if !strings.Contains(msg, "not available") {
		t.Fatalf("an unknown id message = %q, want the availability wording", msg)
	}
	if got := (&Gateway{}).unsupportedModelMessage("x"); got == "" {
		t.Fatal("a gateway without a catalog must still produce a message")
	}
}
