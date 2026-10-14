package identity

import (
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// messageIDPattern is the shape a real client's message identifier has. Measured
// 2026-10-14: the upstream serves this shape and refuses "req_<hex>", an
// arbitrary string after "msg_", and a bare prefix.
var messageIDPattern = regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

func TestMessageIDHasCanonicalShape(t *testing.T) {
	now := int64(1791982827139)
	for i := 0; i < 128; i++ {
		id := MessageID(now + int64(i))
		if !messageIDPattern.MatchString(id) {
			t.Fatalf("MessageID(%d) = %q is not canonically shaped", now+int64(i), id)
		}
	}
}

// The identifier encodes the mint moment forward (ascending), unlike a session,
// and the upstream does not check it — so the value must simply be plausible.
func TestMessageIDPrefixIsAscending(t *testing.T) {
	early, err := strconv.ParseUint(MessageID(1_000_000)[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix is not hex: %v", err)
	}
	late, err := strconv.ParseUint(MessageID(9_000_000)[4:16], 16, 64)
	if err != nil {
		t.Fatalf("prefix is not hex: %v", err)
	}
	if late <= early {
		t.Fatalf("a later mint produced a smaller or equal prefix (%012x then %012x); the field is not ascending",
			early, late)
	}
	if want := uint64(9_000_000)*0x1000 + sessionMintCounter; late != want&0xFFFFFFFFFFFF {
		t.Fatalf("prefix %012x does not encode the mint moment (want %012x)", late, want&0xFFFFFFFFFFFF)
	}
}

func TestMessageIDsAreDistinct(t *testing.T) {
	seen := make(map[string]bool, 256)
	now := int64(1791982827139)
	for i := 0; i < 256; i++ {
		id := MessageID(now)
		if seen[id] {
			t.Fatalf("MessageID collided on %q", id)
		}
		seen[id] = true
	}
}

// The project header is passed through as the client sent it, and defaults to the
// one value the upstream serves unconditionally. The gateway used to hash it into
// a "prj_<hex>" id, which the upstream refuses.
func TestDeriveRequestIDsProjectHeader(t *testing.T) {
	now := time.Now().UnixMilli()
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}

	request := &http.Request{Header: make(http.Header)}
	if got := deriveRequestIDsAt(request, body, now).Project; got != defaultProjectID {
		t.Fatalf("project with no client signal = %q, want %q", got, defaultProjectID)
	}

	request.Header.Set("x-opencode-project", "my-project")
	if got := deriveRequestIDsAt(request, body, now).Project; got != "my-project" {
		t.Fatalf("project = %q, want the client's own value passed through", got)
	}

	fromBody := map[string]any{
		"metadata": map[string]any{"project_id": "body-project"},
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	if got := deriveRequestIDsAt(&http.Request{Header: make(http.Header)}, fromBody, now).Project; got != "body-project" {
		t.Fatalf("project = %q, want the body's metadata.project_id passed through", got)
	}
}

// Every id the gateway hands upstream must satisfy the shapes the gate checks.
func TestDeriveRequestIDsProducesGateShapedIDs(t *testing.T) {
	now := time.Now().UnixMilli()
	for i := 0; i < 64; i++ {
		body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "turn"}}}
		ids := deriveRequestIDsAt(&http.Request{Header: make(http.Header)}, body, now+int64(i))
		if !validCanonicalSessionID(ids.Session) {
			t.Fatalf("session %q is not gate-valid", ids.Session)
		}
		if !messageIDPattern.MatchString(ids.Request) {
			t.Fatalf("request %q is not canonically shaped", ids.Request)
		}
	}
}
