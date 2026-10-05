// Package models discovers model capabilities, pricing, and available routes.
package models

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"opencode2api/internal/config"
	wire "opencode2api/internal/protocol"
)

type Route struct {
	ID       string
	Tier     config.Tier
	Protocol wire.Protocol
	// Protocols is the native protocol for each possible upstream tier. Zen
	// and Go intentionally do not share one global protocol: OpenCode's
	// catalog currently exposes, for example, MiniMax through Chat on Zen and
	// Messages on Go. The request must therefore be re-encoded when a retry
	// crosses tiers.
	Protocols map[config.Tier]wire.Protocol
	Anonymous bool
	// KeyTiers is the ordered authenticated fallback plan. Anonymous requests
	// always start on Zen, then enter this list when the public credential does
	// not succeed.
	KeyTiers []config.Tier
}

type RouteDiagnostic struct {
	Model string `json:"model"`
	// Alias is the ID downstream clients address when the operator advertises
	// a stripped model name. Empty when the two are identical.
	Alias                string                        `json:"alias,omitempty"`
	RequestedProtocol    wire.Protocol                 `json:"requested_protocol,omitempty"`
	NativeProtocol       wire.Protocol                 `json:"native_protocol"`
	NativeProtocols      map[config.Tier]wire.Protocol `json:"native_protocols,omitempty"`
	ProtocolSource       string                        `json:"protocol_source"`
	AvailableZen         bool                          `json:"available_zen"`
	AvailableGo          bool                          `json:"available_go"`
	Tier                 config.Tier                   `json:"tier,omitempty"`
	Anonymous            bool                          `json:"anonymous"`
	KeyTiers             []config.Tier                 `json:"key_tiers,omitempty"`
	AnonymousEligibility AnonymousDecision             `json:"anonymous_eligibility"`
	RouteError           string                        `json:"route_error,omitempty"`
}

type Catalog struct {
	mu        sync.RWMutex
	zen       map[string]bool
	goModels  map[string]bool
	protocols map[string]wire.Protocol
	// nativeProtocols is populated from OpenCode's public model capability
	// catalog. protocols remains the user-configured override map.
	nativeProtocols map[config.Tier]map[string]wire.Protocol
	unsupported     map[config.Tier]map[string]bool
	modelMeta       map[config.Tier]map[string]Metadata
	// docs keeps the protocols published in the upstream endpoint tables. They
	// are retained separately from nativeProtocols so a refresh can fall back
	// to them: the System One models are declared only there, and losing that
	// one fetch must not make them unroutable.
	docs         map[config.Tier]map[string]wire.Protocol
	updatedAt    time.Time
	prefer       config.Tier
	pricing      *PricingStore
	cachePath    string
	cacheSource  string
	stale        bool
	refreshAfter time.Duration
}

