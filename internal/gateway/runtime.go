package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"opencode2api/internal/config"
	modelcatalog "opencode2api/internal/models"
	"opencode2api/internal/telemetry"
	"opencode2api/internal/vless"
)

type gatewayRuntime struct {
	config  config.Config
	gateway *Gateway
	handler http.Handler
	cancel  context.CancelFunc
}

type ApplyResult struct {
	Applied         bool     `json:"applied"`
	RestartRequired bool     `json:"restart_required"`
	RestartFields   []string `json:"restart_fields,omitempty"`
}

type RuntimeManager struct {
	configPath string
	root       context.Context
	logger     *slog.Logger
	monitor    *telemetry.Monitor
	hub        *telemetry.LogHub
	redactor   *config.SecretRedactor
	level      *slog.LevelVar
	current    atomic.Pointer[gatewayRuntime]
	updateMu   sync.Mutex
	effective  effectiveListeners
	metadata   *modelcatalog.PricingStore
}

type effectiveListeners struct {
	API          string
	WebUI        string
	WebUIEnabled bool
}

func NewRuntimeManager(root context.Context, configPath string, cfg config.Config, logger *slog.Logger, monitor *telemetry.Monitor, hub *telemetry.LogHub, redactor *config.SecretRedactor, level *slog.LevelVar) (*RuntimeManager, error) {
	manager := &RuntimeManager{
		configPath: configPath, root: root, logger: logger, monitor: monitor, hub: hub, redactor: redactor, level: level,
		effective: effectiveListeners{API: cfg.Listen, WebUI: cfg.WebUI.Listen, WebUIEnabled: cfg.WebUI.Enabled},
	}
	manager.metadata = modelcatalog.NewPricingStore(configPath, logger)
	// models.dev refreshes ride the active runtime's healthy proxy transports
	// and fall back to the store's direct client when none are available.
	manager.metadata.SetClientProvider(func() []*http.Client {
		runtime := manager.current.Load()
		if runtime == nil || runtime.gateway == nil || runtime.gateway.transports == nil {
			return nil
		}
		clients := make([]*http.Client, 0, runtime.gateway.transports.len())
		for _, proxy := range runtime.gateway.transports.snapshot() {
			if proxy != nil && proxy.healthy.Load() {
				clients = append(clients, proxy.client)
			}
		}
		return clients
	})
	// The first metadata fetch waits for the proxy pool for the same reason the
	// model catalog does: a fetch fired against listeners that are still
	// starting fails and stays reported until the next 24h tick.
	manager.metadata.SetProxyWait(func(ctx context.Context) {
		runtime := manager.current.Load()
		if runtime == nil || runtime.gateway == nil {
			return
		}
		runtime.gateway.WaitForProxyPool(ctx)
	})
	if cfg.WebUI.Password != "" {
		hash, err := config.HashPassword(cfg.WebUI.Password)
		if err != nil {
			return nil, fmt.Errorf("hash webui password: %w", err)
		}
		cfg.WebUI.PasswordHash = hash
		cfg.WebUI.Password = ""
		if err := config.SaveAtomic(configPath, cfg); err != nil {
			return nil, fmt.Errorf("persist webui password migration: %w", err)
		}
		// The automatically-created backup contains the one-time plaintext
		// bootstrap password and must not be retained.
		_ = os.Remove(configPath + ".bak")
	}
	runtime, err := manager.build(cfg)
	if err != nil {
		return nil, err
	}
	if err := runtime.gateway.catalog.LoadCache(modelcatalog.CatalogCachePath(configPath)); err != nil && !os.IsNotExist(err) {
		if manager.logger != nil {
			manager.logger.Warn("model catalog cache ignored", "component", "models", "event", "catalog_cache_load_failed", "path", modelcatalog.CatalogCachePath(configPath), "error", err)
		}
	}
	manager.current.Store(runtime)
	manager.redactor.Replace(cfg)
	telemetry.SetLogLevel(manager.level, cfg.Logging.Level)
	manager.start(runtime)
	manager.metadata.Start(root)
	return manager, nil
}

func (m *RuntimeManager) build(cfg config.Config) (*gatewayRuntime, error) {
	gateway, err := New(cfg, m.logger, m.monitor)
	if err != nil {
		return nil, err
	}
	gateway.catalog.SetPricingStore(m.metadata)
	gateway.catalog.SetCachePath(modelcatalog.CatalogCachePath(m.configPath))
	return &gatewayRuntime{config: cfg, gateway: gateway, handler: gateway.Handler(), cancel: func() {}}, nil
}

