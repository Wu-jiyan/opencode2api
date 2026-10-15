package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/httpx"
	"opencode2api/internal/identity"
	"opencode2api/internal/jsonutil"
	"opencode2api/internal/models"
	wire "opencode2api/internal/protocol"
	"opencode2api/internal/telemetry"
)

func (g *Gateway) doUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, models.Route, error) {
	resp, effectiveRoute, attempts, err := g.doUpstreamTiers(ctx, route, bodies, ids, attemptOffset)
	if err != nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, effectiveRoute, err
	}
	origBody := resp.Body
	errBody, readErr := io.ReadAll(io.LimitReader(origBody, 1<<20))
	// The original network body is always closed here: the retry path below
	// reuses the connection, otherwise downstream receives a fresh in-memory
	// reader over the cached bytes.
	httpx.DrainAndClose(origBody)
	if readErr != nil {
		resp.Body = io.NopCloser(bytes.NewReader(errBody))
		return resp, effectiveRoute, nil
	}
	// Restore the body so downstream error handling still sees the original
	// payload when no retry happens below.
	resp.Body = io.NopCloser(bytes.NewReader(errBody))
	if !isStaleReasoningReference(errBody) {
		return resp, effectiveRoute, nil
	}
	stripped, changed := stripStaleReasoningInputs(effectiveRoute, bodies)
	if !changed {
		return resp, effectiveRoute, nil
	}
	// The referenced reasoning items belong to an upstream chain this session
	// can no longer address (e.g. an interrupted stream). Replay once without
	// them instead of failing the client request outright. Attempt numbering
	// continues from the first round so monitoring never shows duplicate attempt
	// numbers for one request.
	//
	// The client's session is deliberately preserved: it carries key/proxy
	// affinity and upstream session correlation. Minting a fresh session made
	// the replay land on a different node with a cold prompt cache and detached
	// the reply from the chain the client still holds; the stale references are
	// already removed from the payload itself.
	g.logger.Info("retrying upstream without stale reasoning references", "component", "upstream", "event", "reasoning_reference_retry", "request_id", ids.Request, "model", route.ID, "tier", effectiveRoute.Tier, "attempt_offset", attempts)
	retryResp, retryRoute, _, retryErr := g.doUpstreamTiers(ctx, effectiveRoute, stripped, ids, attempts)
	if retryErr != nil || retryResp == nil || retryResp.StatusCode/100 != 2 {
		retryStatus := 0
		if retryResp != nil {
			retryStatus = retryResp.StatusCode
			httpx.DrainAndClose(retryResp.Body)
		}
		g.logger.Warn("reasoning reference retry failed; returning original error", "component", "upstream", "event", "reasoning_reference_retry_failed", "request_id", ids.Request, "model", route.ID, "tier", effectiveRoute.Tier, "error", retryErr, "retry_status", retryStatus)
		fallback := *resp
		fallback.Body = io.NopCloser(bytes.NewReader(errBody))
		return &fallback, effectiveRoute, nil
	}
	return retryResp, retryRoute, nil
}

