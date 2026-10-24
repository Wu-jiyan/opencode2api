package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	rand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/httpx"
)

type proxyTransport struct {
	// muIndex is the live position of this transport in the pool. It is
	// reloaded on every binding decision because a rebuild can renumber the
	// entries while keys still hold a reference to this struct.
	muIndex atomic.Int64
	name    string
	client  *http.Client
	healthy atomic.Bool
	// active counts the requests currently streaming through this transport. A
	// vless listener can only be torn down once it reaches zero: killing the
	// Xray process severs every tunnel it serves, which is what made long
	// streams die mid-answer when the pool rotated.
	active   atomic.Int64
	checking atomic.Bool
}

// drain closes the idle connections of this transport and waits for the
// in-flight requests to finish. It reports whether the transport went idle
// within the budget; a false result tells the caller to leave the current exit
// alone and retry the rotation later.
func (t *proxyTransport) drain(budget time.Duration) bool {
	if transport, ok := t.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	if t.active.Load() == 0 {
		return true
	}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if t.active.Load() == 0 {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return t.active.Load() == 0
}

// index reports the transport's current position in the pool.
func (t *proxyTransport) index() int {
	return int(t.muIndex.Load())
}

type transportPool struct {
	mu    sync.RWMutex
	items []*proxyTransport
	// cfg and responseHeaderTimeout are retained so the pool can rebuild its
	// transports with identical tuning when the proxy list changes at runtime.
	cfg                   config.PerformanceConfig
	responseHeaderTimeout time.Duration
}

// anonymousPool gives the shared OpenCode "public" credential an independent
// cooldown per proxy. Unlike key nodes, anonymous nodes are never rebound:
// changing proxy is the failover mechanism because Zen rate-limits them by IP.
type anonymousPool struct {
	nodes    []*anonymousNode
	next     atomic.Uint64
	cooldown time.Duration
	// refusalMu guards refusal, the lane-level verdict kept while every exit is
	// cooling so later requests can be answered without probing an upstream that
	// has already spoken about the lane rather than about one exit.
	refusalMu sync.Mutex
	refusal   *anonymousRefusal
}

type anonymousNode struct {
	proxy         *proxyTransport
	failures      atomic.Uint32
	cooldownUntil atomic.Int64
}

// anonymousRefusal is the last lane-level refusal an exit reported: a rate limit
// or a free-tier identity rejection. Both describe the lane's single credential
// and its address block rather than the exit that happened to deliver them, so
// the answer can be replayed for a later request while every exit is cooling.
//
// The scope is measured, not assumed. On 2026-10-15 the free tier refused all
// 256 addresses of the /56 with Retry-After counting down to the same 00:00 UTC
// instant; an address the pool had never used, in the same /64 as a refused
// exit, was refused on the same schedule and so was every other /64, while a
// Cloudflare WARP egress holding the same "public" credential was served
// normally. Rotating the project header changed nothing either. The verdict
// follows the client address block, so no exit can escape it.
type anonymousRefusal struct {
	status     int
	retryAfter string
	body       []byte
	deadline   time.Time
}

// anonymousRefusalBodyLimit bounds how much of a refusal is kept. Enough for the
// JSON error document, small enough that the record costs nothing.
const anonymousRefusalBodyLimit = 4096

// freeUsageLimitMarker identifies the answer upstream gives when the free tier's
// shared budget is spent:
//
//	429 {"type":"error","error":{"type":"FreeUsageLimitError","message":"Free
//	     usage limit reached. Subscribe to OpenCode Go to keep going: …
//	     https://opencode.ai/go"}}
//
// Unlike a rate limit, this verdict does not belong to the exit that delivered
// it. Measured 2026-10-24 with byte-identical requests, changing one variable at
// a time:
//
//	production IPv4 (108.186.246.70)       -> 429 FreeUsageLimitError
//	production pinned IPv6 (…:1300::2)     -> 429 FreeUsageLimitError
//	two Cloudflare egresses (socks5 exits) -> 429 FreeUsageLimitError
//	a VPS in another country, other /32    -> 429 FreeUsageLimitError
//	two fresh project identities           -> 429 FreeUsageLimitError
//	every advertised model                 -> 429 FreeUsageLimitError
//
// All of them carried the same countdown to the same reset instant, so the
// budget follows the lane's single "public" credential — not the source address,
// not the identity, not the model. That is what makes the next exit a wasted
// probe: it can only deliver this same answer.
const freeUsageLimitMarker = "FreeUsageLimitError"

// ExhaustedBudget reports whether the refusal says the free tier's shared budget
// is spent, as opposed to describing what one exit saw.
func (r anonymousRefusal) ExhaustedBudget() bool {
	return bytes.Contains(r.body, []byte(freeUsageLimitMarker))
}

type anonymousCursor struct {
	pool   *anonymousPool
	start  int
	offset int
}

func newAnonymousPool(enabled bool, transports *transportPool, cooldown time.Duration) *anonymousPool {
	pool := &anonymousPool{cooldown: cooldown}
	if !enabled || transports == nil {
		return pool
	}
	pool.nodes = make([]*anonymousNode, 0, transports.len())
	for _, proxy := range transports.snapshot() {
		pool.nodes = append(pool.nodes, &anonymousNode{proxy: proxy})
	}
	return pool
}

func (p *anonymousPool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.nodes)
}

