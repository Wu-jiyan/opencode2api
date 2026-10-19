package gateway

import (
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// errUpstreamStalled reports an upstream that answered with response headers and
// then sent no bytes at all. A tunnel that accepted the request and went quiet
// is indistinguishable from a dead exit, and waiting for it would burn the
// whole request budget on a single node. The attempt is abandoned instead and
// the request is replayed on the next node.
//
// It is deliberately not treated as proof that the exit is broken: a slow
// upstream looks the same from here, and taking a working exit out of service
// would empty the pool (see isProxyFailure). A stall is a reason to fail over,
// not a reason to record the exit as down.
var errUpstreamStalled = errors.New("upstream sent no response bytes before the first-byte timeout")

// firstByteDeadline bounds the wait for the first byte of a response body. Only
// that first wait is bounded: once the stream is alive it may take as long as it
// needs, and a long answer is not a dead node.
type firstByteDeadline struct {
	io.ReadCloser
	timeout time.Duration
	armed   atomic.Bool
}

func (f *firstByteDeadline) Read(p []byte) (int, error) {
	if f.armed.Swap(true) {
		return f.ReadCloser.Read(p)
	}
	// The first read runs on its own goroutine so the wait can be bounded. The
	// bytes land in a private buffer: the caller's buffer may be reused as soon
	// as this method returns, and the abandoned goroutine must not write into it.
	buf := make([]byte, len(p))
	type outcome struct {
		n   int
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		n, err := f.ReadCloser.Read(buf)
		done <- outcome{n: n, err: err}
	}()
	timer := time.NewTimer(f.timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		copy(p, buf[:result.n])
		return result.n, result.err
	case <-timer.C:
		// Release the connection before giving up on the read. The read below may
		// still be parked in the transport, and the caller drains the body when it
		// abandons the attempt: without this the drain would wait for a body that
		// never ends, and the request would keep burning its budget on the very
		// attempt it just gave up on.
		go func() { _ = f.ReadCloser.Close() }()
		return 0, errUpstreamStalled
	}
}

// withFirstByteDeadline arms the watchdog on a response body. A body that is a
// releaseOnClose keeps that identity, because that type is what lets the
// gateway attribute a mid-stream failure to the exit that served it.
func withFirstByteDeadline(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if body == nil || timeout <= 0 {
		return body
	}
	if release, ok := body.(*releaseOnClose); ok {
		release.ReadCloser = &firstByteDeadline{ReadCloser: release.ReadCloser, timeout: timeout}
		return release
	}
	return &firstByteDeadline{ReadCloser: body, timeout: timeout}
}