// isStaleReasoningReference reports whether an upstream 400 body describes a
// reasoning item/reference the server no longer recognizes, such as
// "Referenced reasoning item 'rs_...' was not found or has expired".
// Generic validation errors that merely mention reasoning (e.g. "unknown
// reasoning field") must NOT match, so both the target phrase and the
// gone/expired marker are required.
func isStaleReasoningReference(body []byte) bool {
	text := strings.ToLower(string(body))
	if !strings.Contains(text, "reasoning item") && !strings.Contains(text, "reasoning reference") {
		return false
	}
	for _, marker := range []string{"not found", "expir", "does not exist", "no longer"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// stripStaleReasoningInputs removes server-issued reasoning references from
// Responses-protocol upstream payloads: replayed "reasoning" input items and
// any previous_response_id chain link. Other tiers/protocols are passed
// through untouched. It reports whether any payload actually changed.
func stripStaleReasoningInputs(route models.Route, bodies map[config.Tier][]byte) (map[config.Tier][]byte, bool) {
	changed := false
	out := make(map[config.Tier][]byte, len(bodies))
	for tier, body := range bodies {
		if len(body) == 0 || route.ProtocolFor(tier) != wire.Responses {
			out[tier] = body
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			out[tier] = body
			continue
		}
		tierChanged := false
		if _, ok := payload["previous_response_id"]; ok {
			delete(payload, "previous_response_id")
			tierChanged = true
		}
		if raw, ok := payload["input"].([]any); ok {
			kept := make([]any, 0, len(raw))
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok && jsonutil.StringAt(m, "type") == "reasoning" {
					tierChanged = true
					continue
				}
				kept = append(kept, item)
			}
			if tierChanged {
				payload["input"] = kept
			}
		}
		if !tierChanged {
			out[tier] = body
			continue
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			out[tier] = body
			continue
		}
		out[tier] = encoded
		changed = true
	}
	return out, changed
}

func (g *Gateway) doUpstreamTiers(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, models.Route, int, error) {
	var lastResponse *http.Response
	var lastErr error
	effectiveRoute := route
	attempts := attemptOffset
	g.dumpOutboundBodies(route, bodies, ids, attemptOffset)
	if route.Anonymous {
		resp, err, used := g.doAnonymousUpstream(ctx, route, bodies, ids, attempts)
		attempts += used
		if err == nil && resp != nil && resp.StatusCode/100 == 2 {
			return resp, route, attempts, nil
		}
		lastResponse, lastErr = resp, err
		if len(route.KeyTiers) > 0 {
			g.logger.Debug("anonymous request did not succeed; entering preferred key tiers", "component", "upstream", "event", "anonymous_fallback", "request_id", ids.Request, "attempts", attempts, "key_tiers", route.KeyTiers)
		}
	}
	if ctx.Err() != nil {
		// The anonymous phase already consumed the request budget. Entering the
		// authenticated tiers here would only fire instant attempts against a
		// dead context, cooling keys that were never really tried.
		if lastResponse != nil {
			return lastResponse, effectiveRoute, attempts, nil
		}
		if lastErr == nil {
			lastErr = ctx.Err()
		}
		return nil, effectiveRoute, attempts, lastErr
	}

	keyTiers := route.KeyTiers
	if !route.Anonymous && len(keyTiers) == 0 && (route.Tier == config.TierZen || route.Tier == config.TierGo) {
		keyTiers = []config.Tier{route.Tier}
	}
	for _, tier := range keyTiers {
		if lastResponse != nil {
			httpx.DrainAndClose(lastResponse.Body)
			lastResponse = nil
		}
		keyRoute := route
		keyRoute.Tier = tier
		keyRoute.Anonymous = false
		keyRoute.Protocol = route.ProtocolFor(tier)
		effectiveRoute = keyRoute
		resp, err, used := g.doKeyUpstream(ctx, keyRoute, bodies, ids, attempts)
		attempts += used
		if err == nil && resp != nil && resp.StatusCode/100 == 2 {
			return resp, keyRoute, attempts, nil
		}
		lastResponse, lastErr = resp, err
	}
	if lastResponse != nil {
		return lastResponse, effectiveRoute, attempts, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no usable upstream route")
	}
	return nil, effectiveRoute, attempts, lastErr
}

// doAnonymousUpstream tries every currently available proxy at most once. Any
// failure, including an HTTP error response, advances to the next proxy. Only a
// successful response ends the anonymous phase; exhausting the proxy cursor
// returns control to the preferred authenticated tiers.
func (g *Gateway) doAnonymousUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, error, int) {
	var lastResponse *http.Response
	var lastErr error
	cursor := g.anonymous.CursorFor(ids.Session)
	limit := g.anonymous.Len()
	attempts := 0
	body := bodies[config.TierZen]
	if len(body) == 0 {
		return nil, errors.New("no prepared Zen request body"), 0
	}
	// The anonymous free tier only serves agent-shaped streaming requests
	// (anything else is rejected with 403 FreeTierError). Normalize the
	// wire body here; the gateway collapses the stream back when the
	// downstream client asked for a plain JSON reply. Key tiers keep their
	// original bodies.
	body = prepareAnonymousBody(body, route.ProtocolFor(config.TierZen))
	for attempts < limit {
		now := time.Now()
		if ctx.Err() != nil {
			// The request-level budget is already gone. Stop scanning instead
			// of firing attempts that cannot succeed and then recording them
			// as failures against proxies that were never really tried.
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			break
		}
		node := cursor.Next()
		if node == nil {
			// Every exit is marked unavailable (unhealthy or cooling). Trying
			// none of them would fail this request without ever contacting an
			// upstream, which also means no outcome is produced that could
			// clear the very state blocking the attempt. Fall back to the
			// unvetted list so real traffic can restore the pool.
			//
			// A recorded lane refusal is the exception, and the reason the
			// record exists: when every exit is cooling because upstream
			// refused the lane itself, the fallback probe can only collect the
			// same refusal again, once per request, for as long as the window
			// lasts. Answering from the record returns the same status, message
			// and retry window the probe would have returned.
			if attempts == 0 {
				if refusal, ok := g.anonymous.Refusal(now); ok && g.anonymous.FullyCooling(now) {
					g.logger.Debug("answering from the anonymous lane's recorded refusal", "component", "upstream", "event", "anonymous_refusal_replayed", "request_id", ids.Request, "status", refusal.status, "retry_after", refusal.retryAfter, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true)
					return refusalResponse(refusal), nil, attempts
				}
				node = cursor.NextUnvetted()
			}
			if node == nil {
				break
			}
		}
		attempts++
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Attempts = attemptOffset + attempts
			meta.Tier = string(config.TierZen)
		}
		if lastResponse != nil {
			httpx.DrainAndClose(lastResponse.Body)
			lastResponse = nil
		}
		req, err := newUpstreamRequest(ctx, g.cfg.Upstream.Zen, route.Protocol, body, ids, anonymousZenKey)
		if err != nil {
			return nil, err, attempts
		}
		setRequestCredential(ctx, config.TierZen, "anonymous", "anonymous", true, node.proxy)
		started := time.Now()
		resp, err := node.proxy.do(req)
		duration := time.Since(started)
		if ctx.Err() != nil {
			// The parent budget expired while this attempt was in flight. Its
			// outcome says nothing about this proxy, so record nothing and stop
			// scanning.
			lastResponse, lastErr = resp, err
			if lastErr == nil && lastResponse == nil {
				lastErr = ctx.Err()
			}
			break
		}
		g.observeAnonymousResult(ctx, node, resp, err)
		if err == nil && resp.StatusCode/100 == 2 {
			g.logger.Debug("anonymous upstream accepted request", "component", "upstream", "event", "anonymous_attempt_succeeded", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "proxy", config.RedactURL(node.proxy.name), "status", resp.StatusCode, "duration_ms", duration.Milliseconds())
			return resp, nil, attempts
		}
		// A request-shape rejection is deterministic: the next exit would reject
		// it identically, so scanning the rest of the pool only buys one tunnel
		// and TLS handshake per remaining proxy before the client is told the
		// same thing. This mirrors the rule the authenticated lane already
		// applies; auth, throttling and server failures still rotate.
		refusal, laneRefusal := anonymousLaneRefusal(resp)
		if laneRefusal {
			if recorded := g.anonymous.RecordRefusal(now, refusal.status, refusal.retryAfter, refusal.body); recorded {
				g.logger.Warn("anonymous lane refused by upstream", "component", "upstream", "event", "anonymous_lane_refused", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "status", refusal.status, "retry_after", refusal.retryAfter, "proxy", config.RedactURL(node.proxy.name), "duration_ms", duration.Milliseconds())
			}
		}
		if isNonRetryableClientResponse(resp, err) {
			g.logger.Debug("anonymous upstream rejected a non-retryable request", "component", "upstream", "event", "anonymous_attempt_rejected", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "status", resp.StatusCode, "proxy", config.RedactURL(node.proxy.name), "duration_ms", duration.Milliseconds())
			return resp, nil, attempts
		}
		// A free-tier identity rejection is the same kind of verdict, but it
		// arrives as 403, which the check above deliberately lets rotate for the
		// authenticated tiers. The anonymous lane has no per-exit credential to
		// rotate onto, so it can only make the rejection worse.
		if laneRefusal && refusal.status == http.StatusForbidden {
			g.logger.Debug("anonymous upstream rejected our client identity; stopping rotation", "component", "upstream", "event", "anonymous_identity_rejected", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "status", resp.StatusCode, "proxy", config.RedactURL(node.proxy.name), "duration_ms", duration.Milliseconds())
			return resp, nil, attempts
		}
		lastResponse = resp
		lastErr = err
		if err != nil {
			g.logger.Debug("anonymous transport attempt failed", "component", "upstream", "event", "anonymous_attempt_transport_failed", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "proxy", config.RedactURL(node.proxy.name), "duration_ms", duration.Milliseconds(), "error", err)
		} else {
			g.logger.Debug("anonymous upstream returned an error response; trying the next proxy", "component", "upstream", "event", "anonymous_attempt_response_failed", "request_id", ids.Request, "attempt", attempts, "tier", config.TierZen, "key_id", "anonymous", "channel", "anonymous", "anonymous", true, "proxy", config.RedactURL(node.proxy.name), "status", resp.StatusCode, "duration_ms", duration.Milliseconds())
		}
	}
	if lastResponse != nil {
		return lastResponse, nil, attempts
	}
	if lastErr == nil {
		lastErr = errors.New("no healthy anonymous proxies available")
	}
	return nil, lastErr, attempts
}