func (m *RuntimeManager) start(runtime *gatewayRuntime) {
	// Store the context through a lightweight wrapper so cancel stops catalog
	// and proxy checks while in-flight HTTP requests continue on the old pools.
	runtimeCtx, cancel := context.WithCancel(m.root)
	runtime.cancel = cancel
	// The transport pool was already built from the full desired proxy list
	// (static proxies + vless listeners) by New(), so no refresh is needed
	// here; refreshing with the static list alone would drop the listeners.
	runtime.gateway.StartVlessPool(runtimeCtx)
	runtime.gateway.StartProxyListSync(runtimeCtx)
	runtime.gateway.StartProxyConnectionRotation(runtimeCtx)
	runtime.gateway.StartProxyHealthChecks(runtimeCtx)
	runtime.gateway.StartModelRefresh(runtimeCtx)
	go func() {
		<-runtimeCtx.Done()
		runtime.gateway.Stop()
	}()
}

func (m *RuntimeManager) Handler() http.Handler {
	dynamic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtime := m.current.Load()
		if runtime == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		runtime.handler.ServeHTTP(w, r)
	})
	return telemetry.Middleware(m.monitor, m.logger, dynamic)
}

// Redact removes configured credentials from diagnostic output.
func (m *RuntimeManager) Redact(value string) string {
	return m.redactor.String(value)
}

func (m *RuntimeManager) Config() config.Config {
	runtime := m.current.Load()
	if runtime == nil {
		return config.Config{}
	}
	return config.Clone(runtime.config)
}

func (m *RuntimeManager) RestartStatus() (effectiveListeners, []string) {
	cfg := m.Config()
	fields := make([]string, 0, 3)
	if cfg.Listen != m.effective.API {
		fields = append(fields, "listen")
	}
	if cfg.WebUI.Listen != m.effective.WebUI {
		fields = append(fields, "webui.listen")
	}
	if cfg.WebUI.Enabled != m.effective.WebUIEnabled {
		fields = append(fields, "webui.enabled")
	}
	return m.effective, fields
}

func (m *RuntimeManager) Apply(candidate config.Config, persist bool) (ApplyResult, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()

	current := m.current.Load()
	hadPlaintextPassword := candidate.WebUI.Password != ""
	if hadPlaintextPassword {
		hash, err := config.HashPassword(candidate.WebUI.Password)
		if err != nil {
			return ApplyResult{}, err
		}
		candidate.WebUI.PasswordHash = hash
		candidate.WebUI.Password = ""
	}
	normalized, err := config.Normalize(m.configPath, candidate)
	if err != nil {
		return ApplyResult{}, err
	}
	next, err := m.build(normalized)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("initialize runtime: %w", err)
	}
	if current != nil {
		next.gateway.catalog.CopyState(current.gateway.catalog)
	}
	if persist || hadPlaintextPassword {
		if err := config.SaveAtomic(m.configPath, normalized); err != nil {
			next.cancel()
			return ApplyResult{}, err
		}
	}

	result := ApplyResult{Applied: true}
	if normalized.Listen != m.effective.API {
		result.RestartFields = append(result.RestartFields, "listen")
	}
	if normalized.WebUI.Listen != m.effective.WebUI {
		result.RestartFields = append(result.RestartFields, "webui.listen")
	}
	if normalized.WebUI.Enabled != m.effective.WebUIEnabled {
		result.RestartFields = append(result.RestartFields, "webui.enabled")
	}
	result.RestartRequired = len(result.RestartFields) > 0
	m.redactor.Replace(normalized)
	telemetry.SetLogLevel(m.level, normalized.Logging.Level)
	if current == nil || normalized.Logging.RingSize != current.config.Logging.RingSize {
		m.hub.Resize(normalized.Logging.RingSize)
	}
	m.start(next)
	previous := m.current.Swap(next)
	if previous != nil {
		previous.cancel()
	}
	m.logger.Info("configuration applied", "component", "config", "event", "config_applied", "restart_required", result.RestartRequired, "restart_fields", result.RestartFields)
	return result, nil
}

func (m *RuntimeManager) Reload() (ApplyResult, error) {
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return ApplyResult{}, err
	}
	hadPlaintextPassword := cfg.WebUI.Password != ""
	result, err := m.Apply(cfg, false)
	if err == nil && hadPlaintextPassword {
		_ = os.Remove(m.configPath + ".bak")
	}
	return result, err
}

func (m *RuntimeManager) Shutdown() {
	if current := m.current.Load(); current != nil {
		current.cancel()
	}
}

type ResourceSnapshot struct {
	Models    modelcatalog.CatalogSnapshot  `json:"models"`
	Keys      []KeyStatus                   `json:"keys"`
	Proxies   []ProxyStatus                 `json:"proxies"`
	Anonymous bool                          `json:"anonymous"`
	Metadata  modelcatalog.MetadataSnapshot `json:"metadata"`
}

