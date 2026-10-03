// Package vless runs an upstream vless subscription as a rolling pool of local
// SOCKS5 listeners. Every listener is served by its own Xray process, so a
// listener has an independent outbound IP that changes whenever its process is
// rebuilt.
package vless

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"opencode2api/internal/config"
)

const subscriptionTimeout = 30 * time.Second

const (
	// rankProbeURL returns a tiny plain-text body, so the measurement reflects
	// connection setup rather than payload size.
	rankProbeURL = "https://cloudflare.com/cdn-cgi/trace"
	// rankTimeout is the budget for one ranking measurement. A node that cannot
	// be dialed within it is ordered last instead of being dropped.
	rankTimeout = 5 * time.Second
	// rankConcurrency bounds how many throwaway Xray processes run at once, so
	// ranking a wide preferred-domain list does not spike CPU on startup.
	rankConcurrency = 4
	rankReadLimit  = 32 << 10
)

// proxyClient builds an HTTP client that dials through a local SOCKS5 listener.
func proxyClient(rawURL string) (*http.Client, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{Transport: transport, Timeout: rankTimeout}, nil
}

type slot struct {
	index  int
	port   int
	mu     sync.Mutex
	cmd    *exec.Cmd
	node   Node
	active bool
	// claimed marks the slot as reserved by an in-flight rebuild before its
	// Xray process is adopted, so the port cannot be handed out twice.
	claimed bool
	pid     int
}

func (s *slot) proxyURL(host string) string {
	return "socks5://" + listenAddress(host, s.port)
}

// Pool owns the Xray child processes and the subscription refresh loop.
type Pool struct {
	cfg       config.VlessConfig
	logger    *slog.Logger
	dataDir   string
	configDir string

	proxyURL atomic.Pointer[[]string]
	enabled  atomic.Bool

	// pathMu guards the resolved Xray path, which is looked up on every
	// rebuild and invalidated after an automatic download.
	pathMu       sync.Mutex
	resolvedPath string

	mu       sync.Mutex
	slots    []*slot
	nodes    []Node
	// preferredOrder holds fixed-candidate addresses sorted fastest first by
	// rankFixedNodes. candidates re-applies it, so a ranking that lands after a
	// refresh still decides which node the next rebuild hands out.
	preferredOrder []string
	cursor         int
	rotation       int
	cancel         context.CancelFunc
	running        atomic.Bool
}

// New creates a pool. The local proxy list is published immediately so the
// gateway can size its transports before the first subscription fetch.
func New(cfg config.VlessConfig, configDir string, logger *slog.Logger) *Pool {
	pool := &Pool{cfg: cfg, logger: logger, dataDir: filepath.Join(configDir, "vless"), configDir: configDir}
	if !cfg.Enabled {
		pool.publish(nil)
		return pool
	}
	slots := make([]*slot, 0, cfg.Count)
	for i := 0; i < cfg.Count; i++ {
		slots = append(slots, &slot{index: i, port: cfg.PortBase + i})
	}
	pool.slots = slots
	pool.publish(pool.localProxies())
	pool.enabled.Store(true)
	return pool
}

// Enabled reports whether the pool was configured on.
func (p *Pool) Enabled() bool { return p != nil && p.enabled.Load() }