// anonymousCoreTools are the tool names the anonymous free tier expects on
// an agent-shaped request. Requests without them are rejected with 403
// FreeTierError. Only the names matter; the gateway synthesizes minimal
// definitions for whichever ones the downstream client did not declare.
var anonymousCoreTools = []string{"bash", "edit", "glob", "grep", "read"}

// prepareAnonymousBody returns a copy of body normalized for the anonymous
// free tier: streaming enabled plus the core agent tools present. Bodies
// that already satisfy both (or are not JSON objects) are returned
// unchanged. System One payloads are decision requests, not agent traffic, so
// they are forwarded verbatim; injecting streaming or tool definitions would
// make the upstream reject them.
func prepareAnonymousBody(body []byte, protocol wire.Protocol) []byte {
	if protocol == wire.SystemOne {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	changed := false
	if streaming, ok := payload["stream"].(bool); !ok || !streaming {
		payload["stream"] = true
		changed = true
	}
	if ensureAnonymousChatUsage(payload, protocol) {
		changed = true
	}
	if ensureAnonymousTools(payload, protocol) {
		changed = true
	}
	if !changed {
		return body
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

// ensureAnonymousChatUsage keeps token usage available when a non-streaming
// request is forced onto the Chat SSE path. OpenAI-compatible Chat streams
// require stream_options.include_usage for the final usage event.
func ensureAnonymousChatUsage(payload map[string]any, protocol wire.Protocol) bool {
	if protocol != wire.Chat {
		return false
	}
	options, ok := payload["stream_options"].(map[string]any)
	if !ok {
		payload["stream_options"] = map[string]any{"include_usage": true}
		return true
	}
	includeUsage, ok := options["include_usage"].(bool)
	if ok && includeUsage {
		return false
	}
	options["include_usage"] = true
	return true
}

// ensureAnonymousTools makes the request read as a genuine agent session: it
// declares every core tool from the official definitions and reports whether the
// payload changed.
//
// A client's own definition of a core name is dropped rather than kept, because
// the free tier fingerprints those definitions and one impostor is enough to
// have the whole request refused — measured 2026-10-14, replacing only `bash`
// with a minimal definition turned a 200 into a 403. Declaring the official
// definition instead keeps the request served; the model then calls those tools
// with the official argument names, which is the shape a real client uses too.
// Tools outside the core set are passed through untouched.
func ensureAnonymousTools(payload map[string]any, protocol wire.Protocol) bool {
	raw, exists := payload["tools"]
	if !exists {
		payload["tools"] = anonymousToolset(protocol, nil)
		return true
	}
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	kept := make([]any, 0, len(items)+len(anonymousCoreTools))
	for _, item := range items {
		if isAnonymousCoreTool(anonymousToolName(protocol, item)) {
			continue
		}
		kept = append(kept, item)
	}
	kept = append(kept, anonymousToolset(protocol, nil)...)
	// Compared structurally rather than by encoding both sides. The official
	// definitions are inline, so marshalling them twice to answer a boolean cost
	// ~43us and ~27 KiB per request — more than half of this normalization.
	// reflect.DeepEqual on the decoded values asks the same question without
	// reaching for a JSON encoder, and the length check answers it outright
	// whenever the client brought tools of its own, which is the common case.
	if len(items) == len(kept) && slices.EqualFunc(items, kept, reflect.DeepEqual) {
		return false
	}
	payload["tools"] = kept
	return true
}

// isAnonymousCoreTool reports whether a tool name is one of the names the
// anonymous free tier fingerprints.
func isAnonymousCoreTool(name string) bool {
	for _, core := range anonymousCoreTools {
		if core == name {
			return true
		}
	}
	return false
}

func anonymousToolName(protocol wire.Protocol, item any) string {
	entry, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	if protocol == wire.Chat {
		return jsonutil.StringAt(entry, "function", "name")
	}
	return jsonutil.StringAt(entry, "name")
}

func anonymousToolset(protocol wire.Protocol, names []string) []any {
	if names == nil {
		names = anonymousCoreTools
	}
	tools := make([]any, 0, len(names))
	for _, name := range names {
		tools = append(tools, anonymousTool(protocol, name))
	}
	return tools
}

// anonymousTool renders one core tool. The official definition is preferred,
// because the free tier matches its content; the synthetic shape below is only a
// fallback for a name the embedded set does not carry.
func anonymousTool(protocol wire.Protocol, name string) map[string]any {
	if definition, ok := agentToolDefinition(protocol, name); ok {
		return definition
	}
	description := "Agent tool " + name
	parameters := map[string]any{"type": "object", "properties": map[string]any{}}
	switch protocol {
	case wire.Chat:
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
				"parameters":  parameters,
			},
		}
	case wire.Anthropic:
		return map[string]any{
			"name":         name,
			"description":  description,
			"input_schema": parameters,
		}
	default:
		return map[string]any{
			"type":        "function",
			"name":        name,
			"description": description,
			"parameters":  parameters,
		}
	}
}

// forceStreamBody returns a copy of body with streaming enabled. Bodies
// that already stream (or are not JSON objects) are returned unchanged.
func forceStreamBody(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if streaming, ok := payload["stream"].(bool); ok && streaming {
		return body
	}
	payload["stream"] = true
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

// requestBodyDumpLimit caps how much of an outbound body is written to the log.
const requestBodyDumpLimit = 64 << 10

// dumpOutboundBodies records the exact bytes about to be sent upstream when
// logging.dump_request_bodies is enabled. It exists so retry and translation
// problems (stale rs_* references, re-encoded history, tier re-encoding) can be
// diagnosed without patching the binary. Credentials travel in headers and never
// in the body. The logger redacts configured secrets, but conversation content
// can still be sensitive. Reference IDs are retained for replay diagnostics.
func (g *Gateway) dumpOutboundBodies(route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) {
	if !g.cfg.Logging.DumpRequestBodies || len(bodies) == 0 {
		return
	}
	phase := "initial"
	if attemptOffset > 0 {
		phase = "retry"
	}
	tiers := make([]string, 0, len(bodies))
	for tier := range bodies {
		tiers = append(tiers, string(tier))
	}
	sort.Strings(tiers)
	for _, name := range tiers {
		tier := config.Tier(name)
		body := bodies[tier]
		if len(body) == 0 {
			continue
		}
		sum := sha256.Sum256(body)
		truncated := len(body) > requestBodyDumpLimit
		payload := body
		if truncated {
			payload = body[:requestBodyDumpLimit]
		}
		g.logger.Debug("outbound upstream request body", "component", "upstream", "event", "upstream_request_body",
			"request_id", ids.Request, "model", route.ID, "tier", tier, "protocol", route.ProtocolFor(tier),
			"phase", phase, "bytes", len(body), "sha256", hex.EncodeToString(sum[:8]),
			"truncated", truncated, "body", string(payload))
	}
}

// shapeKeyBody normalizes key-tier free-model wire bodies to agent shape
// (stream + core tools), mirroring the anonymous lane. Upstream now
// rejects non-agent-shaped free-tier requests on every lane with 403
// FreeTierError; paid models keep their original bodies. It reports
// whether the body changed so the gateway can collapse the SSE stream a
// non-streaming client receives back.
func (g *Gateway) shapeKeyBody(body []byte, route models.Route, tier config.Tier) ([]byte, bool) {
	if !g.catalog.IsFreeModel(route.ID) {
		return body, false
	}
	shaped := prepareAnonymousBody(body, route.ProtocolFor(tier))
	return shaped, !bytes.Equal(shaped, body)
}

func (g *Gateway) doKeyUpstream(ctx context.Context, route models.Route, bodies map[config.Tier][]byte, ids identity.RequestIDs, attemptOffset int) (*http.Response, error, int) {
	var lastResponse *http.Response
	var lastErr error
	nodes := g.zenNodes
	baseURL := g.cfg.Upstream.Zen
	if route.Tier == config.TierGo {
		nodes = g.goNodes
		baseURL = g.cfg.Upstream.Go
	}
	cursor := nodes.CursorFor(ids.Session)
	if nodes.Len() == 0 {
		return nil, fmt.Errorf("no %s nodes configured", route.Tier), 0
	}
	attempts := 0
	body := bodies[route.Tier]
	if len(body) == 0 {
		return nil, fmt.Errorf("no prepared %s request body", route.Tier), 0
	}
	if shaped, changed := g.shapeKeyBody(body, route, route.Tier); changed {
		body = shaped
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Shaped = true
		}
	}
	for attempts < g.cfg.Retry.MaxAttempts {
		if ctx.Err() != nil {
			// Same guard as the anonymous loop: an exhausted request budget
			// must not be spent rotating keys that were never really tried.
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			break
		}
		node := cursor.Next()
		if node == nil {
			break
		}
		attempts++
		// Keep the request-level trace synchronized with the attempt that is
		// about to be sent. Only the redacted key suffix is retained.
		if meta := telemetry.MetaFromContext(ctx); meta != nil {
			meta.Attempts = attemptOffset + attempts
			meta.Tier = string(route.Tier)
		}
		if lastResponse != nil {
			httpx.DrainAndClose(lastResponse.Body)
			lastResponse = nil
		}
		req, err := newUpstreamRequest(ctx, baseURL, route.Protocol, body, ids, node.key)
		if err != nil {
			return nil, err, attempts
		}
		proxy := nodes.Proxy(node)
		if proxy == nil {
			lastErr = errors.New("upstream key has no proxy binding")
			break
		}
		keyID := config.KeyDisplayID(node.key)
		setRequestCredential(ctx, route.Tier, keyID, "key", false, proxy)
		attemptStarted := time.Now()
		resp, err := proxy.do(req)
		attemptDuration := time.Since(attemptStarted)
		if ctx.Err() != nil {
			// The request budget expired while this attempt was in flight. A
			// cancelled context says nothing about the key or the proxy: without
			// this guard the instant DeadlineExceeded would cool a healthy key
			// and evict a healthy proxy from the pool.
			lastResponse, lastErr = resp, err
			if lastErr == nil && lastResponse == nil {
				lastErr = ctx.Err()
			}
			break
		}
		g.observeKeyResult(ctx, nodes, node, proxy, resp, err)
		if err == nil && resp.StatusCode/100 == 2 {
			g.logger.Debug("upstream accepted request", "component", "upstream", "event", "attempt_succeeded", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "proxy", config.RedactURL(proxy.name), "status", resp.StatusCode, "duration_ms", attemptDuration.Milliseconds())
			return resp, nil, attempts
		}
		// Request-shape errors are deterministic and must leave this tier without
		// rotating through unrelated keys. The outer route may still try the next
		// tier in prefer order. Authentication, throttling, server, and transport
		// failures remain retryable inside this tier.
		if isNonRetryableClientResponse(resp, err) {
			g.logger.Debug("upstream rejected a non-retryable request", "component", "upstream", "event", "attempt_rejected", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "status", resp.StatusCode, "proxy", config.RedactURL(proxy.name), "duration_ms", attemptDuration.Milliseconds())
			return resp, nil, attempts
		}
		lastResponse = resp
		lastErr = err
		if err != nil {
			g.logger.Debug("upstream transport attempt failed", "component", "upstream", "event", "attempt_transport_failed", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "proxy", config.RedactURL(proxy.name), "duration_ms", attemptDuration.Milliseconds(), "error", err)
		} else {
			g.logger.Debug("upstream returned a retryable response", "component", "upstream", "event", "attempt_retryable_response", "request_id", ids.Request, "attempt", attempts, "tier", route.Tier, "key_id", keyID, "status", resp.StatusCode, "proxy", config.RedactURL(proxy.name), "duration_ms", attemptDuration.Milliseconds())
		}
	}
	if lastResponse != nil {
		return lastResponse, nil, attempts
	}
	return nil, lastErr, attempts
}

// observeKeyResult feeds a production attempt's outcome back into pool state.
func (g *Gateway) observeKeyResult(ctx context.Context, nodes *nodePool, node *upstreamNode, proxy *proxyTransport, resp *http.Response, err error) {
	status := upstreamStatus(resp)
	proxyFailed := g.syncProxyResult(ctx, proxy, status, err)
	if err == nil && status/100 == 2 || isNonRetryableClientResponse(resp, err) {
		nodes.MarkSuccess(node)
		return
	}
	if !proxyFailed || nodes.Proxy(node) == proxy {
		nodes.MarkFailure(node, resp, err)
	}
}

func (g *Gateway) observeAnonymousResult(ctx context.Context, node *anonymousNode, resp *http.Response, err error) {
	status := upstreamStatus(resp)
	g.syncProxyResult(ctx, node.proxy, status, err)
	if err == nil && status/100 == 2 {
		g.anonymous.MarkSuccess(node)
		// A served inference request is the outcome that proves a lane-level
		// refusal no longer describes upstream, so it is also what retires the
		// record. The catalog refresh deliberately does not: that route answers
		// 200 while inference is rate-limited — the same trap that made a
		// retired inference path look alive — so clearing on it would send the
		// next request back to probing an upstream that is still refusing.
		g.anonymous.ClearRefusal()
		return
	}
	g.anonymous.MarkFailure(node, resp, err)
}

func upstreamStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// releaseOnClose ties an in-flight slot to the lifetime of the response body,
// so a transport stays marked busy for as long as a stream is still being read
// and goes idle exactly once the body is closed. The proxy reference lets the
// caller attribute a mid-stream failure to the exit that served it.
type releaseOnClose struct {
	io.ReadCloser
	release func()
	proxy   *proxyTransport
	once    sync.Once
}

func (r *releaseOnClose) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.release)
	return err
}

