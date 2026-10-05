package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"opencode2api/internal/config"
	modelcatalog "opencode2api/internal/models"
	"opencode2api/internal/vless"
)

const (
	proxyHealthCheckURL      = "https://cloudflare.com/cdn-cgi/trace"
	proxyHealthCheckInterval = 15 * time.Minute
	proxyHealthCheckTimeout  = 10 * time.Second
	// proxyListSyncInterval bounds how long a rebuilt vless listener can be
	// missing from the transport pool.
	proxyListSyncInterval = 15 * time.Second
	// proxyPoolReadyTimeout bounds how long the first model refresh waits for
	// the vless pool to produce a usable listener.
	proxyPoolReadyTimeout = 30 * time.Second
	// proxyPoolSettleDelay lets a few more listeners come up before the first
	// refresh walks the pool, so it has failover options immediately.
	proxyPoolSettleDelay = 3 * time.Second
)

// syncProxyResult updates proxy health from real traffic. Only timeouts and
// connection refusals mark a proxy unavailable. Other errors and 4xx/5xx
// responses trigger a neutral URL check without being treated as proxy failure.
func (g *Gateway) syncProxyResult(ctx context.Context, proxy *proxyTransport, status int, err error) bool {
	if proxy == nil {
		return false
	}
	if isProxyFailure(err) {
		g.rebindFailedProxy(proxy)
		g.verifyProxyAfterError(ctx, proxy, status)
		return true
	}
	if status >= 200 && status < 400 {
		wasHealthy := proxy.healthy.Swap(true)
		if !wasHealthy {
			g.restoreProxy(proxy)
		}
		return false
	}
	if err != nil {
		g.verifyProxyAfterError(ctx, proxy, status)
		return false
	}
	if status >= 400 && status < 600 {
		g.verifyProxyAfterError(ctx, proxy, status)
	}
	return false
}

func (g *Gateway) verifyProxyAfterError(ctx context.Context, proxy *proxyTransport, status int) {
	if !proxy.checking.CompareAndSwap(false, true) {
		return
	}
	// The client request may finish or be cancelled while the verification is
	// running. Keep its values but give the proxy check an independent timeout.
	checkCtx := context.WithoutCancel(ctx)
	go func() {
		result := g.transports.checkClaimedProxy(checkCtx, proxy, proxyHealthCheckURL, proxyHealthCheckTimeout)
		g.applyProxyHealthResult(result, "upstream HTTP response", status)
	}()
}

func (g *Gateway) rebindFailedProxy(proxy *proxyTransport) (zenMoved, goMoved int) {
	if proxy == nil {
		return 0, 0
	}
	wasHealthy := proxy.healthy.Swap(false)
	return g.rebindUnavailableProxy(proxy, wasHealthy)
}

func (g *Gateway) rebindUnavailableProxy(proxy *proxyTransport, wasHealthy bool) (zenMoved, goMoved int) {
	index := proxy.index()
	zenMoved = g.zenNodes.RebindProxy(index)
	goMoved = g.goNodes.RebindProxy(index)
	if wasHealthy || zenMoved+goMoved > 0 {
		g.logger.Warn("proxy became unavailable", "component", "proxy", "event", "proxy_unavailable", "proxy", config.RedactURL(proxy.name), "zen_keys_moved", zenMoved, "go_keys_moved", goMoved)
	}
	return zenMoved, goMoved
}

func (g *Gateway) restoreProxy(proxy *proxyTransport) (zenMoved, goMoved int) {
	if proxy == nil {
		return 0, 0
	}
	index := proxy.index()
	zenMoved = g.zenNodes.RestoreProxy(index)
	goMoved = g.goNodes.RestoreProxy(index)
	if zenMoved+goMoved > 0 {
		g.logger.Info("proxy connectivity restored", "component", "proxy", "event", "proxy_restored", "proxy", config.RedactURL(proxy.name), "zen_keys_moved", zenMoved, "go_keys_moved", goMoved)
	}
	return zenMoved, goMoved
}

func (g *Gateway) StartProxyHealthChecks(ctx context.Context) {
	check := func() {
		results := g.transports.CheckHealth(ctx, proxyHealthCheckURL, proxyHealthCheckTimeout)
		for _, result := range results {
			g.applyProxyHealthResult(result, "scheduled health check", 0)
		}
	}
	go func() {
		ticker := time.NewTicker(proxyHealthCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}

// StartProxyConnectionRotation recycles idle upstream connections so a later
// request dials again and leaves through a new exit IP. The pool is rotated one
// listener at a time: a fresh connection costs an extra tunnel handshake, so
// staggering the work keeps that cost off nearly every request instead of
// paying it across the whole pool on the same tick. CloseIdleConnections only
// touches connections sitting in the idle pool, so an in-flight stream is never
// interrupted.
func (g *Gateway) StartProxyConnectionRotation(ctx context.Context) {
	interval := time.Duration(g.cfg.Performance.ConnectionRotationSeconds) * time.Second
	if interval <= 0 {
		return
	}
	size := g.transports.len()
	if size == 0 {
		return
	}
	tick := interval / time.Duration(size)
	if tick < time.Second {
		tick = time.Second
	}
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		position := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				proxies := g.transports.snapshot()
				if len(proxies) == 0 {
					continue
				}
				proxy := proxies[position%len(proxies)]
				position++
				if proxy == nil {
					continue
				}
				if transport, ok := proxy.client.Transport.(*http.Transport); ok {
					transport.CloseIdleConnections()
				}
			}
		}
	}()
}

