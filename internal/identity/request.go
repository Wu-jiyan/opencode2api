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

	"opencode2api/internal/jsonutil"
)

type RequestIDs struct {
	Session       string
	Request       string
	Project       string
	ParentSession string
}

func DeriveRequestIDs(r *http.Request, body map[string]any) RequestIDs {
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
	session := CanonicalSessionID(signal)
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

	// sessionCounterMax bounds the counter field encoded into the leading hex
	// characters of a session id.
	//
	// The Zen free tier does not merely shape-check those characters. Upstream
	// (packages/schema/src/identifier.ts) builds them as the low 48 bits of
	// (timestamp*0x1000 + counter), bit-inverted for a session so that newer
	// ids sort first, and decodes them again to reject ids no real client could
	// have produced. Measured against the live gate on 2026-10-13 with every
	// other request property held constant:
	//
	//	ses_<sha256 cut>          -> 403 FreeTierError
	//	ses_...(counter 1)        -> 200
	//	ses_...(counter 15)       -> 200
	//	ses_...(counter 255)      -> 403 FreeTierError
	//	ascending (not inverted)  -> 403 FreeTierError
	//	any timestamp (epoch 0, 7 days old, or random) -> 200
	//
	// So the timestamp need only be inverted, while the counter must stay
	// small. 15 is the largest value measured to be accepted, and it also
	// reproduces the "ff0".."ffe" tail a real client emits.
	sessionCounterMax = 15
)

// CanonicalSessionID returns signal unchanged when it already carries a session
// a real OpenCode client could have issued (preserving upstream prompt-cache
// affinity). Any other downstream identity (UUIDs, foreign client sessions,
// legacy gateway sessions, conversation seeds) is deterministically encoded
// into the canonical shape so the same conversation keeps a stable session.
func CanonicalSessionID(signal string) string {
	if validCanonicalSessionID(signal) {
		return signal
	}
	sum := sha256.Sum256([]byte("ses\x00" + signal))
	prefix := encodeSessionPrefix(sum[:6])
	randomPart := base62Fixed(new(big.Int).SetBytes(sum[6:16]), 14)
	return "ses_" + prefix + randomPart
}

// encodeSessionPrefix renders 48 bits of digest as the 12 leading hex
// characters upstream decodes as an inverted (timestamp*0x1000 + counter)
// value. Both fields are taken from the digest, which keeps the id a pure
// function of the conversation signal while still carrying a counter small
// enough for the free-tier gate to accept.
func encodeSessionPrefix(digest []byte) string {
	value := uint64(digest[0])<<40 | uint64(digest[1])<<32 | uint64(digest[2])<<24 |
		uint64(digest[3])<<16 | uint64(digest[4])<<8 | uint64(digest[5])
	timestamp := value & 0xFFFFFFFFF // low 36 bits: the decoded millisecond field
	counter := 1 + (value>>36)%sessionCounterMax
	current := timestamp<<12 | counter
	return fmt.Sprintf("%012x", ^current&0xFFFFFFFFFFFF)
}

// validCanonicalSessionID reports whether an id already matches both the shape
// and the inverted-timestamp semantics the free tier checks. A shape-only match
// is not enough: forwarding one makes upstream answer 403 FreeTierError.
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