func (p *anonymousPool) CursorFor(affinity string) anonymousCursor {
	if p == nil || len(p.nodes) == 0 {
		return anonymousCursor{pool: p}
	}
	start := 0
	if affinity == "" {
		start = int((p.next.Add(1) - 1) % uint64(len(p.nodes)))
	} else {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(affinity))
		start = int(hash.Sum64() % uint64(len(p.nodes)))
	}
	return anonymousCursor{pool: p, start: start}
}

// Next visits each healthy, non-cooling proxy at most once per cursor.
func (c *anonymousCursor) Next() *anonymousNode {
	if c.pool == nil || len(c.pool.nodes) == 0 {
		return nil
	}
	now := time.Now().UnixNano()
	for c.offset < len(c.pool.nodes) {
		node := c.pool.nodes[(c.start+c.offset)%len(c.pool.nodes)]
		c.offset++
		if node.proxy.healthy.Load() && node.cooldownUntil.Load() <= now {
			return node
		}
	}
	return nil
}

// NextUnvetted returns the next proxy regardless of its health flag or cooldown.
// It is the last resort when every exit is currently marked unavailable, and it
// exists to break a self-locking state: if no exit may be tried, the request
// fails without ever reaching an upstream, so no real outcome is produced and
// nothing can clear the flags that caused the refusal. Recovery would then
// depend solely on a scheduled health check.
func (c *anonymousCursor) NextUnvetted() *anonymousNode {
	if c.pool == nil || len(c.pool.nodes) == 0 {
		return nil
	}
	// The primary pass walks offset to the end before this is called, so it has
	// to be rewound or the fallback would find nothing left to yield. The rewind
	// happens only once the walk has run out again: rewinding on every call would
	// hand out the same exit for the whole attempt budget, so a request stuck
	// behind cooling exits would spend every probe on one of them.
	if c.offset >= len(c.pool.nodes) {
		c.offset = 0
	}
	node := c.pool.nodes[(c.start+c.offset)%len(c.pool.nodes)]
	c.offset++
	return node
}

func (p *anonymousPool) MarkSuccess(node *anonymousNode) {
	if node == nil {
		return
	}
	node.failures.Store(0)
	node.cooldownUntil.Store(0)
}

// RecordRefusal keeps a lane-level refusal and reports whether the record
// changed. The window is only ever extended: a verdict taken later in the same
// window reaches less far than the first one, and shortening the record would
// make the lane resume probing before upstream said it may.
func (p *anonymousPool) RecordRefusal(now time.Time, status int, retryAfter string, body []byte) bool {
	if p == nil || status == 0 {
		return false
	}
	deadline := now.Add(max(parseRetryAfter(retryAfter), p.cooldown))
	if !deadline.After(now) {
		deadline = now.Add(p.cooldown)
	}
	p.refusalMu.Lock()
	defer p.refusalMu.Unlock()
	if held := p.refusal; held != nil && !deadline.After(held.deadline) {
		return false
	}
	p.refusal = &anonymousRefusal{
		status:     status,
		retryAfter: retryAfter,
		body:       append([]byte(nil), body...),
		deadline:   deadline,
	}
	return true
}