type KeyStatus struct {
	ID            string     `json:"id"`
	Tier          string     `json:"tier"`
	Index         int        `json:"index"`
	ProxyIndex    int        `json:"proxy_index"`
	Failures      uint32     `json:"failures"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
}

type ProxyStatus struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	// Address is the local transport the gateway dials through, while Node and
	// Server describe the upstream a vless listener currently serves.
	Address   string `json:"address"`
	Node      string `json:"node,omitempty"`
	Server    string `json:"server,omitempty"`
	Transport string `json:"transport,omitempty"`
	Healthy   bool   `json:"healthy"`
	Checking  bool   `json:"checking"`
	ZenKeys   int    `json:"zen_keys"`
	GoKeys    int    `json:"go_keys"`
	Anonymous bool   `json:"anonymous"`
}

func (m *RuntimeManager) Resources() ResourceSnapshot {
	runtime := m.current.Load()
	if runtime == nil {
		return ResourceSnapshot{}
	}
	gateway := runtime.gateway
	_, models := gateway.availableModels()
	result := ResourceSnapshot{Models: models, Anonymous: gateway.cfg.Anonymous}
	result.Metadata = gateway.catalog.MetadataSnapshot()
	result.Keys = append(result.Keys, keyStatuses("zen", gateway.zenNodes)...)
	result.Keys = append(result.Keys, keyStatuses("go", gateway.goNodes)...)
	gateway.zenNodes.bindingsMu.Lock()
	zenBindings := append([]int(nil), gateway.zenNodes.bindingCount...)
	gateway.zenNodes.bindingsMu.Unlock()
	gateway.goNodes.bindingsMu.Lock()
	goBindings := append([]int(nil), gateway.goNodes.bindingCount...)
	gateway.goNodes.bindingsMu.Unlock()
	// The vless pool labels each local listener with the upstream node it
	// currently serves, keyed by the proxy URL the transport pool knows.
	var nodes map[string]vless.NodeStatus
	if gateway.vlessPool != nil && gateway.vlessPool.Enabled() {
		nodes = make(map[string]vless.NodeStatus)
		for _, node := range gateway.vlessPool.Status() {
			nodes[node.ProxyURL] = node
		}
	}
	for _, proxy := range gateway.transports.snapshot() {
		index := proxy.index()
		status := ProxyStatus{Index: index, Kind: "static", Address: config.RedactURL(proxy.name), Healthy: proxy.healthy.Load(), Checking: proxy.checking.Load(), Anonymous: gateway.cfg.Anonymous}
		if proxy.name == "direct" {
			status.Kind = "direct"
		}
		if node, ok := nodes[proxy.name]; ok {
			status.Kind = "vless"
			status.Node = node.Node
			status.Server = node.Server
			status.Transport = node.Transport
		}
		if index < len(zenBindings) {
			status.ZenKeys = zenBindings[index]
		}
		if index < len(goBindings) {
			status.GoKeys = goBindings[index]
		}
		result.Proxies = append(result.Proxies, status)
	}
	return result
}

// ProbeProxies measures the time to the first response byte of every configured
// proxy. Nil is returned when no runtime is active.
func (m *RuntimeManager) ProbeProxies(ctx context.Context, timeout time.Duration) []ProxyProbe {
	runtime := m.current.Load()
	if runtime == nil || runtime.gateway == nil {
		return nil
	}
	return runtime.gateway.ProbeProxies(ctx, timeout)
}

// RefreshModels forces an immediate, complete upstream refresh on the active
// runtime: the Zen/Go model catalog and the models.dev pricing metadata. The
// routing table shows both side by side, so refreshing one without the other
// would leave the page half stale. It returns once both finished or ctx expired.
func (m *RuntimeManager) RefreshModels(ctx context.Context) {
	runtime := m.current.Load()
	if runtime == nil || runtime.gateway == nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runtime.gateway.RefreshModels(ctx)
	}()
	m.metadata.RefreshAndLog(ctx)
	wg.Wait()
}

func (m *RuntimeManager) DebugModels() ([]modelcatalog.RouteDiagnostic, modelcatalog.MetadataSnapshot) {
	runtime := m.current.Load()
	if runtime == nil {
		return nil, modelcatalog.MetadataSnapshot{}
	}
	gateway := runtime.gateway
	models := gateway.catalog.List()
	routes, _ := gateway.availableModels()
	aliases := gateway.freeAliases(routes)
	result := make([]modelcatalog.RouteDiagnostic, 0, len(models))
	for _, model := range models {
		diagnostic := gateway.catalog.Diagnostic(model, "", len(gateway.cfg.ZenKeys) > 0, len(gateway.cfg.GoKeys) > 0, gateway.cfg.Anonymous)
		if alias, ok := aliases[model]; ok {
			diagnostic.Alias = alias
		}
		result = append(result, diagnostic)
	}
	metadata := gateway.catalog.MetadataSnapshot()
	return result, metadata
}

func keyStatuses(tier string, pool *nodePool) []KeyStatus {
	result := make([]KeyStatus, 0, len(pool.nodes))
	for _, node := range pool.nodes {
		status := KeyStatus{ID: config.KeyDisplayID(node.key), Tier: tier, Index: node.index, ProxyIndex: int(node.proxyIndex.Load()), Failures: node.failures.Load()}
		if until := node.cooldownUntil.Load(); until > time.Now().UnixNano() {
			value := time.Unix(0, until).UTC()
			status.CooldownUntil = &value
		}
		result = append(result, status)
	}
	return result
}
