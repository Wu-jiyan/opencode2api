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
	"sync"
	"sync/atomic"
	"time"

	"opencode2api/internal/config"
)

const subscriptionTimeout = 30 * time.Second

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
	cursor   int
	rotation int
	cancel   context.CancelFunc
	running  atomic.Bool
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
	Running   bool   `json:"running"`
}

// Status reports every slot, including ones whose Xray process is not running
// yet or was stopped because the subscription ran out of candidates.
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
			status.Node = s.node.DisplayName()
			status.Server = s.node.Endpoint()
			status.Transport = nodeTransport(s.node)
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
	go p.run(loopCtx)
}

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

// refresh pulls the subscription and rebuilds every stale slot to a node from
// the new candidate set. On failure the existing listeners are left running.
func (p *Pool) refresh(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(ctx, subscriptionTimeout)
	defer cancel()
	nodes, err := p.fetch(fetchCtx)
	if err != nil {
		p.logger.Warn("vless subscription refresh failed", "component", "vless", "event", "vless_refresh_failed", "error", err)
		return
	}
	if len(nodes) == 0 {
		p.logger.Warn("vless subscription produced no usable nodes", "component", "vless", "event", "vless_refresh_empty")
		return
	}
	p.mu.Lock()
	p.nodes = nodes
	p.mu.Unlock()

	target := min(p.cfg.Count, len(nodes))
	p.logger.Info("vless subscription refreshed", "component", "vless", "event", "vless_refresh", "nodes", len(nodes), "target", target)

	for i := 0; i < target; i++ {
		if ctx.Err() != nil {
			return
		}
		p.rebuild(ctx, i, nodes[i])
	}
	// Slots beyond the available candidate count are parked so a missing node
	// never leaves a listener pointing at a dead process.
	p.mu.Lock()
	slots := append([]*slot(nil), p.slots...)
	p.mu.Unlock()
	for i := target; i < len(slots); i++ {
		slots[i].stop()
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

// validate starts a throwaway Xray process for the node on a temporary port and
// reports whether it accepted connections. The process and its config file are
// always cleaned up.
func (p *Pool) validate(ctx context.Context, executable string, node Node, port int) bool {
	body, err := instanceConfig(node, p.cfg.Host, port)
	if err != nil {
		p.logger.Warn("vless probe config render failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return false
	}
	file, err := os.CreateTemp("", "opencode2api-vless-probe-*.json")
	if err != nil {
		p.logger.Warn("vless probe config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return false
	}
	configPath := file.Name()
	defer os.Remove(configPath)
	if _, err := file.Write(body); err != nil {
		file.Close()
		p.logger.Warn("vless probe config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return false
	}
	if err := file.Close(); err != nil {
		p.logger.Warn("vless probe config write failed", "component", "vless", "event", "vless_config_failed", "error", err)
		return false
	}
	cmd, err := p.launch(executable, configPath)
	if err != nil {
		p.logger.Warn("vless xray probe failed to start", "component", "vless", "event", "vless_start_failed", "node", node.DisplayName(), "error", err)
		return false
	}
	if !p.waitListening(ctx, cmd, p.cfg.Host, port) {
		p.logger.Warn("vless node failed validation", "component", "vless", "event", "vless_node_rejected", "node", node.DisplayName())
		return false
	}
	terminate(cmd)
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

func (s *slot) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active && s.cmd != nil
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
