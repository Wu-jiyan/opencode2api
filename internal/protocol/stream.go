package protocol

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var errStreamUpstreamFailure = errors.New("upstream stream failure delivered")

var errStreamNormalTermination = errors.New("upstream stream terminated normally")

// ErrSSETruncated reports an SSE stream that ended without its terminal event:
// the upstream, or a tunnel on the way to it, closed the connection early.
var ErrSSETruncated = errors.New("unexpected end of SSE stream")

// ErrStreamRestartable reports a stream failure that happened before any byte
// reached the downstream client. The whole upstream request can then be
// replayed invisibly, so callers may retry it with a fresh attempt. Once
// output has been forwarded the turn can no longer be replayed; the failure
// must be reported in-band instead.
var ErrStreamRestartable = errors.New("upstream stream failed before any output")

// restartableStreamError marks cause as replayable. The cause is kept in the
// message so logs and the final in-band error still say what actually happened.
func restartableStreamError(cause error) error {
	return fmt.Errorf("%w (%v)", ErrStreamRestartable, cause)
}

type streamTermination uint8

const (
	streamOpen streamTermination = iota
	streamNormalTermination
	streamErrorTermination
)

type bridgeStreamEvent struct {
	Kind       string
	ResponseID string
	Model      string
	Text       string
	Signature  string
	ToolKey    string
	ToolID     string
	ToolName   string
	Stop       string
	Error      string
	ErrorType  string
	Encrypted  string
	Usage      *Usage
}

// UpstreamStreamError is an error the upstream reported inside a stream that had
// already answered with a success status. It is deliberately distinct from an
// unparseable response: the upstream explained what went wrong, so that
// explanation can be relayed to the caller instead of being replaced by a
// generic conversion error that hides both the cause and the fact that the
// upstream, not the gateway, refused the request.
type UpstreamStreamError struct {
	Type    string
	Message string
}

func (e *UpstreamStreamError) Error() string {
	if e.Message == "" {
		return "upstream stream error"
	}
	return e.Message
}

// TranscodeStream is the request-aware form used by the
// gateway. A cancelled client must not receive a synthetic upstream error
// after its connection has gone away.
//
// finalAttempt tells the function whether it is the caller's last try. On a
// failure that produced no downstream output, a non-final call returns
// ErrStreamRestartable and writes nothing, letting the caller replay the whole
// upstream request; a final call reports the failure as an in-band error event.
func TranscodeStream(ctx context.Context, w http.ResponseWriter, reader io.Reader, from, to Protocol, model string, finalAttempt bool) (Usage, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return Usage{}, fmt.Errorf("response writer does not support streaming")
	}
	parser := &bridgeStreamParser{
		protocol:          from,
		tools:             map[string]bool{},
		toolIDs:           map[string]string{},
		toolNames:         map[string]string{},
		responseArgs:      map[string]bool{},
		responseReasoning: map[string]bool{},
	}
	emitter := newBridgeStreamEmitter(w, flusher, to, model)
	termination := streamOpen
	readErr := readSSE(reader, func(eventName, data string) error {
		events, err := parser.Parse(eventName, data)
		if err != nil {
			termination = streamErrorTermination
			if !emitter.wrote && !finalAttempt {
				return restartableStreamError(err)
			}
			if emitErr := emitter.Emit(bridgeStreamEvent{Kind: "error", Error: err.Error(), ErrorType: "upstream_error"}); emitErr != nil {
				return emitErr
			}
			return errStreamUpstreamFailure
		}
		for _, event := range events {
			switch event.Kind {
			case "done":
				termination = streamNormalTermination
			case "error":
				termination = streamErrorTermination
			}
			if err := emitter.Emit(event); err != nil {
				return err
			}
			if event.Kind == "done" {
				return errStreamNormalTermination
			}
		}
		return nil
	})
	if readErr != nil {
		if errors.Is(readErr, errStreamNormalTermination) {
			return emitter.usage, nil
		}
		if errors.Is(readErr, ErrStreamRestartable) {
			return emitter.usage, readErr
		}
		if ClientCanceled(ctx, readErr) {
			return emitter.usage, readErr
		}
		if termination == streamOpen {
			if !emitter.wrote && !finalAttempt {
				return emitter.usage, restartableStreamError(readErr)
			}
			if emitErr := emitUnexpectedStreamError(emitter, readErr); emitErr != nil {
				return emitter.usage, emitErr
			}
		}
		return emitter.usage, readErr
	}
	if termination == streamNormalTermination {
		return emitter.usage, nil
	}
	if termination == streamErrorTermination {
		return emitter.usage, errStreamUpstreamFailure
	}
	if ClientCanceled(ctx, nil) {
		return emitter.usage, ctx.Err()
	}
	if !emitter.wrote && !finalAttempt {
		return emitter.usage, restartableStreamError(ErrSSETruncated)
	}
	if err := emitUnexpectedStreamError(emitter, ErrSSETruncated); err != nil {
		return emitter.usage, err
	}
	return emitter.usage, ErrSSETruncated
}