// StartProxyListSync keeps the transport pool aligned with the local SOCKS5
// listeners the vless pool publishes. When the pool is disabled the desired
// list equals the resolved proxy configuration and this loop is a no-op.
func (g *Gateway) StartProxyListSync(ctx context.Context) {
	if g.vlessPool == nil || !g.vlessPool.Enabled() {
		return
	}
	reconcile := func() {
		desired := g.desiredProxies()
		if equalProxies(desired, g.transports.snapshot()) {
			return
		}
		if err := g.transports.Refresh(desired); err != nil {
			g.logger.Warn("proxy list refresh failed", "component", "proxy", "event", "proxy_list_refresh_failed", "error", err)
			return
		}
		g.logger.Info("proxy list refreshed", "component", "proxy", "event", "proxy_list_refreshed", "proxies", len(desired))
	}
	go func() {
		reconcile()
		ticker := time.NewTicker(proxyListSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcile()
			}
		}
	}()
}

// desiredProxyList is the transport address list the gateway should be using:
// the static proxies followed by the current vless listeners. A direct
// connection is used only when neither source supplies a transport, so an
// operator who removed `direct` from the configuration gets exactly the
// proxies they asked for.
func desiredProxyList(cfg config.Config, pool *vless.Pool) []string {
	list := config.UniqueStrings(cfg.StaticProxies())
	if pool != nil && pool.Enabled() {
		list = config.UniqueStrings(append(list, pool.Proxies()...))
	}
	if len(list) == 0 {
		return []string{"direct"}
	}
	return list
}

// desiredProxies is the transport address list the running gateway should be
// using right now.
func (g *Gateway) desiredProxies() []string {
	return desiredProxyList(g.cfg, g.vlessPool)
}

func equalProxies(desired []string, items []*proxyTransport) bool {
	if len(desired) != len(items) {
		return false
	}
	for i, item := range items {
		if item.name != desired[i] {
			return false
		}
	}
	return true
}

func (g *Gateway) applyProxyHealthResult(result proxyHealthResult, source string, upstreamStatus int) {
	if result.err == nil {
		if !result.wasHealthy {
			g.restoreProxy(result.proxy)
		}
		g.logger.Debug("proxy health check passed", "component", "proxy", "event", "health_check_passed", "source", source, "upstream_status", upstreamStatus, "proxy", config.RedactURL(result.proxy.name))
		return
	}
	if !result.failed {
		g.logger.Debug("proxy health check was inconclusive", "component", "proxy", "event", "health_check_inconclusive", "source", source, "upstream_status", upstreamStatus, "proxy", config.RedactURL(result.proxy.name), "error", result.err)
		return
	}
	if g.transports.hasHealthy() {
		zenMoved, goMoved := g.rebindUnavailableProxy(result.proxy, result.wasHealthy)
		if result.wasHealthy || zenMoved+goMoved > 0 {
			g.logger.Warn("proxy health check failed", "component", "proxy", "event", "health_check_failed", "source", source, "upstream_status", upstreamStatus, "proxy", config.RedactURL(result.proxy.name), "zen_keys_moved", zenMoved, "go_keys_moved", goMoved, "error", result.err)
			return
		}
	}
	g.logger.Debug("proxy health check is still failing", "component", "proxy", "event", "health_check_still_failing", "source", source, "upstream_status", upstreamStatus, "proxy", config.RedactURL(result.proxy.name), "error", result.err)
}

func (g *Gateway) StartModelRefresh(ctx context.Context) {
	go func() {
		// The vless pool rebuilds its listeners one at a time, so the first
		// refresh must not race it: an empty pool would fail every attempt and
		// leave the catalog bare until the next scheduled interval.
		g.WaitForProxyPool(ctx)
		g.RefreshModels(ctx)
		ticker := time.NewTicker(time.Duration(g.cfg.Models.RefreshSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.RefreshModels(ctx)
			}
		}
	}()
}

