package gateway

import (
	_ "embed"
	"encoding/json"
	"sync"

	wire "opencode2api/internal/protocol"
)

// agentToolDefinitionsJSON holds the official OpenCode core tool definitions.
//
// They are captured verbatim from a genuine client, because the free tier
// fingerprints the tool definitions themselves and refuses the whole request
// when one does not match. Measured 2026-10-14 against the live gate with an
// otherwise byte-exact copy of a real request, one variable at a time:
//
//	5 official tools                          -> 200
//	same tools with `properties` emptied      -> 403
//	same tools with descriptions emptied      -> 403
//	same tools with `$schema` removed         -> 200 (not required)
//	same tools with parameter descriptions    -> 200 (not required)
//	  emptied
//	same tools with a description truncated   -> 403 (the text must survive)
//	  to 300 characters
//	same tools with filler appended           -> 200 (so this is a containment
//	  to a description                           check, not a hash)
//	one core tool replaced by a minimal       -> 403 (one impostor is enough)
//	  definition
//
// So the official text has to be preserved, but it may be appended to, and the
// machine-specific parts are not what is being matched: rewriting the captured
// host's shell, OS name and temporary path into neutral Linux wording still
// answers 200, which is what this asset contains. Keeping the captured host's
// own path and PowerShell notes out of the shipped binary is deliberate.
//
//go:embed agenttools.json
var agentToolDefinitionsJSON []byte

var (
	agentToolDefinitionsOnce sync.Once
	agentToolDefinitions     map[string]map[string]any
)

// agentToolDefinition returns the official definition of a core tool rendered
// for the given protocol. The returned maps are shared and must be treated as
// read-only. It reports false for names the asset does not carry.
func agentToolDefinition(protocol wire.Protocol, name string) (map[string]any, bool) {
	agentToolDefinitionsOnce.Do(func() {
		var entries []map[string]any
		agentToolDefinitions = make(map[string]map[string]any)
		if err := json.Unmarshal(agentToolDefinitionsJSON, &entries); err != nil {
			return
		}
		for _, entry := range entries {
			definition, ok := entry["function"].(map[string]any)
			if !ok {
				continue
			}
			toolName, _ := definition["name"].(string)
			if toolName == "" {
				continue
			}
			agentToolDefinitions[toolName] = definition
		}
	})
	definition, ok := agentToolDefinitions[name]
	if !ok {
		return nil, false
	}
	switch protocol {
	case wire.Chat:
		return map[string]any{"type": "function", "function": definition}, true
	case wire.Anthropic:
		rendered := map[string]any{
			"name":        definition["name"],
			"description": definition["description"],
		}
		if parameters, ok := definition["parameters"]; ok {
			rendered["input_schema"] = parameters
		}
		return rendered, true
	default:
		rendered := map[string]any{"type": "function"}
		for key, value := range definition {
			rendered[key] = value
		}
		return rendered, true
	}
}