type CatalogSnapshot struct {
	Zen         int       `json:"zen"`
	Go          int       `json:"go"`
	Total       int       `json:"total"`
	Exposed     int       `json:"exposed"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	CacheSource string    `json:"cache_source,omitempty"`
	Stale       bool      `json:"stale"`
}

func NewCatalog(prefer config.Tier, overrides map[string]string) *Catalog {
	protocols := make(map[string]wire.Protocol, len(overrides))
	for model, protocol := range overrides {
		protocols[model] = wire.Protocol(protocol)
	}
	return &Catalog{
		zen: map[string]bool{}, goModels: map[string]bool{}, protocols: protocols,
		nativeProtocols: map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}},
		unsupported:     map[config.Tier]map[string]bool{config.TierZen: {}, config.TierGo: {}},
		docs:            map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}},
		// modelMeta is written to by a refresh, so it must exist before the
		// first one lands; a nil inner map panics on assignment.
		modelMeta:   map[config.Tier]map[string]Metadata{config.TierZen: {}, config.TierGo: {}},
		prefer:      prefer,
		cacheSource: "none",
	}
}

// SetPricingStore connects routing decisions to the shared pricing metadata.
func (c *Catalog) SetPricingStore(store *PricingStore) {
	c.mu.Lock()
	c.pricing = store
	c.mu.Unlock()
}

// MetadataSnapshot reports pricing freshness without exposing the store.
func (c *Catalog) MetadataSnapshot() MetadataSnapshot {
	c.mu.RLock()
	store := c.pricing
	c.mu.RUnlock()
	if store == nil {
		return MetadataSnapshot{}
	}
	return store.Snapshot()
}

func (c *Catalog) SetCachePath(path string) {
	c.mu.Lock()
	c.cachePath = path
	c.mu.Unlock()
}

func (c *Catalog) SetRefreshInterval(interval time.Duration) {
	c.mu.Lock()
	c.refreshAfter = interval
	c.mu.Unlock()
}

func (c *Catalog) Replace(zen, goModels []string) {
	c.ReplaceWithCapabilities(zen, goModels, nil, nil, nil, nil)
}

func (c *Catalog) ReplaceWithCapabilities(zen, goModels []string, native map[config.Tier]map[string]wire.Protocol, unsupported map[config.Tier]map[string]bool, metadata map[config.Tier]map[string]Metadata, docs map[config.Tier]map[string]wire.Protocol) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if zen != nil {
		c.zen = toSet(zen)
	}
	if goModels != nil {
		c.goModels = toSet(goModels)
	}
	// Capability data is merged, never replaced wholesale. A refresh reaches the
	// machine catalog and the endpoint tables through independent upstreams, so
	// any one of them can come back partial; dropping the entries a partial
	// answer omitted would unpublish models that were routable a moment ago —
	// the System One models exist only in the endpoint tables, for example.
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		if protocols, ok := native[tier]; ok {
			for model, protocol := range protocols {
				c.nativeProtocols[tier][model] = protocol
			}
		}
		if models, ok := unsupported[tier]; ok {
			for model := range models {
				c.unsupported[tier][model] = true
			}
		}
		// A protocol declaration always beats an "unsupported" note: the
		// machine catalog can lack an SDK mapping for a model the endpoint
		// tables route explicitly.
		for model, protocol := range native[tier] {
			if protocol != "" {
				delete(c.unsupported[tier], model)
			}
		}
		if models, ok := metadata[tier]; ok {
			for model, value := range models {
				c.modelMeta[tier][model] = value
			}
		}
		if protocols, ok := docs[tier]; ok && len(protocols) > 0 {
			c.docs[tier] = cloneProtocols(protocols)
		}
	}
	// Re-apply the retained endpoint tables every time, so the models declared
	// only there keep their published protocol even if this round's table fetch
	// came back empty.
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		for model, protocol := range c.docs[tier] {
			c.nativeProtocols[tier][model] = protocol
			delete(c.unsupported[tier], model)
		}
	}
	c.updatedAt = time.Now().UTC()
	c.cacheSource = "live"
	c.stale = false
}

func (c *Catalog) CopyState(source *Catalog) {
	if source == nil {
		return
	}
	source.mu.RLock()
	zen := make(map[string]bool, len(source.zen))
	goModels := make(map[string]bool, len(source.goModels))
	for model, available := range source.zen {
		zen[model] = available
	}
	for model, available := range source.goModels {
		goModels[model] = available
	}
	native := map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}}
	unsupported := map[config.Tier]map[string]bool{config.TierZen: {}, config.TierGo: {}}
	docs := map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}}
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		for model, protocol := range source.nativeProtocols[tier] {
			native[tier][model] = protocol
		}
		for model, value := range source.unsupported[tier] {
			unsupported[tier][model] = value
		}
		for model, protocol := range source.docs[tier] {
			docs[tier][model] = protocol
		}
	}
	meta := cloneModelMeta(source.modelMeta)
	updatedAt := source.updatedAt
	cacheSource := source.cacheSource
	stale := source.stale
	source.mu.RUnlock()
	c.mu.Lock()
	c.zen, c.goModels, c.nativeProtocols, c.unsupported, c.updatedAt = zen, goModels, native, unsupported, updatedAt
	c.docs = docs
	c.modelMeta = meta
	c.cacheSource, c.stale = cacheSource, stale
	c.mu.Unlock()
}

func (c *Catalog) Route(model string, hasZenKeys, hasGoKeys, hasAnonymous bool) (Route, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.routeLocked(model, hasZenKeys, hasGoKeys, hasAnonymous)
}

func (c *Catalog) routeLocked(model string, hasZenKeys, hasGoKeys, hasAnonymous bool) (Route, error) {
	keyTiers := c.keyTierOrderLocked(model, hasZenKeys, hasGoKeys)
	// OpenCode's public credential is a Zen-only lane. Every free model starts
	// there, even if the current catalog only advertises it on Go: an upstream
	// rejection will move the request into the authenticated fallback plan.
	decision := c.anonymousDecision(model)
	if hasAnonymous && decision.Allowed && (c.protocols[model] != "" || !c.unsupported[config.TierZen][model]) &&
		(len(c.zen) == 0 && len(c.goModels) == 0 || c.zen[model] || c.goModels[model]) {
		protocols := c.protocolsForLocked(model, keyTiers, true)
		return Route{ID: model, Tier: config.TierZen, Protocol: protocols[config.TierZen], Protocols: protocols, Anonymous: true, KeyTiers: keyTiers}, nil
	}
	if len(keyTiers) > 0 {
		protocols := c.protocolsForLocked(model, keyTiers, false)
		return Route{ID: model, Tier: keyTiers[0], Protocol: protocols[keyTiers[0]], Protocols: protocols, KeyTiers: keyTiers}, nil
	}
	return Route{}, fmt.Errorf("model %q is not available in the configured Zen or Go pools", model)
}

func (r Route) ProtocolFor(tier config.Tier) wire.Protocol {
	if protocol := r.Protocols[tier]; protocol != "" {
		return protocol
	}
	return r.Protocol
}

func (c *Catalog) protocolsForLocked(model string, keyTiers []config.Tier, includeZen bool) map[config.Tier]wire.Protocol {
	protocols := make(map[config.Tier]wire.Protocol, len(keyTiers)+1)
	if includeZen {
		protocols[config.TierZen] = c.protocolForLocked(model, config.TierZen)
	}
	for _, tier := range keyTiers {
		protocols[tier] = c.protocolForLocked(model, tier)
	}
	return protocols
}

func (c *Catalog) protocolForLocked(model string, tier config.Tier) wire.Protocol {
	if protocol := c.protocols[model]; protocol != "" {
		return protocol
	}
	if protocol := c.nativeProtocols[tier][model]; protocol != "" {
		return protocol
	}
	// The OpenCode capability catalog is authoritative when available. Chat is
	// the only safe protocol-neutral fallback for an ID that has just appeared
	// in /v1/models but is not present in the capability snapshot yet.
	return wire.Chat
}

// keyTierOrderLocked builds an authenticated route in prefer order. A tier is
// included only when it has a key and advertises the model. Before the first
// successful catalog refresh, configured key pools remain usable so temporary
// discovery failures do not take the gateway offline.
func (c *Catalog) keyTierOrderLocked(model string, hasZenKeys, hasGoKeys bool) []config.Tier {
	catalogPending := len(c.zen) == 0 && len(c.goModels) == 0
	available := func(tier config.Tier) bool {
		switch tier {
		case config.TierZen:
			return hasZenKeys && (catalogPending || c.zen[model]) && c.tierSupportedLocked(model, config.TierZen)
		case config.TierGo:
			return hasGoKeys && (catalogPending || c.goModels[model]) && c.tierSupportedLocked(model, config.TierGo)
		default:
			return false
		}
	}
	order := []config.Tier{config.TierZen, config.TierGo}
	if c.prefer == config.TierGo {
		order[0], order[1] = order[1], order[0]
	}
	result := make([]config.Tier, 0, len(order))
	for _, tier := range order {
		if available(tier) {
			result = append(result, tier)
		}
	}
	return result
}

func (c *Catalog) anonymousDecision(model string) AnonymousDecision {
	if c.pricing != nil {
		return c.pricing.Decide(model)
	}
	return AnonymousDecision{Allowed: isFreeModel(model), Source: "name_fallback_metadata_pending"}
}

// IsFreeModel reports whether the catalog considers the model free-tier
// (pricing metadata, falling back to the -free name convention). Key-tier
// shaping uses it so free models without "free" in the name are covered.
func (c *Catalog) IsFreeModel(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.anonymousDecision(model).Allowed
}

func (c *Catalog) Diagnostic(model string, requested wire.Protocol, hasZenKeys, hasGoKeys, hasAnonymous bool) RouteDiagnostic {
	c.mu.RLock()
	configured, explicit := c.protocols[model]
	zen, goModel := c.zen[model], c.goModels[model]
	// Only real declarations go into this snapshot. protocolForLocked answers
	// with a Chat default for an unknown ID, and mixing that default into the
	// report is what made a Zen-only System One model read as "chat".
	declared := map[config.Tier]wire.Protocol{}
	if configured != "" {
		declared[config.TierZen], declared[config.TierGo] = configured, configured
	} else {
		if protocol := c.nativeProtocols[config.TierZen][model]; protocol != "" {
			declared[config.TierZen] = protocol
		}
		if protocol := c.nativeProtocols[config.TierGo][model]; protocol != "" {
			declared[config.TierGo] = protocol
		}
	}
	_, zenKnown := c.nativeProtocols[config.TierZen][model]
	_, goKnown := c.nativeProtocols[config.TierGo][model]
	c.mu.RUnlock()
	source := "configured"
	if !explicit {
		source = "default"
		if zenKnown || goKnown {
			source = "upstream"
		}
	}
	protocol := configured
	if protocol == "" {
		// Route() below replaces this with the protocol of the tier that will
		// actually serve the request. Only the no-route fallback is shown here,
		// and it must come from a tier that declares the model: preferring the
		// operator's tier blindly reported an unknown Go entry as the Chat
		// default even for a Zen-only System One model.
		protocol = knownProtocol(declared, c.prefer)
	}
	diagnostic := RouteDiagnostic{
		Model: model, RequestedProtocol: requested, NativeProtocol: protocol, NativeProtocols: declared, ProtocolSource: source,
		AvailableZen: zen, AvailableGo: goModel, AnonymousEligibility: c.anonymousDecision(model),
	}
	route, err := c.Route(model, hasZenKeys, hasGoKeys, hasAnonymous)
	if err != nil {
		diagnostic.RouteError = err.Error()
		return diagnostic
	}
	diagnostic.NativeProtocol = route.Protocol
	diagnostic.NativeProtocols = route.Protocols
	diagnostic.Tier, diagnostic.Anonymous = route.Tier, route.Anonymous
	diagnostic.KeyTiers = append([]config.Tier(nil), route.KeyTiers...)
	return diagnostic
}

// knownProtocol returns the operator's preferred tier when that tier declares
// the model, and otherwise the single tier that does. An empty result means no
// tier has a declaration, which is reported as-is rather than defaulted.
func knownProtocol(declared map[config.Tier]wire.Protocol, prefer config.Tier) wire.Protocol {
	if protocol := declared[prefer]; protocol != "" {
		return protocol
	}
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		if protocol := declared[tier]; protocol != "" {
			return protocol
		}
	}
	return ""
}

func isFreeModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "free")
}

func (c *Catalog) List() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := c.modelIDsLocked()
	models := ids[:0]
	for _, model := range ids {
		if c.supportedLocked(model) {
			models = append(models, model)
		}
	}
	return models
}

func (c *Catalog) modelIDsLocked() []string {
	seen := make(map[string]bool, len(c.zen)+len(c.goModels))
	for model := range c.zen {
		seen[model] = true
	}
	for model := range c.goModels {
		seen[model] = true
	}
	return sortedSetKeys(seen)
}

func (c *Catalog) Snapshot() CatalogSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked(c.modelIDsLocked())
}

// AvailableModels provides discovery and readiness with the same route
// filtering, including configured key tiers and anonymous eligibility.
func (c *Catalog) AvailableModels(hasZenKeys, hasGoKeys, hasAnonymous bool) ([]Route, CatalogSnapshot) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := c.modelIDsLocked()
	routes := make([]Route, 0, len(ids))
	for _, model := range ids {
		if !c.supportedLocked(model) {
			continue
		}
		if route, err := c.routeLocked(model, hasZenKeys, hasGoKeys, hasAnonymous); err == nil {
			routes = append(routes, route)
		}
	}
	snapshot := c.snapshotLocked(ids)
	snapshot.Exposed = len(routes)
	return routes, snapshot
}

func (c *Catalog) snapshotLocked(ids []string) CatalogSnapshot {
	exposed := 0
	for _, model := range ids {
		if c.supportedLocked(model) {
			exposed++
		}
	}
	stale := c.stale
	if !c.updatedAt.IsZero() && c.refreshAfter > 0 {
		stale = stale || time.Since(c.updatedAt) > max(2*c.refreshAfter, time.Minute)
	}
	return CatalogSnapshot{
		Zen: len(c.zen), Go: len(c.goModels), Total: len(ids), Exposed: exposed,
		UpdatedAt: c.updatedAt, CacheSource: c.cacheSource, Stale: stale,
	}
}

func (c *Catalog) Supported(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.supportedLocked(model)
}

// MetadataForTier returns the rich per-model metadata (context window,
// reasoning, tool call, modalities) captured from the opencode catalog for
// the tier that will actually serve the request. Same-named models can carry
// different limits per tier, so callers must pass route.Tier — never a
// tier-blind lookup. Anonymous routes always resolve to TierZen, which keeps
// the keyless path on Zen metadata. The zero value is returned for models
// the catalog does not describe (or before the first capability refresh).
func (c *Catalog) MetadataForTier(model string, tier config.Tier) Metadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modelMeta[tier][model]
}

func (c *Catalog) supportedLocked(model string) bool {
	if len(c.zen) == 0 && len(c.goModels) == 0 {
		return true
	}
	if c.zen[model] && c.tierSupportedLocked(model, config.TierZen) {
		return true
	}
	if c.goModels[model] && c.tierSupportedLocked(model, config.TierGo) {
		return true
	}
	return false
}

func (c *Catalog) tierSupportedLocked(model string, tier config.Tier) bool {
	if c.protocols[model] != "" {
		return true
	}
	// Only an explicit "unsupported" note from the machine catalog rules a model
	// out. Absence of a note carries no information: a refresh whose endpoint
	// tables came back empty must not read as "this model cannot be served".
	if c.unsupported[tier][model] {
		return false
	}
	return true
}

func toSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

func cloneProtocols(source map[string]wire.Protocol) map[string]wire.Protocol {
	result := make(map[string]wire.Protocol, len(source))
	for model, protocol := range source {
		result[model] = protocol
	}
	return result
}

func cloneBools(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for model, value := range source {
		result[model] = value
	}
	return result
}

func cloneModelMeta(source map[config.Tier]map[string]Metadata) map[config.Tier]map[string]Metadata {
	result := map[config.Tier]map[string]Metadata{config.TierZen: {}, config.TierGo: {}}
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		for id, md := range source[tier] {
			result[tier][id] = md
		}
	}
	return result
}

func sortedSetKeys(source map[string]bool) []string {
	result := make([]string, 0, len(source))
	for model, available := range source {
		if available {
			result = append(result, model)
		}
	}
	sort.Strings(result)
	return result
}

func cloneTierProtocols(source map[config.Tier]map[string]wire.Protocol) map[config.Tier]map[string]wire.Protocol {
	result := map[config.Tier]map[string]wire.Protocol{config.TierZen: {}, config.TierGo: {}}
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		if protocols, ok := source[tier]; ok {
			result[tier] = cloneProtocols(protocols)
		}
	}
	return result
}

func cloneTierBools(source map[config.Tier]map[string]bool) map[config.Tier]map[string]bool {
	result := map[config.Tier]map[string]bool{config.TierZen: {}, config.TierGo: {}}
	for _, tier := range []config.Tier{config.TierZen, config.TierGo} {
		if models, ok := source[tier]; ok {
			result[tier] = cloneBools(models)
		}
	}
	return result
}
