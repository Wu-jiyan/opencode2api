package protocol

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// The upstream opens a Chat stream with a role chunk and then drops the
// connection before the model produced a single character. The client holds a
// frame that carries no content, so the turn can still be replayed on another
// exit: reporting an error would break a request that never said anything.
func TestForwardStreamReplaysAfterFramingOnlyCut(t *testing.T) {
	framing := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
	reader := &cutReader{data: framing, err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if !errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("a cut after framing only should be restartable, got: %v", err)
	}
}

// Once the model has produced text the turn can no longer be replayed: the
// client already holds the answer, so the failure has to be reported in-band.
func TestForwardStreamDoesNotReplayAfterText(t *testing.T) {
	content := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"
	reader := &cutReader{data: content, err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("a cut after text must not be restartable, got: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "upstream SSE stream failed") {
		t.Errorf("the failure should be reported in-band, got: %q", recorder.Body.String())
	}
}

// A frame the parser does not recognize says nothing about its content, so the
// turn must not be replayed on the strength of an unknown frame.
func TestForwardStreamDoesNotReplayOnUnknownFrame(t *testing.T) {
	reader := &cutReader{data: "data: {\"a\":1}\n\n", err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("an unrecognized frame must not be treated as framing, got: %v", err)
	}
}

// The Responses framing carries identifiers a client would see twice, so the
// relaxed rule stays with Chat.
func TestForwardStreamKeepsStrictRuleForResponses(t *testing.T) {
	framing := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"
	reader := &cutReader{data: framing, err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Responses, "test-model", false)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("a Responses stream must keep the strict replay rule, got: %v", err)
	}
}

// The final attempt has no exit left to move to, so the failure is reported
// in-band even when nothing but framing was forwarded.
func TestForwardStreamFinalFailureAfterFramingIsReported(t *testing.T) {
	framing := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
	reader := &cutReader{data: framing, err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", true)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("the final attempt must report the failure, got: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), "upstream SSE stream failed") {
		t.Errorf("the failure should be reported in-band, got: %q", recorder.Body.String())
	}
}

// A reasoning model opens the turn with the role and a first stretch of
// thinking. The answer has not been written yet, so a connection that dies
// there can still be replayed: the turn completes instead of failing.
func TestForwardStreamReplaysAfterReasoningOnlyCut(t *testing.T) {
	reasoning := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"thinking\"}}]}\n\n"
	reader := &cutReader{data: reasoning, err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if !errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("a cut before the answer should be restartable, got: %v", err)
	}
}

// The answer itself is what the turn is for: once it reached the client the
// turn can no longer be replayed.
func TestForwardStreamDoesNotReplayAfterReasoningAndAnswer(t *testing.T) {
	reasoning := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n"
	answer := "data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"
	reader := &cutReader{data: reasoning + answer, err: io.ErrUnexpectedEOF}
	recorder := httptest.NewRecorder()
	_, err := ForwardStream(context.Background(), recorder, reader, Chat, "test-model", false)
	if err == nil || errors.Is(err, ErrStreamRestartable) {
		t.Fatalf("a cut after the answer must not be restartable, got: %v", err)
	}
}