// Refusal returns the recorded refusal while it still describes the lane. The
// Retry-After it carries counts down to the same deadline upstream named, so a
// client backing off from it waits exactly as long as upstream asked.
func (p *anonymousPool) Refusal(now time.Time) (anonymousRefusal, bool) {
	if p == nil {
		return anonymousRefusal{}, false
	}
	p.refusalMu.Lock()
	defer p.refusalMu.Unlock()
	held := p.refusal
	if held == nil || !now.Before(held.deadline) {
		return anonymousRefusal{}, false
	}
	kept := *held
	if kept.retryAfter != "" {
		remaining := int64(held.deadline.Sub(now).Seconds())
		if remaining < 1 {
			remaining = 1
		}
		kept.retryAfter = strconv.FormatInt(remaining, 10)
	}
	return kept, true
}

// ClearRefusal drops the recorded refusal.
func (p *anonymousPool) ClearRefusal() {
	if p == nil {
		return
	}
	p.refusalMu.Lock()
	p.refusal = nil
	p.refusalMu.Unlock()
}

// FullyCooling reports whether every exit is inside a cooldown window. A cooling
// exit cannot serve before its deadline, so while this holds no attempt can reach
// an upstream that has not just refused the lane.
func (p *anonymousPool) FullyCooling(now time.Time) bool {
	if p == nil || len(p.nodes) == 0 {
		return false
	}
	for _, node := range p.nodes {
		if node.cooldownUntil.Load() <= now.UnixNano() {
			return false
		}
	}
	return true
}

func (p *anonymousPool) MarkFailure(node *anonymousNode, resp *http.Response, err error) {
	if node == nil {
		return
	}
	// A connection that broke before a status line says nothing about the exit:
	// the request reached upstream and the answer never came back, which is one
	// dropped connection rather than this exit being unusable. Cooling the exit
	// for it is what turned a transient upstream flap into a self-sustaining
	// outage: every drop cooled an exit, the whole pool ended up inside its
	// cooldown windows, a cooling pool hands a request a single probe, and that
	// probe's failure re-armed the window it had just run out of. Reachability is
	// the only verdict a broken connection can carry, and syncProxyResult already
	// records exactly that much through isProxyFailure.
	if err != nil && !isProxyFailure(err) {
		return
	}
	if err == nil && resp != nil && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
		return
	}
	failures := node.failures.Add(1)
	delay := p.cooldown * time.Duration(1<<min(failures-1, 3))
	if resp != nil {
		if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > delay {
			delay = retryAfter
		}
	}
	node.cooldownUntil.Store(time.Now().Add(delay).UnixNano())
}

// snapshot returns the current transport list under the read lock.
func (p *transportPool) snapshot() []*proxyTransport {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.items
}

// len reports the current transport count.
func (p *transportPool) len() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.items)
}

// Refresh rebuilds the transport list, preserving the health and in-flight
// state of proxies whose address is unchanged. It keeps the proxy index stable
// for existing entries so keys bound to them are not rebound on every refresh.
func (p *transportPool) Refresh(proxies []string) error {
	next, err := newTransportPool(proxies, p.cfg, p.responseHeaderTimeout)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	existing := make(map[string]*proxyTransport, len(p.items))
	for _, item := range p.items {
		existing[item.name] = item
	}
	for index, item := range next.items {
		if previous, ok := existing[item.name]; ok {
			previous.muIndex.Store(int64(index))
			next.items[index] = previous
		}
	}
	p.items = next.items
	return nil
}

func (p *transportPool) hasHealthy() bool {
	for _, proxy := range p.snapshot() {
		if proxy.healthy.Load() {
			return true
		}
	}
	return false
}

func (p *transportPool) healthCounts() (total, healthy int) {
	items := p.snapshot()
	for _, proxy := range items {
		if proxy.healthy.Load() {
			healthy++
		}
	}
	return len(items), healthy
}

// Drain waits for the named transport to have no in-flight request left. A
// missing address drains nothing and reports success, so a listener that is
// already gone never blocks the caller.
func (p *transportPool) Drain(name string, budget time.Duration) bool {
	for _, proxy := range p.snapshot() {
		if proxy.name != name {
			continue
		}
		return proxy.drain(budget)
	}
	return true
}