func emitUnexpectedStreamError(emitter *bridgeStreamEmitter, cause error) error {
	message := "upstream SSE stream ended before a terminal event"
	if cause != nil && !errors.Is(cause, ErrSSETruncated) {
		message = fmt.Sprintf("upstream SSE stream failed: %v", cause)
	}
	err := emitter.Emit(bridgeStreamEvent{Kind: "error", Error: message, ErrorType: "upstream_error"})
	if errors.Is(err, errStreamUpstreamFailure) {
		return nil
	}
	return err
}

func ClientCanceled(ctx context.Context, streamErr error) bool {
	if errors.Is(streamErr, context.Canceled) {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}

// sseFlushWriter preserves an upstream SSE byte stream while making each
// successful write visible to the client immediately. io.Copy is free to
// choose large writes, so flushing in Write is the only reliable place to
// keep same-protocol streams live.
//
// An optional observer receives each chunk only after the client write and
// flush have happened. Usage and terminal-state tracking are diagnostic, so
// they must never delay the first byte the client sees.
type sseFlushWriter struct {
	writer   io.Writer
	flusher  http.Flusher
	observer io.Writer
}

func (writer *sseFlushWriter) Write(data []byte) (int, error) {
	n, err := writer.writer.Write(data)
	if n > 0 {
		writer.flusher.Flush()
		if writer.observer != nil {
			_, _ = writer.observer.Write(data[:n])
		}
	}
	return n, err
}

// maxStreamFrameBytes matches readSSE's scanner limit. A run of bytes with no
// blank-line boundary growing past it is not SSE framing; forwarding it raw
// keeps a non-SSE body from being buffered forever.
const maxStreamFrameBytes = 16 << 20

// ForwardStream relays a same-protocol SSE stream to the client. Only whole
// frames are ever written: the bytes after the last blank-line boundary are
// held back until they are complete, so a stream the upstream cuts mid-frame
// never reaches the client as truncated JSON. The caller's error event is the
// only thing the client sees for such a cut.
//
// finalAttempt tells the function whether it is the caller's last try. On a
// failure that forwarded nothing, a non-final call returns
// ErrStreamRestartable and writes nothing, letting the caller replay the whole
// upstream request; a final call reports the failure as an in-band error event.
func ForwardStream(ctx context.Context, w http.ResponseWriter, reader io.Reader, protocol Protocol, model string, finalAttempt bool) (Usage, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return Usage{}, fmt.Errorf("response writer does not support streaming")
	}
	observer := newStreamUsageObserver(protocol)
	forwarder := &sseFlushWriter{writer: w, flusher: flusher, observer: observer}
	wrote, copyErr := forwardCompleteFrames(forwarder, reader)
	usage := observer.Finish()
	if observer.ErrorTermination() {
		if copyErr != nil {
			return usage, copyErr
		}
		return usage, errStreamUpstreamFailure
	}
	if observer.NormalTermination() && observer.ParseError() == nil {
		return usage, copyErr
	}
	if ClientCanceled(ctx, copyErr) {
		if copyErr == nil {
			copyErr = ctx.Err()
		}
		return usage, copyErr
	}
	cause := observer.ParseError()
	if copyErr != nil {
		cause = copyErr
	}
	if cause == nil {
		cause = ErrSSETruncated
	}
	if !wrote && !finalAttempt {
		return usage, restartableStreamError(cause)
	}
	emitter := newBridgeStreamEmitter(w, flusher, protocol, model)
	if err := emitUnexpectedStreamError(emitter, cause); err != nil {
		return usage, err
	}
	return usage, cause
}

// forwardCompleteFrames copies the upstream byte stream, writing only the
// prefix that ends with a complete SSE frame boundary and holding the
// unfinished tail back. On a clean close the tail is still forwarded: some
// upstreams omit the trailing blank line after the terminal event, and a
// pending record is the client's to judge. A read error means the frame was
// cut mid-flight; the partial bytes are dropped because no client can parse
// them, and the caller reports the failure itself. It reports whether any
// byte reached the writer.
func forwardCompleteFrames(writer io.Writer, reader io.Reader) (bool, error) {
	wrote := false
	chunk := make([]byte, 32<<10)
	var pending []byte
	// scanned is how much of pending has already been searched for a boundary.
	// The last byte is always kept unsearched so a boundary split across two
	// reads is still found.
	scanned := 0
	for {
		n, readErr := reader.Read(chunk)
		if n > 0 {
			pending = append(pending, chunk[:n]...)
			if cut := lastSSEBoundary(pending[scanned:]); cut >= 0 {
				cut += scanned
				if _, err := writer.Write(pending[:cut]); err != nil {
					return wrote, err
				}
				wrote = true
				pending = append(pending[:0], pending[cut:]...)
				scanned = 0
			} else if len(pending) > maxStreamFrameBytes {
				if _, err := writer.Write(pending); err != nil {
					return wrote, err
				}
				wrote = true
				pending = pending[:0]
				scanned = 0
			} else {
				// Keep the last three bytes unsearched: a \r\n\r\n boundary can
				// straddle the next read and must still be found.
				scanned = max(len(pending)-3, 0)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && len(pending) > 0 {
				if _, err := writer.Write(pending); err != nil {
					return wrote, err
				}
				wrote = true
			}
			if errors.Is(readErr, io.EOF) {
				return wrote, nil
			}
			return wrote, readErr
		}
	}
}

// lastSSEBoundary returns the length of the longest prefix of data ending with
// a blank-line SSE boundary, or -1 when data holds no complete frame. Both the
// \n\n and the \r\n\r\n line endings are recognized, matching nextSSEBoundary.
func lastSSEBoundary(data []byte) int {
	cut := -1
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			if i+1 < len(data) && data[i+1] == '\n' {
				cut = i + 2
			}
			continue
		}
		if data[i] == '\r' && i+3 < len(data) && data[i+1] == '\n' && data[i+2] == '\r' && data[i+3] == '\n' {
			cut = i + 4
		}
	}
	return cut
}

