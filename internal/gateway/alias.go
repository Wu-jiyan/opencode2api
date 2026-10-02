package gateway

import (
	"encoding/json"
	"strings"

	"opencode2api/internal/jsonutil"
	modelcatalog "opencode2api/internal/models"
)

// freeSuffix is the upstream convention that marks the keyless lane of a
// model. The catalog always stores the real ID; stripping is a downstream
// facing alias only.
const freeSuffix = "-free"

// stripFreeSuffix removes a trailing "-free" from a model ID. IDs without the
// suffix, and IDs that are nothing but the suffix, are returned unchanged.
func stripFreeSuffix(model string) string {
	if len(model) <= len(freeSuffix) {
		return model
	}
	if !strings.EqualFold(model[len(model)-len(freeSuffix):], freeSuffix) {
		return model
	}
	return model[:len(model)-len(freeSuffix)]
}

// resolveModel maps a downstream model ID onto the route that serves it. An
// exact match always wins; only an unroutable ID falls back to name+"-free",
// so both the stripped alias and the real ID keep working.
func (g *Gateway) resolveModel(requested string) string {
	if requested == "" || !g.cfg.Models.StripFreeSuffix {
		return requested
	}
	if g.routable(requested) {
		return requested
	}
	if real := requested + freeSuffix; g.routable(real) {
		return real
	}
	return requested
}

// routable reports whether a model ID can be served with the current keys.
func (g *Gateway) routable(model string) bool {
	_, err := g.catalog.Route(model, len(g.cfg.ZenKeys) > 0, len(g.cfg.GoKeys) > 0, g.cfg.Anonymous)
	return err == nil
}

// freeAliases maps each advertised ID to the ID downstream clients should
// address. It is built from the models this gateway can actually serve, and a
// variant is only hidden when its stripped name is not served as well, so an
// alias never shadows a real model.
func (g *Gateway) freeAliases(routes []modelcatalog.Route) map[string]string {
	if !g.cfg.Models.StripFreeSuffix {
		return nil
	}
	served := make(map[string]bool, len(routes))
	for _, route := range routes {
		served[route.ID] = true
	}
	aliases := make(map[string]string)
	for _, route := range routes {
		alias := stripFreeSuffix(route.ID)
		if alias == route.ID || served[alias] {
			continue
		}
		aliases[route.ID] = alias
	}
	return aliases
}

// displayModel is the ID downstream clients should address for a catalog ID.
func displayModel(aliases map[string]string, model string) string {
	if alias, ok := aliases[model]; ok {
		return alias
	}
	return model
}

// applyModelAlias rewrites the request's model field to the catalog ID that
// serves it and returns the resolved ID together with the possibly rewritten
// body. The body is only rebuilt when the alias actually changed the ID, so
// the hot path is untouched when stripping is off or already in effect.
func (g *Gateway) applyModelAlias(payload map[string]any, body []byte) (string, []byte, error) {
	requested := jsonutil.StringAt(payload, "model")
	resolved := g.resolveModel(requested)
	if resolved == "" || resolved == requested {
		return resolved, body, nil
	}
	payload["model"] = resolved
	if len(body) == 0 {
		return resolved, body, nil
	}
	rewritten, err := rewriteModelField(body, resolved)
	if err != nil {
		return resolved, body, err
	}
	return resolved, rewritten, nil
}

// rewriteModelField replaces the top-level "model" value of a request body and
// leaves every other value byte-for-byte intact.
func rewriteModelField(body []byte, model string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	fields["model"] = encoded
	return json.Marshal(fields)
}