// dialerFor builds the dialer for one pool entry and reports whether that
// entry names a local source address.
//
// A local source address is not a proxy server: the connection still runs
// straight to the upstream, but it leaves through the chosen address. Pinning
// the local endpoint also restricts the dialer to destinations that address can
// reach, so an IPv6 source resolves to the upstream's IPv6 addresses only, and
// an unusable entry fails over through the ordinary pool machinery.
func dialerFor(raw string, cfg config.PerformanceConfig) (*net.Dialer, bool) {
	dialer := &net.Dialer{
		Timeout:   time.Duration(cfg.ConnectTimeoutSeconds) * time.Second,
		KeepAlive: 30 * time.Second,
	}
	addr, ok := config.ParseIPv6ProxyAddr(raw)
	if !ok {
		return dialer, false
	}
	dialer.LocalAddr = &net.TCPAddr{IP: addr.AsSlice()}
	return dialer, true
}

func newTransportPool(proxies []string, cfg config.PerformanceConfig, responseHeaderTimeout time.Duration) (*transportPool, error) {
	p := &transportPool{items: make([]*proxyTransport, 0, len(proxies))}
	for _, raw := range proxies {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConns = cfg.MaxIdleConns
		transport.MaxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
		transport.MaxConnsPerHost = cfg.MaxConnsPerHost
		transport.IdleConnTimeout = idleConnTimeout(cfg)
		transport.ResponseHeaderTimeout = responseHeaderTimeout
		transport.ForceAttemptHTTP2 = true
		dialer, isLocalSource := dialerFor(raw, cfg)
		transport.DialContext = dialer.DialContext
		if isLocalSource || raw == "direct" {
			transport.Proxy = nil
		} else {
			u, err := url.Parse(raw)
			if err != nil {
				return nil, fmt.Errorf("parse proxy %s: %w", config.RedactURL(raw), err)
			}
			transport.Proxy = http.ProxyURL(u)
		}
		proxy := &proxyTransport{name: raw, client: &http.Client{Transport: transport}}
		proxy.muIndex.Store(int64(len(p.items)))
		proxy.healthy.Store(true)
		p.items = append(p.items, proxy)
	}
	p.cfg = cfg
	p.responseHeaderTimeout = responseHeaderTimeout
	return p, nil
}

// idleConnTimeout is how long an unused upstream connection is kept. A
// configured rotation shortens it so an idle connection is dropped at the
// rotation boundary; the request that follows dials a fresh entry connection
// and therefore leaves through a new exit IP.
func idleConnTimeout(cfg config.PerformanceConfig) time.Duration {
	idle := time.Duration(cfg.IdleConnTimeoutSeconds) * time.Second
	rotation := time.Duration(cfg.ConnectionRotationSeconds) * time.Second
	if rotation > 0 && rotation < idle {
		return rotation
	}
	return idle
}

type proxyHealthResult struct {
	proxy      *proxyTransport
	err        error
	failed     bool
	wasHealthy bool
}

// CheckHealth concurrently rechecks only proxies already marked unhealthy.
// Healthy proxies are skipped before a check is claimed. Any HTTP response
// from the test URL proves that the route is reachable; only a timeout or
// connection refusal keeps the proxy unhealthy.
func (p *transportPool) CheckHealth(ctx context.Context, target string, timeout time.Duration) []proxyHealthResult {
	results := make(chan proxyHealthResult, p.len())
	checks := 0
	for _, proxy := range p.snapshot() {
		if proxy.healthy.Load() || !proxy.checking.CompareAndSwap(false, true) {
			continue
		}
		// A real request may have restored the proxy between the first health
		// read and claiming this check.
		if proxy.healthy.Load() {
			proxy.checking.Store(false)
			continue
		}
		checks++
		go func() {
			results <- p.checkClaimedProxy(ctx, proxy, target, timeout)
		}()
	}
	out := make([]proxyHealthResult, 0, checks)
	for range checks {
		out = append(out, <-results)
	}
	return out
}

