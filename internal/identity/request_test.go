package identity

import (
	"fmt"
	"strconv"
	"testing"
)

// decodeSessionCounter reads an id the way the Zen free tier does: the leading
// 12 hex characters are the low 48 bits of an inverted
// (timestamp*0x1000 + counter) value, so inverting them exposes the counter.
func decodeSessionCounter(t *testing.T, id string) int {
	t.Helper()
	if !canonicalSessionPattern.MatchString(id) {
		t.Fatalf("id %q does not match the canonical shape", id)
	}
	stored, err := strconv.ParseUint(id[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix %q of %q is not lowercase hex: %v", id[4:16], id, err)
	}
	current := ^stored & 0xFFFFFFFFFFFF
	return int(current & 0xFFF)
}

// Upstream decodes the leading 12 hex characters and refuses ids whose counter
// is not small. A plain digest cut lands in range roughly 0.4% of the time, so
// a few hundred samples make this a reliable guard: reverting CanonicalSessionID
// to a raw sha256 prefix (the pre-2026-10-13 behaviour that caused 403
// FreeTierError on every free-tier model) fails this test almost immediately.
func TestCanonicalSessionIDSatisfiesFreeTierCounterGate(t *testing.T) {
	for i := 0; i < 512; i++ {
		signal := fmt.Sprintf("conversation-%d", i)
		id := CanonicalSessionID(signal)
		if counter := decodeSessionCounter(t, id); counter < 1 || counter > sessionCounterMax {
			t.Fatalf("CanonicalSessionID(%q) = %q decodes to counter %d, want 1..%d",
				signal, id, counter, sessionCounterMax)
		}
	}
}

// The prefix must be an inverted timestamp, not an ascending one: upstream
// refuses ascending ids with the same 403.
func TestCanonicalSessionIDPrefixIsDescending(t *testing.T) {
	id := CanonicalSessionID("descending check")
	stored, err := strconv.ParseUint(id[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix %q is not hex: %v", id[4:16], err)
	}
	current := ^stored & 0xFFFFFFFFFFFF
	if timestamp := current >> 12; timestamp == stored>>12 {
		t.Fatalf("prefix %q is not bit-inverted (timestamp survived unchanged)", id[4:16])
	}
}

// A stable session per conversation is what keeps upstream prompt-cache affinity
// and pins a conversation to one exit.
func TestCanonicalSessionIDIsStablePerConversation(t *testing.T) {
	first := CanonicalSessionID("same conversation")
	if second := CanonicalSessionID("same conversation"); second != first {
		t.Fatalf("session for one conversation changed: %q then %q", first, second)
	}
	if other := CanonicalSessionID("different conversation"); other == first {
		t.Fatalf("two conversations collapsed onto session %q", first)
	}
}

// A shape-valid id whose counter is not small is exactly what a downstream
// gateway produces from a digest. Forwarding it unchanged makes upstream answer
// 403, so it must be re-derived instead.
func TestCanonicalSessionIDReDerivesShapeOnlyImpostors(t *testing.T) {
	impostor := "ses_000000000001abcdefghijklmn"
	if !canonicalSessionPattern.MatchString(impostor) {
		t.Fatalf("test fixture %q is not shape-valid", impostor)
	}
	if decodeSessionCounter(t, impostor) <= sessionCounterMax {
		t.Fatalf("test fixture %q is not actually outside the accepted counter range", impostor)
	}
	got := CanonicalSessionID(impostor)
	if got == impostor {
		t.Fatalf("shape-only session %q was forwarded unchanged; upstream rejects it", impostor)
	}
	if counter := decodeSessionCounter(t, got); counter < 1 || counter > sessionCounterMax {
		t.Fatalf("re-derived session %q decodes to counter %d, want 1..%d", got, counter, sessionCounterMax)
	}
}

// A genuine OpenCode session is passed through so a real client keeps its own
// upstream prompt-cache affinity.
func TestCanonicalSessionIDPreservesGenuineUpstreamSession(t *testing.T) {
	genuine := "ses_" + encodeSessionPrefix([]byte{9, 8, 7, 6, 5, 4}) + "Cy14V8Em7lI2ux"
	if !validCanonicalSessionID(genuine) {
		t.Fatalf("test fixture %q should be recognised as a genuine session", genuine)
	}
	if got := CanonicalSessionID(genuine); got != genuine {
		t.Fatalf("genuine upstream session %q was rewritten to %q", genuine, got)
	}
}

// Foreign identities must never leak their raw bytes into the hex prefix, or
// the counter gate would be satisfied only by luck.
func TestCanonicalSessionIDAlwaysMatchesCanonicalShape(t *testing.T) {
	for _, signal := range []string{
		"", "plain", "550e8400-e29b-41d4-a716-446655440000",
		"ses_000000000001abcdefghijklmn", "conversation-seed-{}",
		"x-parent-session-id", "非常长的中文会话标识",
	} {
		id := CanonicalSessionID(signal)
		if !canonicalSessionPattern.MatchString(id) {
			t.Fatalf("CanonicalSessionID(%q) = %q does not match the canonical shape", signal, id)
		}
	}
}