type streamUsageObserver struct {
	parser   *bridgeStreamParser
	buffer   []byte
	usage    Usage
	normal   bool
	error    bool
	parseErr error
}

func newStreamUsageObserver(protocol Protocol) *streamUsageObserver {
	return &streamUsageObserver{parser: &bridgeStreamParser{
		protocol: protocol, tools: map[string]bool{}, toolIDs: map[string]string{}, toolNames: map[string]string{},
		responseArgs: map[string]bool{}, responseReasoning: map[string]bool{},
	}}
}

// streamFrameMarkers are the only byte patterns that can change what the
// observer records: a terminal event, an error, or a usage payload. Frames
// without any of them are pure content and are never JSON-decoded, which keeps
// the same-protocol hot path from parsing every chunk.
var streamFrameMarkers = [][]byte{
	[]byte("[DONE]"),
	[]byte("message_stop"),
	[]byte("response.completed"),
	[]byte("response.failed"),
	[]byte(`"usage"`),
	[]byte("finish_reason"),
	[]byte("error"),
}

func interestingStreamFrame(frame []byte) bool {
	for _, marker := range streamFrameMarkers {
		if bytes.Contains(frame, marker) {
			return true
		}
	}
	return false
}

func (observer *streamUsageObserver) Write(data []byte) (int, error) {
	observer.buffer = append(observer.buffer, data...)
	for {
		index, width := nextSSEBoundary(observer.buffer)
		if index < 0 {
			break
		}
		// The frame is consumed before the buffer is re-sliced, so it never
		// escapes; copying it would add an allocation per upstream chunk.
		frame := observer.buffer[:index+width]
		if interestingStreamFrame(frame) {
			observer.consume(frame)
		}
		observer.buffer = observer.buffer[index+width:]
	}
	return len(data), nil
}

func (observer *streamUsageObserver) Finish() Usage {
	// An unterminated final line is not an SSE frame. In particular, do not
	// count a usage object from a response that was cut off at EOF.
	observer.buffer = nil
	return observer.usage
}

func (observer *streamUsageObserver) NormalTermination() bool { return observer.normal }

func (observer *streamUsageObserver) ErrorTermination() bool { return observer.error }

func (observer *streamUsageObserver) ParseError() error { return observer.parseErr }

func (observer *streamUsageObserver) consume(frame []byte) {
	_ = readSSE(bytes.NewReader(frame), func(eventName, data string) error {
		events, err := observer.parser.Parse(eventName, data)
		if err != nil {
			if observer.parseErr == nil {
				observer.parseErr = err
			}
			return nil
		}
		for _, event := range events {
			switch event.Kind {
			case "done":
				observer.normal = true
			case "error":
				observer.error = true
			}
			if event.Usage != nil {
				mergeBridgeUsage(&observer.usage, *event.Usage)
			}
		}
		return nil
	})
}

func nextSSEBoundary(data []byte) (int, int) {
	lf := bytes.Index(data, []byte("\n\n"))
	crlf := bytes.Index(data, []byte("\r\n\r\n"))
	if lf < 0 {
		if crlf < 0 {
			return -1, 0
		}
		return crlf, 4
	}
	if crlf >= 0 && crlf < lf {
		return crlf, 4
	}
	return lf, 2
}

func readSSE(reader io.Reader, handler func(eventName, data string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var eventName string
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		err := handler(eventName, strings.Join(dataLines, "\n"))
		eventName = ""
		dataLines = dataLines[:0]
		return err
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(dataLines) > 0 {
		// A blank line is the SSE record delimiter. Do not parse a final
		// unterminated record as a complete frame; an EOF without a terminal
		// event is handled by the caller as a truncated stream.
		return ErrSSETruncated
	}
	return nil
}