// checkClaimedProxy performs a check after the caller has acquired checking.
func (p *transportPool) checkClaimedProxy(ctx context.Context, proxy *proxyTransport, target string, timeout time.Duration) proxyHealthResult {
	defer proxy.checking.Store(false)
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, target, nil)
	if err == nil {
		req.Header.Set("User-Agent", httpx.UserAgent())
		resp, requestErr := proxy.client.Do(req)
		err = requestErr
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
		}
	}
	result := proxyHealthResult{proxy: proxy, err: err, wasHealthy: proxy.healthy.Load()}
	if err == nil {
		result.wasHealthy = proxy.healthy.Swap(true)
	} else if isProxyFailure(err) {
		result.failed = true
		result.wasHealthy = proxy.healthy.Swap(false)
	}
	return result
}

// isProxyFailure deliberately recognizes only failures that say the proxy route
// is unavailable: a refused or unreachable connection, or a dial that timed out.
//
// A response-header timeout is excluded on purpose. It says the upstream was
// slow to answer, which is a reason to fail this request over to another exit,
// not to record the exit as down. Recording it would take a working exit out of
// service, and once that has happened across the pool there is nothing left to
// route through: the anonymous lane would then refuse to try anything and no
// attempt could produce the outcome that clears the flags.
func isProxyFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	// Only the connection phase can prove the exit unusable, so a timeout is
	// honored only when it belongs to the dial rather than the response.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return opErr.Timeout()
	}
	return false
}

// upstreamNode keeps a key stable while allowing its proxy binding to change
// atomically when a proxy becomes unavailable.
type upstreamNode struct {
	key            string
	index          int
	preferredProxy int
	proxyIndex     atomic.Int64
	failures       atomic.Uint32
	cooldownUntil  atomic.Int64
}

type nodePool struct {
	nodes        []*upstreamNode
	transports   *transportPool
	next         atomic.Uint64
	cooldown     time.Duration
	bindingsMu   sync.Mutex
	bindingCount []int
}

// newNodePool distributes keys over proxies in round-robin order. When there
// are fewer keys than proxies, the remaining proxies are intentionally idle and
// can take over a key immediately if an active proxy fails.
func newNodePool(keys []string, transports *transportPool, cooldown time.Duration) (*nodePool, error) {
	if transports == nil || transports.len() == 0 {
		return nil, fmt.Errorf("at least one proxy transport is required")
	}
	items := transports.snapshot()
	pool := &nodePool{
		nodes:        make([]*upstreamNode, 0, len(keys)),
		transports:   transports,
		cooldown:     cooldown,
		bindingCount: make([]int, len(items)),
	}
	if len(keys) == 0 {
		return pool, nil
	}
	for i, key := range keys {
		proxyIndex := i % len(items)
		node := &upstreamNode{key: key, index: i, preferredProxy: proxyIndex}
		node.proxyIndex.Store(int64(proxyIndex))
		pool.nodes = append(pool.nodes, node)
		pool.bindingCount[proxyIndex]++
	}
	return pool, nil
}

// RestoreProxy moves keys that were originally assigned to a recovered proxy
// back to it. Proxies without an original key simply become healthy failover
// candidates again.
func (p *nodePool) RestoreProxy(recoveredProxy int) int {
	if p == nil || p.transports == nil || recoveredProxy < 0 || recoveredProxy >= p.transports.len() {
		return 0
	}
	p.bindingsMu.Lock()
	defer p.bindingsMu.Unlock()

	moved := 0
	for _, node := range p.nodes {
		current := int(node.proxyIndex.Load())
		if node.preferredProxy != recoveredProxy || current == recoveredProxy {
			continue
		}
		if current >= 0 && current < len(p.bindingCount) {
			p.bindingCount[current]--
		}
		p.bindingCount[recoveredProxy]++
		node.proxyIndex.Store(int64(recoveredProxy))
		node.failures.Store(0)
		node.cooldownUntil.Store(0)
		moved++
	}
	return moved
}

func (p *nodePool) Len() int { return len(p.nodes) }

func (p *nodePool) Proxy(node *upstreamNode) *proxyTransport {
	if p == nil || node == nil || p.transports == nil {
		return nil
	}
	index := int(node.proxyIndex.Load())
	items := p.transports.snapshot()
	if index < 0 || index >= len(items) {
		return nil
	}
	return items[index]
}

