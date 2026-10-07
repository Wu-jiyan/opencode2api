package protocol

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// chunkReader delivers each chunk from its own Read call, then io.EOF. It
// exists to control exactly where the upstream byte stream is cut.
type chunkReader struct {
	chunks [][]byte
	pos    int
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if r.pos >= len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.pos]
	r.pos++
	n := copy(buffer, chunk)
	return n, nil
}

// cutReader delivers its data and then fails with the supplied error, the way
// a truncated chunked response body fails mid-frame.
type cutReader struct {
	data string
	err  error
	sent bool
}

func (r *cutReader) Read(buffer []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(buffer, r.data), nil
	}
	return 0, r.err
}

func TestLastSSEBoundary(t *testing.T) {
	cases := []struct {
		input    string
		expected int
	}{
		{"abc", -1},
		{"data: x\n", -1},
		{"a\n\nb", 3},
		{"a\r\n\r\nb", 5},
		{"a\n\nb\n\n", 6},
		{"\n\n", 2},
	}
	for _, testCase := range cases {
		if got := lastSSEBoundary([]byte(testCase.input)); got != testCase.expected {
			t.Errorf("lastSSEBoundary(%q) = %d, want %d", testCase.input, got, testCase.expected)
		}
	}
}

func TestForwardStreamForwardsWholeFramesVerbatim(t *testing.T) {
	stream := "data: {\"choices\":[]}\n\ndata: [DONE]\n\n"
	recorder := httptest.NewRecorder()
	usage, err := ForwardStream(context.Background(), recorder, strings.NewReader(stream), Chat, "test-model", false)
	if err != nil {
		t.Fatalf("ForwardStream returned an error for a complete stream: %v", err)
	}
	if recorder.Body.String() != stream {
		t.Errorf("stream was not relayed verbatim: %q", recorder.Body.String())
	}
	if usage.Output != 0 {
		t.Errorf("unexpected usage on a frameless stream: %+v", usage)
	}
}

func TestForwardStreamFindsBoundariesSplitAcrossReads(t *testing.T) {
	stream := "data: {\"a\":1}\n\ndata: [DONE]\n\n"
	reader := &chunkReader{chunks: [][]byte{
		[]byte("data: {\"a\""),
		[]byte(":1}\n"),
		[]byte("\ndata: [DONE]\n\n"),
	}}
	recorder := httptest.NewRecorder()
	if _, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false); err != nil {
		t.Fatalf("ForwardStream returned an error: %v", err)
	}
	if recorder.Body.String() != stream {
		t.Errorf("split stream was not reassembled verbatim: %q", recorder.Body.String())
	}
}

func TestForwardStreamRestartableBeforeFirstFrame(t *testing.T) {
	reader := &cutReader{data: "data: {\"uncomp", err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if !errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("early cut should be restartable, got: %v", err)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("restartable failure must not write to the client, got: %q", recorder.Body.String())
	}
}

func TestForwardStreamFinalFailureEmitsErrorEvent(t *testing.T) {
	reader := &cutReader{data: "data: {\"uncomp", err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", true)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("final failure must not be restartable, got: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "upstream SSE stream failed: unexpected EOF") {
		t.Errorf("final failure should emit the cause as an error event, got: %q", recorder.Body.String())
	}
}

func TestForwardStreamDropsTruncatedTailAfterOutput(t *testing.T) {
	first := "data: {\"a\":1}\n\n"
	reader := &cutReader{data: first + "data: {\"trunc", err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("mid-stream cut should surface the read error, got: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, first) {
		t.Errorf("the complete frame before the cut was lost: %q", body)
	}
	if strings.Contains(body, "trunc") {
		t.Errorf("a truncated frame reached the client: %q", body)
	}
}

func TestForwardStreamForwardsUnterminatedFinalRecord(t *testing.T) {
	// Some upstreams omit the trailing blank line after the terminal event.
	// The record is forwarded so a client dispatching a pending event on close
	// still sees it; the gateway then reports the missing terminal in-band.
	reader := &cutReader{data: "data: [DONE]\n", err: io.EOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if !errors.Is(err, ErrSSETruncated) {
		t.Fatalf("missing terminal event should be reported: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "data: [DONE]\n") {
		t.Errorf("the unterminated final record was dropped: %q", recorder.Body.String())
	}
}

func TestTranscodeStreamRestartableOnInvalidFirstFrame(t *testing.T) {
	recorder := httptest.NewRecorder()
	_, err := TranscodeStream(context.Background(), recorder, strings.NewReader("data: notjson\n\n"), Chat, Responses, "test-model", false)
	if !errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("an unparseable first frame should be restartable, got: %v", err)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("restartable failure must not write to the client, got: %q", recorder.Body.String())
	}
}

func TestTranscodeStreamFinalFailureEmitsTargetError(t *testing.T) {
	recorder := httptest.NewRecorder()
	_, err := TranscodeStream(context.Background(), recorder, strings.NewReader("data: notjson\n\n"), Chat, Responses, "test-model", true)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("final failure must not be restartable, got: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "response.failed") {
		t.Errorf("the target protocol error event was not emitted: %q", recorder.Body.String())
	}
}

func TestCollapseStreamTruncatedStreamIsReported(t *testing.T) {
	if _, err := CollapseStream(strings.NewReader("data: {\"choices\":[]}\n\ndata: {\"trunc"), Chat, "test-model"); !errors.Is(err, ErrSSETruncated) {
		t.Fatalf("a stream cut after the last complete frame should be ErrSSETruncated, got: %v", err)
	}
	if _, err := CollapseStream(&cutReader{data: "data: {\"choices\":[]", err: io.ErrUnexpectedEOF}, Chat, "test-model"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("a mid-frame cut should surface the read error, got: %v", err)
	}
}
