// Package config loads, validates, persists, and redacts service configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opencode2api/internal/jsonutil"
	wire "opencode2api/internal/protocol"
)

type Config struct {
	Listen      string            `json:"listen"`
	ServerKeys  []string          `json:"server_keys"`
	ZenKeys     []string          `json:"zen_keys"`
	GoKeys      []string          `json:"go_keys"`
	Anonymous   bool              `json:"anonymous"`
	Proxies     []string          `json:"proxies"`
	ProxyFile   string            `json:"proxyfile"`
	Vless       VlessConfig       `json:"vless"`
	Upstream    UpstreamConfig    `json:"upstream"`
	Retry       RetryConfig       `json:"retry"`
	Models      ModelsConfig      `json:"models"`
	Performance PerformanceConfig `json:"performance"`
	Logging     LoggingConfig     `json:"logging"`
	WebUI       WebUIConfig       `json:"webui"`
	Prefer      Tier              `json:"prefer"`
	// Reasoning configures a forced thinking level. Both fields are optional
	// and empty by default, so an existing configuration keeps the client's own
	// level untouched.
	Reasoning ReasoningConfig `json:"reasoning"`

	effectiveProxies []string
	// dir is the directory of the configuration file the value was loaded
	// from. Resolved-only: it is never serialized.
	dir string
}

// ReasoningConfig forces a thinking level for requests that do not state one.
//
// Effort is the default for every model; EffortByModel overrides it for the
// model IDs it names. A level a client sends explicitly (Anthropic's
// output_config.effort / top-level effort, or Chat's reasoning_effort) always
// wins, so a forced level only replaces a value that would otherwise have been
// derived from thinking.budget_tokens or left unset.
type ReasoningConfig struct {
	// omitempty keeps a saved configuration free of the fields the operator
	// never set, matching how the rest of the optional surface is persisted.
	Effort        string            `json:"effort,omitempty"`
	EffortByModel map[string]string `json:"effort_by_model,omitempty"`
}

type UpstreamConfig struct {
	Zen string `json:"zen"`
	Go  string `json:"go"`
}

// VlessConfig turns an upstream vless subscription into a rolling pool of
// local SOCKS5 listeners. Each enabled pool node is served by its own Xray
// child process bound to a loopback port, so every node has an independent
// outbound IP that changes whenever its process is rebuilt.
type VlessConfig struct {
	Enabled bool `json:"enabled"`
	// Subscription is an http(s) URL returning either a base64 v2ray
	// subscription or a Clash/mihomo YAML proxy list. It is optional when
	// Nodes supplies every candidate, so a fixed-node setup needs no URL.
	Subscription string `json:"subscription"`
	// Nodes are fixed vless:// share links used instead of, or alongside, a
	// subscription. A link whose address is a Cloudflare preferred domain is
	// expanded across PreferredDomains, which is how one fixed node yields
	// several independent outbound routes.
	Nodes []string `json:"nodes"`
	// PreferredDomains are Cloudflare preferred domains (or hand-picked IPs) to
	// substitute for a node's address. The rest of the link — UUID, SNI, host,
	// path, transport — is preserved, because only the edge address changes.
	PreferredDomains []string `json:"preferred_domains"`
	// Endpoints are alternative origins for the same service, for example
	// several edn2/edn3-style hostnames. Candidates receive them round-robin so
	// each origin's request budget is spent at an even rate instead of draining
	// the first one. Empty keeps every candidate on the host its link named.
	Endpoints []string `json:"endpoints"`
	// Count is how many local listeners stay alive at once.
	Count int `json:"count"`
	// RefreshSeconds is the base interval of the rolling rebuild loop.
	RefreshSeconds int `json:"refresh_seconds"`
	// RotateBatch is how many listeners are rebuilt per cycle. It bounds how
	// much of the pool is torn down at once: smaller values keep the pool
	// mostly warm, larger values rotate the whole pool sooner.
	RotateBatch int `json:"rotate_batch"`
	// XrayPath is the Xray executable. A relative path resolves against the
	// configuration file directory; an empty value looks for bin/xray/xray[.exe]
	// next to the configuration file and on PATH.
	XrayPath string `json:"xray_path"`
	// AutoDownloadXray fetches the official Xray release when no executable is
	// found, so a fresh deployment works without a manual download. The archive
	// is verified against the SHA2-256 published next to it before anything is
	// written to disk.
	AutoDownloadXray bool `json:"auto_download_xray"`
	// XrayVersion pins the release tag used by the automatic download, for
	// example "v26.3.27". Empty means the latest release.
	XrayVersion string `json:"xray_version"`
	// PortBase is the first loopback port assigned to a listener. Node i uses
	// PortBase+i.
	PortBase int `json:"port_base"`
	// Host is the address the local SOCKS5 listeners bind to.
	Host string `json:"host"`
	// SubscriptionProxy optionally routes the subscription fetch itself through
	// a proxy URL (for example a static socks5:// listener).
	SubscriptionProxy string `json:"subscription_proxy"`
	// StartupTimeoutSeconds bounds how long a freshly started Xray process may
	// take to accept connections on its listener.
	StartupTimeoutSeconds int `json:"startup_timeout_seconds"`
}