// Ready reports whether at least one listener is already serving traffic.
// Startup is sequential, so a caller that needs a working route (for example
// the first model catalog refresh) can wait for this instead of racing the
// pool's first rebuild.
func (p *Pool) Ready() bool {
	if p == nil || !p.Enabled() {
		return true
	}
	p.mu.Lock()
	slots := append([]*slot(nil), p.slots...)
	p.mu.Unlock()
	for _, s := range slots {
		s.mu.Lock()
		running := s.cmd != nil
		s.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}

// NodeStatus describes one local listener and the upstream node it currently
// serves. It is a read-only snapshot intended for diagnostics.
type NodeStatus struct {
	Index     int    `json:"index"`
	Port      int    `json:"port"`
	ProxyURL  string `json:"proxy_url"`
	Node      string `json:"node,omitempty"`
	Server    string `json:"server,omitempty"`
	Transport string `json:"transport,omitempty"`
	// Host is the origin the node currently talks to. With several endpoints
	// configured it differs per listener, which is what an operator needs to
	// see when tracking each origin's request budget.
	Host    string `json:"host,omitempty"`
	Running bool   `json:"running"`
	// Source is "fixed" for a hand-configured node and "subscription"
	// otherwise, so an operator can tell a deliberate route from a rotating
	// candidate at a glance.
	Source string `json:"source,omitempty"`
}

// Status reports every slot, including ones whose Xray process is not running
// yet or was stopped because the pool ran out of candidates.
func (p *Pool) Status() []NodeStatus {
	if p == nil || !p.Enabled() {
		return nil
	}
	p.mu.Lock()
	slots := append([]*slot(nil), p.slots...)
	p.mu.Unlock()
	out := make([]NodeStatus, 0, len(slots))
	for _, s := range slots {
		s.mu.Lock()
		status := NodeStatus{Index: s.index, Port: s.port, ProxyURL: s.proxyURL(p.cfg.Host), Running: s.cmd != nil}
		if status.Running {
			// Clones of one fixed link differ only by edge address, so the
			// address is the only label that tells them apart; the shared host
			// would render every row identically.
			status.Node = s.node.Endpoint()
			status.Server = s.node.Address
			status.Transport = nodeTransport(s.node)
			status.Host = s.node.Host
			if s.node.Fixed {
				status.Source = "fixed"
			} else {
				status.Source = "subscription"
			}
		}
		s.mu.Unlock()
		out = append(out, status)
	}
	return out
}

// nodeTransport summarizes the upstream wire format, for example "ws/tls".
func nodeTransport(node Node) string {
	network := node.Network
	if network == "" {
		network = "tcp"
	}
	security := node.Security
	if security == "" {
		security = "none"
	}
	return network + "/" + security
}

// Proxies returns the current local SOCKS5 listeners.
func (p *Pool) Proxies() []string {
	if p == nil {
		return nil
	}
	if value := p.proxyURL.Load(); value != nil {
		return *value
	}
	return nil
}

func (p *Pool) publish(list []string) {
	value := append([]string(nil), list...)
	p.proxyURL.Store(&value)
}

func (p *Pool) localProxies() []string {
	out := make([]string, 0, len(p.slots))
	for _, s := range p.slots {
		out = append(out, s.proxyURL(p.cfg.Host))
	}
	return out
}

// Start launches the refresh loop and the rolling rotation. It is safe to call
// once; the loop stops when ctx is cancelled or Stop is called.
func (p *Pool) Start(ctx context.Context) {
	if !p.Enabled() || p.running.Swap(true) {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()
	if p.cfg.AutoDownloadXray && p.xrayPath() == "" {
		// Fetch the executable in the background: the gateway must stay
		// responsive while a slow mirror is tried, and the refresh loop simply
		// keeps reporting the pool as empty until the binary lands.
		go p.downloadXray(loopCtx)
	}
	// A Cloudflare-fronted node reaches the same origin through many edge
	// addresses whose quality differs wildly, so measure them before handing
	// slots out. Doing it in the background keeps startup immediate: the pool
	// serves every candidate first and gets reordered once the ranking lands.
	go p.rankFixedNodes(loopCtx)
	go p.run(loopCtx)
}

// rankFixedNodes orders the fixed candidates by measured round-trip time so the
// fastest edge addresses take the first slots. Reachable-but-slow domains are
// the common case here — they answer, just several seconds late — and leaving
// them to chance means an unlucky request can be pinned to one for its whole
// timeout.
//
// Only fixed nodes are ranked. Subscription nodes are already an arbitrary
// rotating set, and measuring them would add a probe process per candidate on
// every refresh for no ordering benefit.
func (p *Pool) rankFixedNodes(ctx context.Context) {
	if len(p.cfg.Nodes) == 0 || len(p.cfg.PreferredDomains) < 2 {
		return
	}
	executable := p.xrayPath()
	if executable == "" {
		return
	}
	nodes, err := ParseFixedNodes(p.cfg.Nodes, p.cfg.PreferredDomains, p.cfg.Endpoints)
	if err != nil {
		return
	}
	latencies := make([]time.Duration, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, rankConcurrency)
	for i := range nodes {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			latencies[index] = p.measureNode(ctx, executable, nodes[index])
		}(i)
	}
	wg.Wait()

	// An unreachable candidate keeps its place at the end rather than being
	// dropped: a momentary edge failure should not shrink the pool below the
	// count the operator asked for.
	sort.SliceStable(nodes, func(i, j int) bool { return latencies[i] < latencies[j] })

	order := make([]string, 0, len(nodes))
	for _, node := range nodes {
		order = append(order, node.Address)
	}
	p.mu.Lock()
	p.preferredOrder = order
	nodes = p.nodes
	p.mu.Unlock()
	p.logger.Info("vless fixed nodes ranked by latency", "component", "vless", "event", "vless_ranked",
		"nodes", len(order), "fastest_ms", millisOf(latencies[0]), "slowest_ms", millisOf(latencies[len(latencies)-1]))
	// The first refresh already handed out slots in configuration order, so the
	// ranking only takes effect once those slots are pointed at the faster
	// nodes. Unchanged slots are left running.
	if len(nodes) > 0 {
		p.assign(ctx, applyPreferredOrder(nodes, order))
	}
}

// measureNode times one candidate through a throwaway listener. A node that
// cannot be dialed reports the full budget, which sorts it last instead of
// dropping it.
func (p *Pool) measureNode(ctx context.Context, executable string, node Node) time.Duration {
	port, err := freePort(p.cfg.Host)
	if err != nil {
		return rankTimeout
	}
	// The listener has to outlive the measurement, so it cannot go through
	// validate: that helper tears the process down as soon as it accepts
	// connections, which would leave the timing below dialing a dead port.
	release, ok := p.serve(ctx, executable, node, port)
	if !ok {
		return rankTimeout
	}
	defer release()

	client, err := proxyClient("socks5://" + listenAddress(p.cfg.Host, port))
	if err != nil {
		return rankTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, rankTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rankProbeURL, nil)
	if err != nil {
		return rankTimeout
	}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return rankTimeout
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, rankReadLimit))
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return rankTimeout
	}
	return elapsed
}

