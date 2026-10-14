// Package identity derives stable session identities and unique request IDs.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"opencode2api/internal/jsonutil"
)

type RequestIDs struct {
	Session       string
	Request       string
	Project       string
	ParentSession string
}

func DeriveRequestIDs(r *http.Request, body map[string]any) RequestIDs {
	return deriveRequestIDsAt(r, body, time.Now().UnixMilli())
}

func deriveRequestIDsAt(r *http.Request, body map[string]any, nowMillis int64) RequestIDs {
	signal := jsonutil.FirstString(
		r.Header.Get("x-opencode-session"),
		r.Header.Get("x-session-affinity"),
		r.Header.Get("X-Session-Id"),
		r.Header.Get("x-session-id"),
		r.Header.Get("conversation-id"),
		jsonutil.StringAt(body, "conversation_id"),
		jsonutil.StringAt(body, "metadata", "session_id"),
	)
	if signal == "" {
		// Using the first user turn keeps a multi-turn conversation stable as its
		// history grows while separating conversations with different beginnings.
		signal = conversationSeed(body)
	}
	if signal == "" {
		signal = jsonutil.StringAt(body, "previous_response_id")
	}
	if signal == "" || signal == `{}` {
		signal = RandomID("fallback", 16)
	}
	session := canonicalSessionIDAt(signal, nowMillis)
	projectSignal := jsonutil.FirstString(r.Header.Get("x-opencode-project"), jsonutil.StringAt(body, "metadata", "project_id"))
	if projectSignal == "" {
		projectSignal = "opencode2api:default-project"
	}
	parentSession := jsonutil.FirstString(
		r.Header.Get("x-parent-session-id"),
		jsonutil.StringAt(body, "metadata", "parent_session_id"),
	)
	return RequestIDs{
		Session:       session,
		Request:       RandomID("req", 16),
		Project:       StableID("prj", projectSignal),
		ParentSession: parentSession,
	}
}

func conversationSeed(body map[string]any) string {
	if input, ok := body["input"].(string); ok && input != "" {
		return input
	}
	for _, field := range []string{"messages", "input"} {
		for _, raw := range jsonutil.SliceAt(body, field) {
			item, ok := raw.(map[string]any)
			if !ok || jsonutil.StringAt(item, "role") != "user" {
				continue
			}
			encoded, _ := json.Marshal(item["content"])
			if len(encoded) > 0 && string(encoded) != "null" {
				return string(encoded)
			}
		}
	}
	return ""
}

func StableID(prefix, value string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + value))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

// canonicalSessionPattern matches OpenCode's canonical session shape:
// "ses_" + 12 lowercase hex characters + 14 Base62 characters.
var canonicalSessionPattern = regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

const (
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	// sessionMintCounter is the counter the gateway writes: 1 is what a real
	// client emits for the first identifier it mints in a millisecond.
	sessionMintCounter = 1

	// sessionCounterMax is the largest counter upstream has been measured to
	// accept. Upstream decodes the 12 leading hex characters as the low 48 bits
	// of ~(unixMillis*0x1000 + counter) and serves the request only when that
	// counter is small. Measured against the live gate by varying the session id
	// alone, everything else byte-identical:
	//
	//	2026-10-13  counter 1 and 15 -> 200    counter 255 -> 403
	//	2026-10-14  counter 1 and 5  -> 200    counter 255 -> 403
	//	2026-10-14  the 36-bit timestamp field replaced with noise -> 200
	//	2026-10-14  mint time back-dated 48 hours -> 200
	//
	// So upstream checks the counter and does NOT read the timestamp. Ids whose
	// counter falls outside this range are re-minted; a real client's ids, which
	// only ever use a small counter, pass through untouched.
	sessionCounterMax = 15

	// sessionMintBucket is how coarsely that mint time is quantised. A
	// conversation keeps one session id for a whole bucket, which is what
	// preserves upstream prompt-cache affinity and keeps the conversation on one
	// exit for that hour; quantising also removes any need for a mint cache. The
	// encoded time stays obviously recent, so it cannot age out of any window
	// upstream might start enforcing.
	sessionMintBucket = time.Hour

	sessionMintBucketMillis = int64(sessionMintBucket / time.Millisecond)
)

// CanonicalSessionID returns signal unchanged when it already carries a session
// a real OpenCode client could have issued (preserving upstream prompt-cache
// affinity). Any other downstream identity (UUIDs, foreign client sessions,
// legacy gateway sessions, conversation seeds) is deterministically encoded
// into the canonical shape so the same conversation keeps a stable session.
//
// Only the trailing 14 characters are a function of signal: they are the
// digest-derived, per-conversation part. The leading hex characters carry the
// counter the gate checks, so they come from the mint instant instead.
func CanonicalSessionID(signal string) string {
	return canonicalSessionIDAt(signal, time.Now().UnixMilli())
}

func canonicalSessionIDAt(signal string, nowMillis int64) string {
	if validCanonicalSessionID(signal) {
		return signal
	}
	mintMillis := nowMillis - nowMillis%sessionMintBucketMillis
	sum := sha256.Sum256([]byte("ses\x00" + signal))
	randomPart := base62Fixed(new(big.Int).SetBytes(sum[6:16]), 14)
	return "ses_" + encodeSessionPrefix(mintMillis) + randomPart
}

// encodeSessionPrefix renders the 12 leading hex characters of an official
// OpenCode identifier: the low 48 bits of ~(unixMillis*0x1000 + counter), which
// is exactly what packages/schema/src/identifier.ts writes for a descending id.
// That is what makes the low 12 bits decode back to a counter of 1, which is the
// part upstream verifies.
func encodeSessionPrefix(unixMillis int64) string {
	current := uint64(unixMillis)*0x1000 + sessionMintCounter
	return fmt.Sprintf("%012x", ^current&0xFFFFFFFFFFFF)
}

// validCanonicalSessionID reports whether an id may be forwarded untouched.
// Upstream requires the canonical shape and a counter inside the measured range.
// A digest-derived implementation (this gateway before v1.3.1, and the upstream
// plugins that mint sessions the same way) satisfies the shape while landing on
// a small counter only occasionally, so the shape on its own is not a sufficient
// test.
func validCanonicalSessionID(value string) bool {
	if !canonicalSessionPattern.MatchString(value) {
		return false
	}
	stored, err := strconv.ParseUint(value[4:16], 16, 64)
	if err != nil {
		return false
	}
	counter := (^stored & 0xFFFFFFFFFFFF) & 0xFFF
	return counter >= 1 && counter <= sessionCounterMax
}

func base62Fixed(n *big.Int, width int) string {
	base := big.NewInt(62)
	out := make([]byte, width)
	remainder := new(big.Int)
	for i := width - 1; i >= 0; i-- {
		n.DivMod(n, base, remainder)
		out[i] = base62Alphabet[remainder.Int64()]
	}
	return string(out)
}

func RandomID(prefix string, size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(buf)
}