// peekPrefix reads at most limit bytes from the response and rewrites the body
// so those same bytes are served again to whoever reads it next. The value stays
// a *releaseOnClose, which is what keeps attemptTransport able to attribute the
// response to the exit that served it.
func (r *releaseOnClose) peekPrefix(limit int64) ([]byte, error) {
	buffered := make([]byte, limit)
	n, err := io.ReadFull(r.ReadCloser, buffered)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	r.ReadCloser = &replayBody{prefix: buffered[:n], rest: r.ReadCloser}
	return buffered[:n], nil
}

// replayBody serves a buffered prefix and then the rest of the original stream,
// closing the original exactly once.
type replayBody struct {
	prefix []byte
	rest   io.ReadCloser
	once   sync.Once
}

func (r *replayBody) Read(p []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	return r.rest.Read(p)
}

func (r *replayBody) Close() error {
	var err error
	r.once.Do(func() { err = r.rest.Close() })
	return err
}

// attemptTransport returns the proxy exit that served a response, if the body
// still carries the marker the transport attached when the request was sent.
func attemptTransport(resp *http.Response) *proxyTransport {
	if resp == nil {
		return nil
	}
	if release, ok := resp.Body.(*releaseOnClose); ok {
		return release.proxy
	}
	return nil
}

// do performs the request and keeps the transport marked busy until the
// response body is closed, so a rotation that stops this listener's process
// waits for the stream instead of cutting it. A failed dial never opened a
// stream and releases immediately.
func (t *proxyTransport) do(req *http.Request) (*http.Response, error) {
	t.active.Add(1)
	resp, err := t.client.Do(req)
	if err != nil || resp == nil {
		t.active.Add(-1)
		return resp, err
	}
	resp.Body = &releaseOnClose{ReadCloser: resp.Body, release: func() { t.active.Add(-1) }, proxy: t}
	return resp, nil
}