func millisOf(d time.Duration) int64 { return d.Milliseconds() }

// downloadXray installs the Xray executable into the standard bin/xray
// location, after which the normal path lookup finds it.
func (p *Pool) downloadXray(ctx context.Context) {
	dir := filepath.Join(p.configDir, "bin", "xray")
	if _, err := installXray(ctx, dir, p.cfg.XrayVersion, p.logger); err != nil {
		p.logger.Error("automatic xray download failed", "component", "vless",
			"event", "xray_download_failed", "directory", dir, "error", err)
		return
	}
	p.invalidatePathCache()
}

// Stop terminates every Xray process and stops the refresh loop.
func (p *Pool) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.stopAll()
}

func (p *Pool) stopAll() {
	p.mu.Lock()
	slots := append([]*slot(nil), p.slots...)
	p.mu.Unlock()
	for _, s := range slots {
		s.stop()
	}
}

func (p *Pool) run(ctx context.Context) {
	p.refresh(ctx)
	interval := time.Duration(p.cfg.RefreshSeconds) * time.Second
	rotateTicker := time.NewTicker(interval)
	refreshTicker := time.NewTicker(interval * 20)
	defer rotateTicker.Stop()
	defer refreshTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rotateTicker.C:
			p.rotate(ctx)
		case <-refreshTicker.C:
			p.refresh(ctx)
		}
	}
}

