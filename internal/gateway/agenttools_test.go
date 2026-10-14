package gateway

import (
	"encoding/json"
	"testing"

	wire "opencode2api/internal/protocol"
)

// The free tier fingerprints the core tool definitions, and it refuses a tool
// whose description is empty or whose `properties` is empty (measured
// 2026-10-14). These assertions are what keeps the embedded asset usable.
func TestAgentToolDefinitionsAreSubstantive(t *testing.T) {
	for _, name := range anonymousCoreTools {
		definition, ok := agentToolDefinition(wire.Chat, name)
		if !ok {
			t.Fatalf("no embedded definition for core tool %q", name)
		}
		function, ok := definition["function"].(map[string]any)
		if !ok {
			t.Fatalf("core tool %q is not rendered in the Chat shape: %#v", name, definition)
		}
		if description, _ := function["description"].(string); len(description) < 200 {
			t.Fatalf("core tool %q has a %d-character description; the gate refuses short ones",
				name, len(description))
		}
		parameters, ok := function["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("core tool %q has no parameters object", name)
		}
		properties, ok := parameters["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			t.Fatalf("core tool %q has empty properties; the gate refuses those", name)
		}
		for property, raw := range properties {
			if _, ok := raw.(map[string]any); !ok {
				t.Fatalf("core tool %q property %q is not a schema object", name, property)
			}
		}
	}
}

// A client's own definition of a core name would be an impostor and would have
// the whole request refused, so it is replaced by the official one while every
// other tool the client declared survives untouched.
func TestEnsureAnonymousToolsReplacesClientCoreTools(t *testing.T) {
	custom := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "list_dir",
			"description": "List a directory",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
			},
		},
	}
	impostor := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "bash",
			"description": "Run a command.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
	payload := map[string]any{"tools": []any{impostor, custom}}

	if !ensureAnonymousTools(payload, wire.Chat) {
		t.Fatalf("payload with an impostor core tool must be rewritten")
	}
	tools, ok := payload["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not an array after the rewrite: %#v", payload["tools"])
	}
	if len(tools) != len(anonymousCoreTools)+1 {
		t.Fatalf("got %d tools, want the custom one plus %d core tools", len(tools), len(anonymousCoreTools))
	}

	seen := map[string]map[string]any{}
	for _, raw := range tools {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tool entry is not an object: %#v", raw)
		}
		seen[anonymousToolName(wire.Chat, entry)] = entry
	}
	if len(seen) != len(tools) {
		t.Fatalf("the rewrite produced duplicate tool names: %#v", seen)
	}
	for _, name := range anonymousCoreTools {
		entry, ok := seen[name]
		if !ok {
			t.Fatalf("core tool %q is missing after the rewrite", name)
		}
		official, _ := agentToolDefinition(wire.Chat, name)
		if got, want := mustJSON(t, entry), mustJSON(t, official); got != want {
			t.Fatalf("core tool %q was not replaced by the official definition\ngot:  %s\nwant: %s",
				name, got, want)
		}
	}
	kept, ok := seen["list_dir"]
	if !ok {
		t.Fatalf("the client's own tool was dropped")
	}
	if got, want := mustJSON(t, kept), mustJSON(t, custom); got != want {
		t.Fatalf("the client's own tool was altered\ngot:  %s\nwant: %s", got, want)
	}
}

// With no tools at all the payload gains the full official core set.
func TestEnsureAnonymousToolsInjectsWhenAbsent(t *testing.T) {
	payload := map[string]any{"model": "space-bunny-free"}
	if !ensureAnonymousTools(payload, wire.Chat) {
		t.Fatalf("a payload without tools must be rewritten")
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != len(anonymousCoreTools) {
		t.Fatalf("got %#v, want %d tools", payload["tools"], len(anonymousCoreTools))
	}
	official, _ := agentToolDefinition(wire.Chat, "read")
	if got, want := mustJSON(t, tools[4]), mustJSON(t, official); got != want {
		t.Fatalf("injected tools are not the official ones\ngot:  %s\nwant: %s", got, want)
	}
}

// A payload that already carries exactly the official set is left byte-identical,
// so the hot path never re-encodes a body for nothing.
func TestEnsureAnonymousToolsLeavesOfficialSetAlone(t *testing.T) {
	payload := map[string]any{"tools": anonymousToolset(wire.Chat, nil)}
	if ensureAnonymousTools(payload, wire.Chat) {
		t.Fatalf("an already-official tool set must not be reported as changed")
	}
}

// The same asset has to render in each protocol's own tool shape.
func TestAgentToolDefinitionsRenderPerProtocol(t *testing.T) {
	anthropic, ok := agentToolDefinition(wire.Anthropic, "read")
	if !ok {
		t.Fatalf("no Anthropic rendering for read")
	}
	if _, ok := anthropic["input_schema"]; !ok {
		t.Fatalf("Anthropic rendering has no input_schema: %#v", anthropic)
	}
	if _, ok := anthropic["function"]; ok {
		t.Fatalf("Anthropic rendering must not carry a function wrapper: %#v", anthropic)
	}

	responses, ok := agentToolDefinition(wire.Responses, "read")
	if !ok {
		t.Fatalf("no Responses rendering for read")
	}
	if _, ok := responses["parameters"]; !ok {
		t.Fatalf("Responses rendering has no parameters: %#v", responses)
	}
	if _, ok := responses["function"]; ok {
		t.Fatalf("Responses rendering must not carry a function wrapper: %#v", responses)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(encoded)
}
