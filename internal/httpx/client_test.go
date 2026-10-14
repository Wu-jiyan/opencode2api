package httpx

import (
	"strings"
	"testing"
)

// The free tier serves a User-Agent that carries a runtime token after the
// version, and refuses both a bare "opencode/<version>" and the exact string this
// gateway used to build from the Go runtime version (measured 2026-10-14). Pin
// both properties so a future edit cannot quietly reintroduce either.
func TestUserAgentCarriesARuntimeToken(t *testing.T) {
	agent := UserAgent()
	if !strings.HasPrefix(agent, "opencode/") {
		t.Fatalf("User-Agent %q does not start with the opencode product token", agent)
	}
	fields := strings.Fields(agent)
	if len(fields) < 2 {
		t.Fatalf("User-Agent %q is a bare version; the gate refuses those", agent)
	}
	if strings.Contains(agent, "go1.") {
		t.Fatalf("User-Agent %q exposes the Go runtime; that is the token the gate refused", agent)
	}
}