// candidates returns every node the pool may serve, from both sources. Fixed
// nodes come first so a hand-picked route keeps its slot even when the
// subscription is unreachable or returns a different set.
//
// The subscription is optional: a fixed-node-only setup never performs a
// network fetch, and a failing one degrades to the fixed nodes instead of
// leaving the pool empty.
func (p *Pool) candidates(ctx context.Context) ([]Node, []Node, error) {
	fixed, err := ParseFixedNodes(p.cfg.Nodes, p.cfg.PreferredDomains, p.cfg.Endpoints)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	order := p.preferredOrder
	p.mu.Unlock()
	if len(order) > 0 {
		fixed = applyPreferredOrder(fixed, order)
	}
	if p.cfg.Subscription == "" {
		return fixed, nil, nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
	subscription, err := p.fetch(fetchCtx)
	if err != nil {
		if len(fixed) == 0 {
			return nil, nil, err
		}
		p.logger.Warn("vless subscription refresh failed, keeping fixed nodes", "component", "vless",
			"event", "vless_refresh_failed", "error", err)
		return fixed, nil, nil
	}
	return fixed, subscription, nil
}

// refresh pulls the candidate set and rebuilds every stale slot. On failure the
// existing listeners are left running.
func (p *Pool) refresh(ctx context.Context) {
	fixed, subscription, err := p.candidates(ctx)
	if err != nil {
		p.logger.Warn("vless candidate refresh failed", "component", "vless", "event", "vless_refresh_failed", "error", err)
		return
	}
	// Subscription nodes go last so a stable fixed route is never displaced by a
	// rotating candidate.
	nodes := dedupe(append(append([]Node(nil), fixed...), subscription...))
	if len(nodes) == 0 {
		p.logger.Warn("vless pool produced no usable nodes", "component", "vless", "event", "vless_refresh_empty")
		return
	}
	target := min(p.cfg.Count, len(nodes))
	p.logger.Info("vless pool refreshed", "component", "vless", "event", "vless_refresh",
		"nodes", len(nodes), "fixed", countFixed(nodes), "target", target)
	p.mu.Lock()
	p.nodes = nodes
	p.mu.Unlock()
	p.assign(ctx, nodes)
}

// assign points slot i at nodes[i], rebuilding only the slots whose node actually
// changed. Rebuilding an unchanged slot would restart a working Xray process for
// nothing, which is why the comparison happens first.
func (p *Pool) assign(ctx context.Context, nodes []Node) {
	target := min(p.cfg.Count, len(nodes))
	p.mu.Lock()
	slots := append([]*slot(nil), p.slots...)
	p.mu.Unlock()

	reassigned := 0
	for i := 0; i < target; i++ {
		if ctx.Err() != nil {
			return
		}
		if slot := slots[i]; slot != nil && slot.serves(nodes[i]) {
			continue
		}
		p.rebuild(ctx, i, nodes[i])
		reassigned++
	}
	// Slots beyond the available candidate count are parked so a missing node
	// never leaves a listener pointing at a dead process.
	for i := target; i < len(slots); i++ {
		slots[i].stop()
	}
	if reassigned > 0 {
		p.logger.Info("vless listeners reassigned", "component", "vless", "event", "vless_assigned", "reassigned", reassigned, "total", target)
	}
}

// rotate rebuilds one batch of listeners so their outbound IP changes without
// tearing down the whole pool at once.
func (p *Pool) rotate(ctx context.Context) {
	p.mu.Lock()
	nodes := append([]Node(nil), p.nodes...)
	slots := append([]*slot(nil), p.slots...)
	rotation := p.rotation
	p.rotation += p.cfg.RotateBatch
	p.mu.Unlock()
	if len(nodes) == 0 || len(slots) == 0 {
		return
	}
	target := min(p.cfg.Count, len(nodes))
	if target == 0 {
		return
	}
	for i := 0; i < p.cfg.RotateBatch; i++ {
		if ctx.Err() != nil {
			return
		}
		position := (rotation + i) % target
		p.rebuild(ctx, position, nodes[position])
	}
	p.logger.Debug("vless pool rotated", "component", "vless", "event", "vless_rotated", "batch", p.cfg.RotateBatch, "start", rotation%target)
}

// rebuild replaces the listener in one slot, tearing the old Xray process down
// only once the replacement is proven to accept connections. The replacement
// is first validated on a throwaway port so a node that refuses to connect
// never leaves the slot without a working listener.
func (p *Pool) rebuild(ctx context.Context, position int, node Node) {
	p.mu.Lock()
	if position >= len(p.slots) {
		p.mu.Unlock()
		return
	}
	target := p.slots[position]
	target.claimed = true
	socket := target.port
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		target.claimed = false
		p.mu.Unlock()
	}()

	executable := p.xrayPath()
	if executable == "" {
		p.logger.Warn("vless xray executable not found", "component", "vless", "event", "vless_xray_missing", "configured", p.cfg.XrayPath)
		return
	}
	// Validate the node on a temporary port first. The slot keeps serving its
	// previous exit until the new one is proven reachable.
	probePort, err := freePort(p.cfg.Host)
	if err != nil {
		p.logger.Warn("vless no spare port for rebuild", "component", "vless", "event", "vless_port_unavailable", "error", err)
		return
	}
	if !p.validate(ctx, executable, node, probePort) {
		return
	}

	// The node is reachable: stop whatever the slot runs now, then bind it to
	// its own stable port so the published proxy list never moves.
	target.stop()
	configPath, cleanup, err := p.writeConfig(target.index, node, socket)
	if err != nil {
		p.logger.Warn("vless instance config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return
	}
	defer cleanup()
	cmd, err := p.launch(executable, configPath)
	if err != nil {
		p.logger.Warn("vless xray start failed", "component", "vless", "event", "vless_start_failed", "node", node.DisplayName(), "error", err)
		return
	}
	if !p.waitListening(ctx, cmd, p.cfg.Host, socket) {
		p.logger.Warn("vless xray did not become ready", "component", "vless", "event", "vless_start_timeout", "node", node.DisplayName(), "port", socket)
		return
	}
	target.adopt(cmd, node, socket)
	p.mu.Lock()
	p.publish(p.localProxies())
	p.mu.Unlock()
	p.logger.Info("vless listener ready", "component", "vless", "event", "vless_listener_ready", "index", target.index, "node", node.DisplayName(), "port", socket)
}

// serve starts an Xray process for the node on the given port and reports
// whether it began accepting connections. The returned function stops the
// process and removes its config file; callers that need a live listener (a
// latency measurement) must keep it running until they are done.
func (p *Pool) serve(ctx context.Context, executable string, node Node, port int) (func(), bool) {
	body, err := instanceConfig(node, p.cfg.Host, port)
	if err != nil {
		p.logger.Warn("vless probe config render failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return nil, false
	}
	file, err := os.CreateTemp("", "opencode2api-vless-probe-*.json")
	if err != nil {
		p.logger.Warn("vless probe config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return nil, false
	}
	configPath := file.Name()
	if _, err := file.Write(body); err != nil {
		file.Close()
		os.Remove(configPath)
		p.logger.Warn("vless probe config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return nil, false
	}
	if err := file.Close(); err != nil {
		os.Remove(configPath)
		p.logger.Warn("vless probe config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return nil, false
	}
	cmd, err := p.launch(executable, configPath)
	if err != nil {
		os.Remove(configPath)
		p.logger.Warn("vless xray probe failed to start", "component", "vless", "event", "vless_start_failed", "node", node.DisplayName(), "error", err)
		return nil, false
	}
	if !p.waitListening(ctx, cmd, p.cfg.Host, port) {
		terminate(cmd)
		os.Remove(configPath)
		p.logger.Warn("vless node failed validation", "component", "vless", "event", "vless_node_rejected", "node", node.DisplayName())
		return nil, false
	}
	return func() {
		terminate(cmd)
		_ = os.Remove(configPath)
	}, true
}

// validate starts a throwaway Xray process for the node on a temporary port and
// reports whether it accepted connections. The process and its config file are
// always cleaned up.
func (p *Pool) validate(ctx context.Context, executable string, node Node, port int) bool {
	release, ok := p.serve(ctx, executable, node, port)
	if !ok {
		return false
	}
	release()
	return true
}

// xrayPath resolves the Xray executable. The result is cached because it is
// consulted on every rebuild; invalidatePathCache drops the entry after an
// automatic download so the next lookup sees the new binary.
func (p *Pool) xrayPath() string {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if p.resolvedPath != "" {
		return p.resolvedPath
	}
	p.resolvedPath = p.lookupXrayPath()
	return p.resolvedPath
}

func (p *Pool) invalidatePathCache() {
	p.pathMu.Lock()
	p.resolvedPath = ""
	p.pathMu.Unlock()
}

func (p *Pool) lookupXrayPath() string {
	if p.cfg.XrayPath != "" {
		if filepath.IsAbs(p.cfg.XrayPath) {
			return p.cfg.XrayPath
		}
		return filepath.Join(p.configDir, p.cfg.XrayPath)
	}
	name := xrayExecutableName()
	candidates := []string{
		filepath.Join(p.configDir, "bin", "xray", name),
		filepath.Join("bin", "xray", name),
	}
	for _, candidate := range candidates {
		if resolved, err := filepath.Abs(candidate); err == nil {
			if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
				return resolved
			}
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	return ""
}

func (p *Pool) writeConfig(index int, node Node, port int) (string, func(), error) {
	body, err := instanceConfig(node, p.cfg.Host, port)
	if err != nil {
		return "", func() {}, err
	}
	if err := os.MkdirAll(p.dataDir, 0o755); err != nil {
		return "", func() {}, err
	}
	name := fmt.Sprintf("xray-%d.json", index)
	path := filepath.Join(p.dataDir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func (p *Pool) launch(executable, configPath string) (*exec.Cmd, error) {
	cmd := exec.Command(executable, "run", "-config", configPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (p *Pool) waitListening(ctx context.Context, cmd *exec.Cmd, host string, port int) bool {
	deadline := time.Now().Add(time.Duration(p.cfg.StartupTimeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			terminate(cmd)
			return false
		}
		if !isPortFree(host, port) {
			return true
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return false
		}
		time.Sleep(120 * time.Millisecond)
	}
	terminate(cmd)
	return false
}

func (p *Pool) fetch(ctx context.Context) ([]Node, error) {
	client := p.subscriptionClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.Subscription, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "opencode2api/vless")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("subscription returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	nodes, err := ParseSubscription(body)
	if err != nil {
		return nil, err
	}
	return dedupe(nodes), nil
}

func (p *Pool) subscriptionClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if p.cfg.SubscriptionProxy != "" {
		if parsed, err := url.Parse(p.cfg.SubscriptionProxy); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		} else if p.logger != nil {
			p.logger.Warn("vless subscription proxy ignored", "component", "vless", "event", "vless_proxy_invalid", "error", err)
		}
	}
	return &http.Client{Transport: transport, Timeout: subscriptionTimeout}
}

func dedupe(nodes []Node) []Node {
	seen := make(map[string]struct{}, len(nodes))
	out := nodes[:0]
	for _, node := range nodes {
		key := node.Fingerprint()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, node)
	}
	return out
}

func countFixed(nodes []Node) int {
	total := 0
	for _, node := range nodes {
		if node.Fixed {
			total++
		}
	}
	return total
}

func (s *slot) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active && s.cmd != nil
}

// serves reports whether the slot already runs this exact node, so a rebuild can
// be skipped instead of restarting a healthy process for nothing.
func (s *slot) serves(node Node) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil && s.node.Fingerprint() == node.Fingerprint()
}

func (s *slot) adopt(cmd *exec.Cmd, node Node, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd != cmd {
		terminate(s.cmd)
	}
	s.cmd = cmd
	s.node = node
	s.port = port
	s.active = cmd != nil
	s.claimed = false
	if cmd != nil && cmd.Process != nil {
		s.pid = cmd.Process.Pid
	}
}

func (s *slot) orphan() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmd = nil
	s.active = false
	s.claimed = false
	s.pid = 0
}

func (s *slot) stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.cmd = nil
	s.active = false
	s.claimed = false
	s.pid = 0
	s.mu.Unlock()
	if cmd != nil {
		terminate(cmd)
	}
}

func isPortFree(host string, port int) bool {
	listener, err := net.Listen("tcp", listenAddress(host, port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func freePort(host string) (int, error) {
	listener, err := net.Listen("tcp", listenAddress(host, 0))
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", listener.Addr())
	}
	return address.Port, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