func setRequestCredential(ctx context.Context, tier config.Tier, keyID, channel string, anonymous bool, proxy *proxyTransport) {
	meta := telemetry.MetaFromContext(ctx)
	if meta == nil {
		return
	}
	meta.Tier = string(tier)
	meta.KeyID = keyID
	meta.Channel = channel
	meta.Anonymous = anonymous
	meta.Proxy = ""
	if proxy != nil {
		meta.Proxy = config.RedactURL(proxy.name)
	}
}

func requestCredential(ctx context.Context) (string, string, bool) {
	meta := telemetry.MetaFromContext(ctx)
	if meta == nil {
		return "", "", false
	}
	return meta.KeyID, meta.Channel, meta.Anonymous
}

func newUpstreamRequest(ctx context.Context, baseURL string, protocol wire.Protocol, body []byte, ids identity.RequestIDs, key string) (*http.Request, error) {
	endpoint := strings.TrimRight(baseURL, "/") + wire.Path(protocol)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", httpx.UserAgent())
	req.Header.Set("x-opencode-client", "cli")
	// Measured against the live gate on 2026-10-14 with an otherwise byte-exact
	// copy of a genuine client request:
	//
	//	x-opencode-session-id dropped     -> 403 FreeTierError
	//	x-session-affinity added          -> 403 FreeTierError
	//	X-Session-Id added                -> 200 (tolerated, but not sent by the
	//	                                     opencode provider branch)
	//	an unrelated extra header added   -> 200 (so the rule is specific)
	//
	// The gateway used to send x-session-affinity and X-Session-Id because the
	// 1.18.x client emits that pair for other providers. The opencode provider
	// never does, and on 2026-10-14 the extra affinity header alone was enough
	// to have every free-tier request refused. Send the provider's own header
	// set and nothing more.
	req.Header.Set("x-opencode-session", ids.Session)
	req.Header.Set("x-opencode-session-id", ids.Session)
	req.Header.Set("x-opencode-request", ids.Request)
	req.Header.Set("x-opencode-project", ids.Project)
	if ids.ParentSession != "" {
		req.Header.Set("x-parent-session-id", ids.ParentSession)
	}
	if protocol == wire.Anthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

func isNonRetryableClientResponse(resp *http.Response, err error) bool {
	return err == nil && resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests
}

// freeTierRejectionMarker identifies the error upstream returns when it decides
// a request did not come from the official client.
const freeTierRejectionMarker = "FreeTierError"

// anonymousLaneRefusal reports whether a response is a verdict on the anonymous
// lane itself rather than on the request or on the exit that served it, and
// returns the answer worth keeping.
//
// Two answers qualify. A free-tier identity rejection says the lane's client
// fingerprint was refused; a rate limit says the lane's address block is out of
// allowance. Neither is about the exit: the lane has exactly one identity, the
// shared "public" credential plus one spoofed client fingerprint, so every
// remaining exit would answer identically. Rotating on one buys a tunnel and a
// TLS handshake per exit, and upstream counts each rejected request against the
// caller, so scanning the pool multiplies the very rejections that caused the
// first one. During the 2026-10-14 outage that turned a sub-second rejection
// into 12-184s client latencies.
//
// A request-shape rejection (400 and friends) is deliberately not recorded: it
// is a statement about the request body, so replaying it would answer a later
// request with an error that was never about it.
func anonymousLaneRefusal(resp *http.Response) (anonymousRefusal, bool) {
	if resp == nil {
		return anonymousRefusal{}, false
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return anonymousRefusal{}, false
	}
	body, err := peekResponsePrefix(resp, anonymousRefusalBodyLimit)
	if resp.StatusCode == http.StatusForbidden {
		if err != nil || !bytes.Contains(body, []byte(freeTierRejectionMarker)) {
			return anonymousRefusal{}, false
		}
	} else if err != nil {
		body = nil
	}
	return anonymousRefusal{
		status:     resp.StatusCode,
		retryAfter: resp.Header.Get("Retry-After"),
		body:       body,
	}, true
}

// refusalResponse rebuilds a recorded lane refusal so a request that never
// reached an upstream is answered exactly as one that did.
func refusalResponse(refusal anonymousRefusal) *http.Response {
	header := make(http.Header, 2)
	header.Set("Content-Type", "application/json")
	if refusal.retryAfter != "" {
		header.Set("Retry-After", refusal.retryAfter)
	}
	return &http.Response{
		StatusCode: refusal.status,
		Status:     strconv.Itoa(refusal.status) + " " + http.StatusText(refusal.status),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(refusal.body)),
	}
}

// peekResponsePrefix reads at most limit bytes of a response body and rewrites
// the response so the same bytes are served again to the next reader.
func peekResponsePrefix(resp *http.Response, limit int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("response has no body")
	}
	if release, ok := resp.Body.(*releaseOnClose); ok {
		return release.peekPrefix(limit)
	}
	buffered := make([]byte, limit)
	n, err := io.ReadFull(resp.Body, buffered)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	resp.Body = &replayBody{prefix: buffered[:n], rest: resp.Body}
	return buffered[:n], nil
}