// RebindProxy moves every key currently using failedProxy to the least-loaded
// healthy proxy. Empty proxies are selected in configuration order; otherwise
// one of the least-loaded proxies is chosen at random. If every alternative is
// currently unhealthy, it still attempts the least-loaded alternative.
func (p *nodePool) RebindProxy(failedProxy int) int {
	if p == nil || p.transports == nil || p.transports.len() < 2 || failedProxy < 0 || failedProxy >= p.transports.len() {
		return 0
	}
	p.bindingsMu.Lock()
	defer p.bindingsMu.Unlock()

	moved := 0
	for _, node := range p.nodes {
		if int(node.proxyIndex.Load()) != failedProxy {
			continue
		}
		target := p.replacementLocked(failedProxy, true)
		if target < 0 {
			target = p.replacementLocked(failedProxy, false)
		}
		if target < 0 {
			continue
		}
		p.bindingCount[failedProxy]--
		p.bindingCount[target]++
		node.proxyIndex.Store(int64(target))
		node.failures.Store(0)
		node.cooldownUntil.Store(0)
		moved++
	}
	return moved
}

func (p *nodePool) replacementLocked(failedProxy int, healthyOnly bool) int {
	items := p.transports.snapshot()
	minimum := int(^uint(0) >> 1)
	candidates := make([]int, 0, len(items)-1)
	for i := range items {
		if i == failedProxy || healthyOnly && !items[i].healthy.Load() {
			continue
		}
		count := p.bindingCount[i]
		if count == 0 {
			return i
		}
		if count < minimum {
			minimum = count
			candidates = candidates[:0]
			candidates = append(candidates, i)
		} else if count == minimum {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return -1
	}
	return candidates[rand.IntN(len(candidates))]
}

type nodeCursor struct {
	pool *nodePool
	next int
}

// Cursor reserves a different starting node for each concurrent request.
// Selection is delayed until Next, so a node marked failed by the preceding
// attempt is immediately skipped. Both Cursor and Next allocate no memory.
func (p *nodePool) Cursor() nodeCursor {
	if len(p.nodes) == 0 {
		return nodeCursor{pool: p}
	}
	return nodeCursor{pool: p, next: int((p.next.Add(1) - 1) % uint64(len(p.nodes)))}
}

// CursorFor returns a cursor whose first choice is stable for the supplied
// affinity key. This keeps every turn in a conversation on the same upstream
// key while retaining Next's cooldown-aware failover behavior. Empty affinity
// keys keep the round-robin behavior used by background tasks.
func (p *nodePool) CursorFor(affinity string) nodeCursor {
	if affinity == "" || len(p.nodes) == 0 {
		return p.Cursor()
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(affinity))
	return nodeCursor{pool: p, next: int(hash.Sum64() % uint64(len(p.nodes)))}
}

func (c *nodeCursor) Next() *upstreamNode {
	if c.pool == nil || len(c.pool.nodes) == 0 {
		return nil
	}
	now := time.Now().UnixNano()
	choice := -1
	var earliest int64
	for offset := 0; offset < len(c.pool.nodes); offset++ {
		i := (c.next + offset) % len(c.pool.nodes)
		until := c.pool.nodes[i].cooldownUntil.Load()
		if until <= now {
			choice = i
			break
		}
		if choice == -1 || until < earliest {
			choice, earliest = i, until
		}
	}
	if choice < 0 {
		return nil
	}
	c.next = (choice + 1) % len(c.pool.nodes)
	return c.pool.nodes[choice]
}

func (p *nodePool) MarkSuccess(node *upstreamNode) {
	node.failures.Store(0)
	node.cooldownUntil.Store(0)
}

func (p *nodePool) MarkFailure(node *upstreamNode, resp *http.Response, err error) {
	if err == nil && resp != nil && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
		return
	}
	failures := node.failures.Add(1)
	multiplier := time.Duration(1 << min(failures-1, 3))
	delay := p.cooldown * multiplier
	if resp != nil {
		if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > delay {
			delay = retryAfter
		}
	}
	node.cooldownUntil.Store(time.Now().Add(delay).UnixNano())
}

func parseRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(time.Until(when), 0)
	}
	return 0
}