// RefreshModels re-reads the Zen/Go model lists and the OpenCode capability
// catalog, then swaps the result into the routing catalog. The scheduled loop
// and an operator-triggered refresh are serialized, so the upstream is never
// queried twice at once.
func (g *Gateway) RefreshModels(ctx context.Context) {
	g.refreshMu.Lock()
	defer g.refreshMu.Unlock()

	var zen, goModels []string
	var capabilities modelcatalog.Capabilities
	var capabilitiesErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); zen = g.refreshZen(ctx) }()
	go func() { defer wg.Done(); goModels = g.refreshTier(ctx, g.cfg.Upstream.Go, g.goNodes) }()
	go func() {
		defer wg.Done()
		capabilityCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		capabilities, capabilitiesErr = g.refreshProtocolCapabilities(capabilityCtx)
	}()
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	if capabilitiesErr != nil {
		g.logger.Warn("OpenCode capability catalog refresh failed", "component", "models", "event", "capability_refresh_failed", "error", capabilitiesErr)
	}
	if zen != nil || goModels != nil {
		capabilities.ApplyDocs()
		g.catalog.ReplaceWithCapabilities(zen, goModels, capabilities.Protocols, capabilities.Unsupported, capabilities.Metadata, capabilities.Docs)
		if ctx.Err() == nil {
			if err := g.catalog.SaveCache(); err != nil {
				g.logger.Warn("model catalog cache write failed", "component", "models", "event", "catalog_cache_write_failed", "error", err)
			}
		}
		g.logger.Info("model catalog refreshed", "component", "models", "event", "catalog_refreshed", "models", len(g.catalog.List()))
	}
}

// WaitForProxyPool blocks until the vless pool serves at least one listener.
// It is a no-op when the pool is disabled, so a direct or static setup keeps
// its immediate first refresh. Startup pulls that need a working route — the
// model catalog and the models.dev metadata — go through it so they do not
// race the pool and report a failure for the rest of the refresh interval.
func (g *Gateway) WaitForProxyPool(ctx context.Context) {
	if g.vlessPool == nil || !g.vlessPool.Enabled() {
		return
	}
	deadline := time.Now().Add(proxyPoolReadyTimeout)
	for time.Now().Before(deadline) {
		if g.vlessPool.Settled() && g.vlessPool.Ready() {
			select {
			case <-ctx.Done():
			case <-time.After(proxyPoolSettleDelay):
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	g.logger.Warn("proxy pool did not become ready in time; refreshing with whatever route is available",
		"component", "proxy", "event", "proxy_pool_timeout", "timeout_seconds", int(proxyPoolReadyTimeout/time.Second))
}

func (g *Gateway) refreshProtocolCapabilities(ctx context.Context) (modelcatalog.Capabilities, error) {
	if g.transports == nil || g.transports.len() == 0 {
		return modelcatalog.FetchCapabilities(ctx, &http.Client{Timeout: 30 * time.Second}, modelcatalog.CapabilitiesURL)
	}
	var lastErr error
	for _, proxy := range g.transports.snapshot() {
		if proxy == nil || !proxy.healthy.Load() {
			continue
		}
		capabilities, err := modelcatalog.FetchCapabilities(ctx, proxy.client, modelcatalog.CapabilitiesURL)
		if err == nil {
			return capabilities, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no healthy proxy available for OpenCode capability catalog")
	}
	return modelcatalog.Capabilities{}, lastErr
}

func (g *Gateway) refreshZen(ctx context.Context) []string {
	if models := g.refreshTier(ctx, g.cfg.Upstream.Zen, g.zenNodes); models != nil {
		return models
	}
	if !g.cfg.Anonymous {
		return nil
	}
	return g.refreshAnonymousTier(ctx, g.cfg.Upstream.Zen)
}

func (g *Gateway) refreshAnonymousTier(ctx context.Context, base string) []string {
	cursor := g.anonymous.CursorFor("")
	limit := g.anonymous.Len()
	for attempt := 1; attempt <= limit; attempt++ {
		node := cursor.Next()
		if node == nil {
			break
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		models, status, err := modelcatalog.FetchModels(refreshCtx, node.proxy.client, base, anonymousZenKey)
		g.syncProxyResult(refreshCtx, node.proxy, status, err)
		cancel()
		if err == nil {
			g.anonymous.MarkSuccess(node)
			return models
		}
		g.anonymous.MarkFailure(node, nil, err)
		g.logger.Debug("anonymous model catalog refresh attempt failed", "component", "models", "event", "anonymous_refresh_attempt_failed", "upstream", config.RedactURL(base), "attempt", attempt, "proxy", config.RedactURL(node.proxy.name), "error", err)
	}
	g.logger.Warn("anonymous model catalog refresh failed", "component", "models", "event", "anonymous_refresh_failed", "upstream", config.RedactURL(base))
	return nil
}

func (g *Gateway) refreshTier(ctx context.Context, base string, nodes *nodePool) []string {
	cursor := nodes.Cursor()
	for attempt := 0; attempt < g.cfg.Retry.MaxAttempts; attempt++ {
		node := cursor.Next()
		if node == nil {
			return nil
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		proxy := nodes.Proxy(node)
		if proxy == nil {
			cancel()
			return nil
		}
		models, status, err := modelcatalog.FetchModels(refreshCtx, proxy.client, base, node.key)
		g.syncProxyResult(refreshCtx, proxy, status, err)
		cancel()
		if err == nil {
			nodes.MarkSuccess(node)
			return models
		}
		nodes.MarkFailure(node, nil, err)
		g.logger.Debug("model catalog refresh attempt failed", "component", "models", "event", "refresh_attempt_failed", "upstream", config.RedactURL(base), "attempt", attempt+1, "error", err)
	}
	g.logger.Warn("model catalog refresh failed", "component", "models", "event", "refresh_failed", "upstream", config.RedactURL(base))
	return nil
}
