package gateway

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wire "opencode2api/internal/protocol"
)

// blockingBody never delivers a byte; only closing it releases the read.
type blockingBody struct {
	once     sync.Once
	released chan struct{}
}

func newBlockingBody() *blockingBody {
	return &blockingBody{released: make(chan struct{})}
}

func (b *blockingBody) Read([]byte) (int, error) {
	<-b.released
	return 0, io.EOF
}

func (b *blockingBody) Close() error {
	b.once.Do(func() { close(b.released) })
	return nil
}

// A response that never sends a byte must fail the attempt instead of hanging
// until the request budget runs out.
func TestFirstByteDeadlineFailsASilentUpstream(t *testing.T) {
	body := newBlockingBody()
	defer func() { _ = body.Close() }()
	guarded := withFirstByteDeadline(body, 50*time.Millisecond)

	start := time.Now()
	if _, err := guarded.Read(make([]byte, 32)); !errors.Is(err, errUpstreamStalled) {
		t.Fatalf("read error = %v, want errUpstreamStalled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the stall took %s, want the watch dog to fire promptly", elapsed)
	}
}

// A response that answers promptly must pass through untouched.
func TestFirstByteDeadlinePassesAHealthyUpstream(t *testing.T) {
	guarded := withFirstByteDeadline(io.NopCloser(strings.NewReader("hello")), time.Second)
	buf := make([]byte, 32)
	n, err := guarded.Read(buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("read %q, want hello", buf[:n])
	}
}

// slowTailBody answers at once and then takes its time, the way a model that
// thinks before it keeps writing does.
type slowTailBody struct {
	delivered bool
}

func (b *slowTailBody) Read(p []byte) (int, error) {
	if !b.delivered {
		b.delivered = true
		return copy(p, []byte("hello")), nil
	}
	time.Sleep(300 * time.Millisecond)
	return copy(p, []byte("tail")), nil
}

func (b *slowTailBody) Close() error { return nil }

// Only the first read is guarded: a stream that pauses after it started is still
// allowed to take the time it needs.
func TestFirstByteDeadlineOnlyGuardsTheFirstRead(t *testing.T) {
	guarded := withFirstByteDeadline(&slowTailBody{}, 100*time.Millisecond)
	buf := make([]byte, 32)
	n, err := guarded.Read(buf)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("first read %q, want hello", buf[:n])
	}
	n, err = guarded.Read(buf)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if string(buf[:n]) != "tail" {
		t.Fatalf("second read %q, want tail", buf[:n])
	}
}

// A stalled upstream is a reason to replay the request on the next node, not a
// dead exit.
func TestStalledUpstreamIsRestartable(t *testing.T) {
	if !isRestartableStreamRead(errUpstreamStalled) {
		t.Fatal("a stalled upstream must be replayable on the next node")
	}
}

// The streaming path must turn the stall into a restartable failure, which is
// what makes the gateway replay the request on another node.
func TestStalledStreamIsReplayedOnTheNextNode(t *testing.T) {
	body := newBlockingBody()
	defer func() { _ = body.Close() }()
	guarded := withFirstByteDeadline(body, 50*time.Millisecond)

	recorder := httptest.NewRecorder()
	_, err := wire.ForwardStream(context.Background(), recorder, guarded, wire.Chat, "test-model", false)
	if !errors.Is(err, wire.ErrStreamRestartable) {
		t.Fatalf("stream error = %v, want ErrStreamRestartable so the request is replayed", err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("the failed attempt wrote %d bytes downstream, want none: a replay must stay invisible", recorder.Body.Len())
	}
}

// The body must keep its releaseOnClose identity, or the exit that served the
// stream can no longer be attributed to the failure.
func TestFirstByteDeadlineKeepsReleaseOnClose(t *testing.T) {
	release := &releaseOnClose{ReadCloser: io.NopCloser(strings.NewReader("x")), release: func() {}}
	guarded := withFirstByteDeadline(release, time.Second)
	if _, ok := guarded.(*releaseOnClose); !ok {
		t.Fatal("the watch dog must not hide the releaseOnClose wrapper")
	}
}

// A disabled watch dog must leave the body exactly as it was.
func TestFirstByteDeadlineDisabledLeavesTheBody(t *testing.T) {
	body := io.NopCloser(strings.NewReader("x"))
	if guarded := withFirstByteDeadline(body, 0); guarded != body {
		t.Fatal("a disabled watch dog must not wrap the body")
	}
}

// closeTrackingBody blocks in Read until it is closed, the way a body whose
// upstream never sends anything behaves.
type closeTrackingBody struct {
	closed atomic.Bool
	once   sync.Once
}

func (b *closeTrackingBody) Read([]byte) (int, error) {
	for !b.closed.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	return 0, io.EOF
}

func (b *closeTrackingBody) Close() error {
	b.once.Do(func() { b.closed.Store(true) })
	return nil
}

// The abandoned attempt must not hold its connection: the caller drains the body
// when it gives up, and a drain on a body that never ends waits for the whole
// request budget. That is what made the first-byte watch dog detect the stall in
// 3 seconds and then still hold the request for 60 (measured 2026-10-19).
func TestFirstByteDeadlineClosesTheBodyOnStall(t *testing.T) {
	body := &closeTrackingBody{}
	guarded := withFirstByteDeadline(body, 50*time.Millisecond)

	if _, err := guarded.Read(make([]byte, 32)); !errors.Is(err, errUpstreamStalled) {
		t.Fatalf("read error = %v, want errUpstreamStalled", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !body.closed.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	if !body.closed.Load() {
		t.Fatal("a stalled body must be closed, or the abandoned attempt keeps the request waiting")
	}
}
