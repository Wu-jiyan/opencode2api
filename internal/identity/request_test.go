package identity

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The leading hex characters are the one part of an id that upstream actually
// verifies, so they are pinned against two identifiers a real OpenCode client
// minted. The first was captured on 2026-10-14 from the official CLI and
// returned 200 from the live gate. Any change to encodeSessionPrefix is a change
// to the identifier format itself, so these are exact-equality checks.
func TestEncodeSessionPrefixMatchesOfficialClientSamples(t *testing.T) {
	cases := []struct {
		mintedMillis int64
		want         string
	}{
		{1791982827139, "ec580097cffe"}, // 2026-10-14, live gate 200
		{1791882946721, "ecb74175effe"}, // 2026-10-13
	}
	for _, tc := range cases {
		if got := encodeSessionPrefix(tc.mintedMillis); got != tc.want {
			t.Fatalf("encodeSessionPrefix(%d) = %q, want the official client's %q",
				tc.mintedMillis, got, tc.want)
		}
	}
}

// decodeCounter reads an id the way the free tier does: the leading 12 hex
// characters are the low 48 bits of an inverted (unixMillis*0x1000 + counter).
func decodeCounter(t *testing.T, id string) uint64 {
	t.Helper()
	if !canonicalSessionPattern.MatchString(id) {
		t.Fatalf("id %q does not match the canonical shape", id)
	}
	stored, err := strconv.ParseUint(id[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix %q of %q is not lowercase hex: %v", id[4:16], id, err)
	}
	return (^stored & 0xFFFFFFFFFFFF) & 0xFFF
}

// noisePrefix renders a shape-valid 48-bit field with the given counter but
// arbitrary timestamp bits, i.e. what a digest-derived implementation emits.
func noisePrefix(timestamp uint64, counter uint64) string {
	return fmt.Sprintf("%012x", ^((timestamp<<12)|counter)&0xFFFFFFFFFFFF)
}

// Every id the gateway mints must carry the counter upstream checks, whatever
// the conversation, and must in turn be accepted by our own passthrough test.
func TestCanonicalSessionIDSatisfiesFreeTierCounterRule(t *testing.T) {
	now := int64(1791982827139)
	for i := 0; i < 512; i++ {
		signal := fmt.Sprintf("conversation-%d", i)
		id := canonicalSessionIDAt(signal, now)
		if counter := decodeCounter(t, id); counter < 1 || counter > sessionCounterMax {
			t.Fatalf("canonicalSessionIDAt(%q) = %q decodes to counter %d, want 1..%d",
				signal, id, counter, sessionCounterMax)
		}
		if !validCanonicalSessionID(id) {
			t.Fatalf("canonicalSessionIDAt(%q) = %q is not accepted by our own gate", signal, id)
		}
	}
}

// The prefix must be an inverted timestamp: upstream refuses ascending ids. The
// mint time is quantised to a bucket, so the comparison steps whole buckets.
func TestCanonicalSessionIDPrefixIsDescending(t *testing.T) {
	now := int64(1791982827139)
	earlier, err := strconv.ParseUint(canonicalSessionIDAt("descending", now)[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix is not hex: %v", err)
	}
	later, err := strconv.ParseUint(canonicalSessionIDAt("descending", now+2*time.Hour.Milliseconds())[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix is not hex: %v", err)
	}
	if later >= earlier {
		t.Fatalf("a later mint produced a larger or equal prefix (%012x then %012x); the field is not inverted",
			earlier, later)
	}
}

// A stable session per conversation is what keeps upstream prompt-cache affinity
// and holds a conversation on one exit. Quantising the mint time to a bucket
// gives that stability without a cache, and rotates afterwards so the encoded
// time never looks stale.
func TestCanonicalSessionIDIsStableWithinBucket(t *testing.T) {
	now := int64(1791982827139)
	first := canonicalSessionIDAt("same conversation", now)
	if second := canonicalSessionIDAt("same conversation", now+30*time.Minute.Milliseconds()); second != first {
		t.Fatalf("session changed inside one bucket: %q then %q", first, second)
	}
	if other := canonicalSessionIDAt("different conversation", now); other == first {
		t.Fatalf("two conversations collapsed onto session %q", first)
	}
	if later := canonicalSessionIDAt("same conversation", now+2*time.Hour.Milliseconds()); later == first {
		t.Fatalf("session did not rotate into the next bucket")
	}
}

// A shape-valid id whose counter is not 1 is exactly what a digest-derived
// implementation produces, and upstream answers 403 FreeTierError for it, so it
// must be re-minted rather than forwarded.
func TestCanonicalSessionIDReDerivesWrongCounter(t *testing.T) {
	impostor := "ses_000000000001abcdefghijklmn" // low 12 bits 0x001, counter 0xffe
	if !canonicalSessionPattern.MatchString(impostor) {
		t.Fatalf("test fixture %q is not shape-valid", impostor)
	}
	if decodeCounter(t, impostor) == sessionMintCounter {
		t.Fatalf("test fixture %q does not actually carry the wrong counter", impostor)
	}
	if validCanonicalSessionID(impostor) {
		t.Fatalf("wrong-counter id %q must not be accepted for passthrough", impostor)
	}
	got := canonicalSessionIDAt(impostor, 1791982827139)
	if got == impostor {
		t.Fatalf("wrong-counter session %q was forwarded unchanged; upstream rejects it", impostor)
	}
	if counter := decodeCounter(t, got); counter != sessionMintCounter {
		t.Fatalf("re-minted session %q decodes to counter %d, want %d", got, counter, sessionMintCounter)
	}
}

// The counter is the whole rule, and it is a range rather than a single value:
// measured against the live gate, counters 1 and 5 returned 200 while 255
// returned 403. The timestamp is not read at all — an id whose 36-bit timestamp
// field is pure noise returned 200, as did one back-dated 48 hours — so an id
// inside the counter range is forwarded untouched, which keeps a real client's
// own session and its prompt cache intact.
func TestCanonicalSessionIDForwardsAnyAcceptedCounter(t *testing.T) {
	for _, timestamp := range []uint64{0, 1, 1 << 20, 1 << 35, (1 << 36) - 1} {
		id := "ses_" + noisePrefix(timestamp, sessionMintCounter) + "abcdefghijklmn"
		if !canonicalSessionPattern.MatchString(id) {
			t.Fatalf("fixture %q is not shape-valid", id)
		}
		if !validCanonicalSessionID(id) {
			t.Fatalf("counter-%d id %q must be accepted for passthrough", sessionMintCounter, id)
		}
		if got := canonicalSessionIDAt(id, 1791982827139); got != id {
			t.Fatalf("counter-%d session %q was re-minted to %q; upstream does not read the timestamp",
				sessionMintCounter, id, got)
		}
	}

	// A counter inside the range is kept, at its own boundary.
	inRange := "ses_" + noisePrefix(1<<20, sessionCounterMax) + "abcdefghijklmn"
	if !validCanonicalSessionID(inRange) {
		t.Fatalf("counter %d is accepted upstream and must be forwarded", sessionCounterMax)
	}
	if got := canonicalSessionIDAt(inRange, 1791982827139); got != inRange {
		t.Fatalf("counter-%d session %q was re-minted to %q", sessionCounterMax, inRange, got)
	}

	// One past the range is refused upstream, so it must be re-minted: this is
	// the shape a digest-derived implementation produces.
	outOfRange := "ses_" + noisePrefix(1<<20, sessionCounterMax+1) + "abcdefghijklmn"
	if validCanonicalSessionID(outOfRange) {
		t.Fatalf("counter %d is refused upstream and must not be forwarded", sessionCounterMax+1)
	}
	if got := canonicalSessionIDAt(outOfRange, 1791982827139); got == outOfRange {
		t.Fatalf("out-of-range session %q was forwarded unchanged", outOfRange)
	}
}

// The counter upstream refuses is what a digest-derived implementation emits,
// and it is the shape this gateway produced before v1.3.1.
func TestCanonicalSessionIDReDerivesRefusedCounter(t *testing.T) {
	impostor := "ses_" + noisePrefix(1<<20, 255) + "abcdefghijklmn"
	if decodeCounter(t, impostor) <= sessionCounterMax {
		t.Fatalf("test fixture %q does not actually carry a refused counter", impostor)
	}
	if validCanonicalSessionID(impostor) {
		t.Fatalf("refused-counter id %q must not be accepted for passthrough", impostor)
	}
	got := canonicalSessionIDAt(impostor, 1791982827139)
	if got == impostor {
		t.Fatalf("refused-counter session %q was forwarded unchanged; upstream rejects it", impostor)
	}
	if counter := decodeCounter(t, got); counter < 1 || counter > sessionCounterMax {
		t.Fatalf("re-minted session %q decodes to counter %d, want 1..%d", got, counter, sessionCounterMax)
	}
}

// A genuine, current OpenCode session is passed through so a real client keeps
// its own upstream prompt-cache affinity.
func TestCanonicalSessionIDPreservesGenuineUpstreamSession(t *testing.T) {
	now := time.Now().UnixMilli()
	genuine := "ses_" + encodeSessionPrefix(now) + "Cy14V8Em7lI2ux"
	if !validCanonicalSessionID(genuine) {
		t.Fatalf("test fixture %q should be recognised as a genuine session", genuine)
	}
	if got := canonicalSessionIDAt(genuine, now); got != genuine {
		t.Fatalf("genuine upstream session %q was rewritten to %q", genuine, got)
	}
}

// Foreign identities must never leak their raw bytes into the hex prefix, and
// every id we mint must be one we would also accept.
func TestCanonicalSessionIDAlwaysMatchesCanonicalShape(t *testing.T) {
	now := time.Now().UnixMilli()
	for _, signal := range []string{
		"", "plain", "550e8400-e29b-41d4-a716-446655440000",
		"ses_000000000001abcdefghijklmn", "conversation-seed-{}",
		"x-parent-session-id", "非常长的中文会话标识",
	} {
		id := canonicalSessionIDAt(signal, now)
		if !canonicalSessionPattern.MatchString(id) {
			t.Fatalf("canonicalSessionIDAt(%q) = %q does not match the canonical shape", signal, id)
		}
		if !validCanonicalSessionID(id) {
			t.Fatalf("canonicalSessionIDAt(%q) = %q is not accepted by our own gate", signal, id)
		}
	}
}

// Derived ids must satisfy the gate at whatever instant the request arrives, not
// just at a fixed reference time.
func TestDeriveRequestIDsAlwaysYieldsGateValidSessions(t *testing.T) {
	now := time.Now().UnixMilli()
	for i := 0; i < 128; i++ {
		now += int64(i) * 37 // walk across bucket boundaries
		body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": fmt.Sprintf("turn %d", i)}}}
		ids := deriveRequestIDsAt(&http.Request{Header: make(http.Header)}, body, now)
		if !validCanonicalSessionID(ids.Session) {
			t.Fatalf("derived session %q is not gate-valid at %d", ids.Session, now)
		}
		if ids.Session == ids.Request {
			t.Fatalf("session and request ids collided")
		}
	}
}