type RetryConfig struct {
	MaxAttempts    int `json:"max_attempts"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

type ModelsConfig struct {
	RefreshSeconds int               `json:"refresh_seconds"`
	Protocols      map[string]string `json:"protocols"`
	// StripFreeSuffix hides the trailing "-free" from the model IDs this
	// gateway advertises. A client may then address a model by either the
	// stripped alias or the real ID; the upstream always receives the real ID.
	StripFreeSuffix bool `json:"strip_free_suffix"`
}

type LoggingConfig struct {
	Level    string `json:"level"`
	RingSize int    `json:"ring_size"`
	// DumpRequestBodies logs the exact bytes sent upstream (redacted, capped)
	// so retry and translation problems can be diagnosed without patching the
	// binary. Off by default: the bodies contain conversation content.
	DumpRequestBodies bool `json:"dump_request_bodies"`
}

type WebUIConfig struct {
	Enabled           bool   `json:"enabled"`
	Listen            string `json:"listen"`
	Username          string `json:"username"`
	Password          string `json:"password,omitempty"`
	PasswordHash      string `json:"password_hash,omitempty"`
	SessionTTLMinutes int    `json:"session_ttl_minutes"`
}

type PerformanceConfig struct {
	MaxIdleConns           int `json:"max_idle_conns"`
	MaxIdleConnsPerHost    int `json:"max_idle_conns_per_host"`
	MaxConnsPerHost        int `json:"max_conns_per_host"`
	IdleConnTimeoutSeconds int `json:"idle_conn_timeout_seconds"`
	ConnectTimeoutSeconds  int `json:"connect_timeout_seconds"`
	FailureCooldownSeconds int `json:"failure_cooldown_seconds"`
	AttemptTimeoutSeconds  int `json:"attempt_timeout_seconds"`
	// ConnectionRotationSeconds recycles idle upstream connections so the next
	// request dials again. A vless entry assigns a fresh exit IP to every new
	// connection, so recycling is how the outbound IP is rotated; keep-alive
	// still serves requests that arrive back to back. Zero disables rotation.
	ConnectionRotationSeconds int `json:"connection_rotation_seconds"`
}

// AttemptTimeout bounds how long a single upstream attempt may wait for
// response headers before it is abandoned and the next node is tried. Values
// <= 0 keep the historical behavior of using the request-level retry timeout,
// so existing configs are unaffected. The result never exceeds requestTimeout,
// and only the header wait is bounded: an established stream keeps flowing
// under the request-level timeout.
//
// The bound is installed on the shared transports, so it covers every attempt
// in both the anonymous and the authenticated loops. Without it, one hung exit
// can consume the entire request budget by itself, and the attempts that follow
// are fired against an already-expired context.
func (cfg PerformanceConfig) AttemptTimeout(requestTimeout time.Duration) time.Duration {
	if cfg.AttemptTimeoutSeconds > 0 {
		attempt := time.Duration(cfg.AttemptTimeoutSeconds) * time.Second
		if requestTimeout > 0 && attempt > requestTimeout {
			return requestTimeout
		}
		return attempt
	}
	return requestTimeout
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	data, err = stripJSONComments(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg := Config{
		Listen:      "127.0.0.1:8080",
		Upstream:    UpstreamConfig{Zen: "https://opencode.ai/zen", Go: "https://opencode.ai/zen/go"},
		Retry:       RetryConfig{MaxAttempts: 3, TimeoutSeconds: 300},
		Models:      ModelsConfig{RefreshSeconds: 300, Protocols: map[string]string{}},
		Performance: PerformanceConfig{MaxIdleConns: 2048, MaxIdleConnsPerHost: 256, MaxConnsPerHost: 0, IdleConnTimeoutSeconds: 120, ConnectTimeoutSeconds: 5, FailureCooldownSeconds: 15},
		Logging:     LoggingConfig{Level: "info", RingSize: 2000},
		WebUI:       WebUIConfig{Listen: "0.0.0.0:8081", SessionTTLMinutes: 720},
		Vless:       VlessConfig{Count: 24, RefreshSeconds: 300, RotateBatch: 3, PortBase: 24000, Host: "127.0.0.1", StartupTimeoutSeconds: 15},
		Prefer:      TierGo,
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := jsonutil.EnsureEOF(dec); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return Normalize(path, cfg)
}

// Normalize resolves external inputs and validates a Config supplied by
// either the JSON file or the authenticated management API.
func Normalize(path string, cfg Config) (Config, error) {
	cfg.dir = filepath.Dir(path)
	trimList(&cfg.ServerKeys)
	trimList(&cfg.ZenKeys)
	trimList(&cfg.GoKeys)
	cfg.ProxyFile = strings.TrimSpace(cfg.ProxyFile)
	if err := resolveProxyFiles(path, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Prefer != TierZen && cfg.Prefer != TierGo {
		return Config{}, errors.New("prefer must be \"zen\" or \"go\"")
	}
	if cfg.Listen == "" {
		return Config{}, errors.New("listen must not be empty")
	}
	cfg.Upstream.Zen = strings.TrimSpace(cfg.Upstream.Zen)
	cfg.Upstream.Go = strings.TrimSpace(cfg.Upstream.Go)
	for name, raw := range map[string]string{"upstream.zen": cfg.Upstream.Zen, "upstream.go": cfg.Upstream.Go} {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") {
			return Config{}, fmt.Errorf("%s must be an http or https URL", name)
		}
	}
	if len(cfg.ServerKeys) == 0 {
		return Config{}, errors.New("server_keys must contain at least one local key")
	}
	if !cfg.Anonymous && len(cfg.ZenKeys) == 0 && len(cfg.GoKeys) == 0 {
		return Config{}, errors.New("zen_keys or go_keys must contain at least one upstream key unless anonymous is enabled")
	}
	if cfg.Retry.MaxAttempts < 1 {
		return Config{}, errors.New("retry.max_attempts must be at least 1")
	}
	if cfg.Retry.TimeoutSeconds < 1 {
		return Config{}, errors.New("retry.timeout_seconds must be at least 1")
	}
	if cfg.Models.RefreshSeconds < 1 {
		return Config{}, errors.New("models.refresh_seconds must be at least 1")
	}
	if cfg.Performance.MaxIdleConns < 1 || cfg.Performance.MaxIdleConnsPerHost < 1 || cfg.Performance.MaxConnsPerHost < 0 || cfg.Performance.IdleConnTimeoutSeconds < 1 || cfg.Performance.ConnectTimeoutSeconds < 1 || cfg.Performance.FailureCooldownSeconds < 1 {
		return Config{}, errors.New("performance values must be positive (max_conns_per_host may be zero for unlimited)")
	}
	if cfg.Performance.AttemptTimeoutSeconds < 0 {
		return Config{}, errors.New("performance.attempt_timeout_seconds must not be negative (0 keeps the retry timeout)")
	}
	if cfg.Performance.ConnectionRotationSeconds < 0 {
		return Config{}, errors.New("performance.connection_rotation_seconds must not be negative (0 disables rotation)")
	}
	if cfg.Logging.Level != "debug" && cfg.Logging.Level != "info" && cfg.Logging.Level != "warn" && cfg.Logging.Level != "error" {
		return Config{}, errors.New("logging.level must be debug, info, warn, or error")
	}
	if cfg.Logging.RingSize < 100 || cfg.Logging.RingSize > 50000 {
		return Config{}, errors.New("logging.ring_size must be between 100 and 50000")
	}
	if cfg.WebUI.Password != "" && len(cfg.WebUI.Password) < 10 {
		return Config{}, errors.New("webui.password must contain at least 10 characters")
	}
	if cfg.WebUI.Enabled {
		cfg.WebUI.Listen = strings.TrimSpace(cfg.WebUI.Listen)
		cfg.WebUI.Username = strings.TrimSpace(cfg.WebUI.Username)
		if cfg.WebUI.Listen == "" {
			return Config{}, errors.New("webui.listen must not be empty when webui is enabled")
		}
		if cfg.WebUI.Username == "" {
			return Config{}, errors.New("webui.username must not be empty when webui is enabled")
		}
		if cfg.WebUI.Password == "" && cfg.WebUI.PasswordHash == "" {
			return Config{}, errors.New("webui.password is required for first-time setup")
		}
		if cfg.WebUI.SessionTTLMinutes < 5 || cfg.WebUI.SessionTTLMinutes > 10080 {
			return Config{}, errors.New("webui.session_ttl_minutes must be between 5 and 10080")
		}
	}
	for _, raw := range cfg.RuntimeProxies() {
		if raw == "direct" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return Config{}, fmt.Errorf("invalid proxy URL %q", RedactURL(raw))
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return Config{}, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
		}
	}
	for model, protocol := range cfg.Models.Protocols {
		if model == "" || !wire.Valid(wire.Protocol(protocol)) {
			return Config{}, fmt.Errorf("models.protocols contains invalid mapping %q: %q", model, protocol)
		}
	}
	if err := normalizeReasoning(&cfg); err != nil {
		return Config{}, err
	}
	if err := normalizeVless(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// normalizeVless fills defaults and validates the vless pool settings. The
// block is inert unless enabled, so an unused section never breaks a config.
func normalizeVless(cfg *Config) error {
	cfg.Vless.Subscription = strings.TrimSpace(cfg.Vless.Subscription)
	cfg.Vless.XrayPath = strings.TrimSpace(cfg.Vless.XrayPath)
	cfg.Vless.XrayVersion = strings.TrimSpace(cfg.Vless.XrayVersion)
	cfg.Vless.Host = strings.TrimSpace(cfg.Vless.Host)
	cfg.Vless.SubscriptionProxy = strings.TrimSpace(cfg.Vless.SubscriptionProxy)
	trimList(&cfg.Vless.Nodes)
	trimList(&cfg.Vless.PreferredDomains)
	trimList(&cfg.Vless.Endpoints)
	cfg.Vless.Nodes = UniqueStrings(cfg.Vless.Nodes)
	cfg.Vless.PreferredDomains = UniqueStrings(cfg.Vless.PreferredDomains)
	cfg.Vless.Endpoints = UniqueStrings(cfg.Vless.Endpoints)
	if cfg.Vless.XrayVersion != "" && !strings.HasPrefix(cfg.Vless.XrayVersion, "v") {
		cfg.Vless.XrayVersion = "v" + cfg.Vless.XrayVersion
	}
	if !cfg.Vless.Enabled {
		return nil
	}
	// A pool needs candidates from somewhere: either a subscription or at least
	// one fixed node. Accepting both lets a fixed node act as a stable fallback
	// when the subscription is unreachable.
	if cfg.Vless.Subscription == "" && len(cfg.Vless.Nodes) == 0 {
		return errors.New("vless requires either a subscription or at least one fixed node")
	}
	for _, node := range cfg.Vless.Nodes {
		if !strings.HasPrefix(strings.ToLower(node), "vless://") {
			return fmt.Errorf("vless.nodes entry %q must be a vless:// share link", node)
		}
	}
	if cfg.Vless.Subscription != "" {
		u, err := url.Parse(cfg.Vless.Subscription)
		if err != nil || u.Host == "" || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") {
			return errors.New("vless.subscription must be an http or https URL")
		}
	}
	if cfg.Vless.SubscriptionProxy != "" {
		proxyURL, err := url.Parse(cfg.Vless.SubscriptionProxy)
		if err != nil || proxyURL.Host == "" {
			return errors.New("vless.subscription_proxy must be a valid proxy URL")
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("unsupported vless.subscription_proxy scheme %q", proxyURL.Scheme)
		}
	}
	if cfg.Vless.Count < 1 || cfg.Vless.Count > 256 {
		return errors.New("vless.count must be between 1 and 256")
	}
	if cfg.Vless.RefreshSeconds < 10 {
		return errors.New("vless.refresh_seconds must be at least 10")
	}
	if cfg.Vless.RotateBatch < 1 || cfg.Vless.RotateBatch > cfg.Vless.Count {
		return errors.New("vless.rotate_batch must be between 1 and vless.count")
	}
	if cfg.Vless.PortBase < 1024 || cfg.Vless.PortBase+cfg.Vless.Count > 65535 {
		return errors.New("vless.port_base must leave room for every listener below port 65535")
	}
	if cfg.Vless.Host == "" {
		return errors.New("vless.host must not be empty when vless is enabled")
	}
	if cfg.Vless.StartupTimeoutSeconds < 1 {
		return errors.New("vless.startup_timeout_seconds must be at least 1")
	}
	return nil
}

// normalizeReasoning validates and canonicalizes the forced thinking level.
//
// Levels are stored lowercased so the hot path can compare them without
// allocating. An empty value means "not forced", which is the default and keeps
// the feature absent for existing configurations.
func normalizeReasoning(cfg *Config) error {
	cfg.Reasoning.Effort = strings.ToLower(strings.TrimSpace(cfg.Reasoning.Effort))
	if err := validateEffort("reasoning.effort", cfg.Reasoning.Effort); err != nil {
		return err
	}
	if cfg.Reasoning.EffortByModel == nil {
		return nil
	}
	// An empty map is the same as an absent one, and writing nil back keeps a
	// saved configuration free of a field the user never set.
	if len(cfg.Reasoning.EffortByModel) == 0 {
		cfg.Reasoning.EffortByModel = nil
		return nil
	}
	normalized := make(map[string]string, len(cfg.Reasoning.EffortByModel))
	for model, effort := range cfg.Reasoning.EffortByModel {
		key := strings.TrimSpace(model)
		if key == "" {
			return errors.New("reasoning.effort_by_model must not contain an empty model ID")
		}
		level := strings.ToLower(strings.TrimSpace(effort))
		if err := validateEffort(fmt.Sprintf("reasoning.effort_by_model[%q]", key), level); err != nil {
			return err
		}
		normalized[key] = level
	}
	cfg.Reasoning.EffortByModel = normalized
	return nil
}

// validateEffort accepts the levels the OpenAI and Anthropic surfaces define
// plus an empty string, which means "not forced".
func validateEffort(name, value string) error {
	switch value {
	case "", "minimal", "low", "medium", "high", "xhigh", "max", "none":
		return nil
	default:
		return fmt.Errorf("%s must be one of minimal, low, medium, high, xhigh, max, none, or empty to disable the override", name)
	}
}

// ForcedEffort returns the level the operator forced for a model, or an empty
// string when none applies. A stored level is already lowercase.
func (cfg Config) ForcedEffort(model string) string {
	if effort, ok := cfg.Reasoning.EffortByModel[model]; ok {
		return effort
	}
	return cfg.Reasoning.Effort
}

// StaticProxies returns the configured proxies, including proxyfile entries,
// without any implicit fallback. An empty result means the operator configured
// no static route, which is valid when another source (the vless pool) supplies
// the transports.
func (cfg Config) StaticProxies() []string {
	if len(cfg.effectiveProxies) > 0 {
		return cfg.effectiveProxies
	}
	if len(cfg.Proxies) > 0 {
		return cfg.Proxies
	}
	return nil
}

// RuntimeProxies returns the resolved proxy list, including proxyfile entries,
// falling back to a direct connection when no static proxy is configured.
// Config.Proxies intentionally remains the list stored in config.json so a
// save never duplicates values loaded from proxyfile.
func (cfg Config) RuntimeProxies() []string {
	if list := cfg.StaticProxies(); len(list) > 0 {
		return list
	}
	return []string{"direct"}
}

// Dir returns the directory of the configuration file the value was loaded
// from. It is empty for a Config that was never normalized, in which case the
// caller should fall back to the process working directory.
func (cfg Config) Dir() string {
	if cfg.dir != "" {
		return cfg.dir
	}
	return "."
}

// PasswordForSave ensures resolved-only data is excluded. The method is kept
// separate to make accidental persistence of effective proxy values obvious.
func (cfg *Config) PasswordForSave() {
	cfg.effectiveProxies = nil
}

func Clone(cfg Config) Config {
	cfg.ServerKeys = append([]string(nil), cfg.ServerKeys...)
	cfg.ZenKeys = append([]string(nil), cfg.ZenKeys...)
	cfg.GoKeys = append([]string(nil), cfg.GoKeys...)
	cfg.Proxies = append([]string(nil), cfg.Proxies...)
	cfg.effectiveProxies = append([]string(nil), cfg.effectiveProxies...)
	if cfg.Models.Protocols != nil {
		protocols := make(map[string]string, len(cfg.Models.Protocols))
		for key, value := range cfg.Models.Protocols {
			protocols[key] = value
		}
		cfg.Models.Protocols = protocols
	}
	if cfg.Reasoning.EffortByModel != nil {
		efforts := make(map[string]string, len(cfg.Reasoning.EffortByModel))
		for model, effort := range cfg.Reasoning.EffortByModel {
			efforts[model] = effort
		}
		cfg.Reasoning.EffortByModel = efforts
	}
	return cfg
}
