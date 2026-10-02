// Package admin serves configuration, authentication, diagnostics, and the WebUI.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"opencode2api/internal/buildinfo"
	"opencode2api/internal/config"
	"opencode2api/internal/gateway"
	"opencode2api/internal/httpx"
	"opencode2api/internal/telemetry"
	"opencode2api/webui"
)

const (
	// proxyProbeTimeout bounds a single probe, while proxyProbeBudget bounds
	// the whole request so a stuck listener cannot hold the endpoint open.
	proxyProbeTimeout = 8 * time.Second
	proxyProbeBudget  = 25 * time.Second
)

type Server struct {
	manager  *gateway.RuntimeManager
	monitor  *telemetry.Monitor
	logs     *telemetry.LogHub
	logger   *slog.Logger
	mu       sync.Mutex
	sessions map[string]adminSession
	attempts map[string]loginWindow
}

func New(manager *gateway.RuntimeManager, monitor *telemetry.Monitor, logs *telemetry.LogHub, logger *slog.Logger) *Server {
	return &Server{
		manager: manager, monitor: monitor, logs: logs, logger: logger, sessions: make(map[string]adminSession),
		attempts: make(map[string]loginWindow),
	}
}

func (a *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", a.handleLogin)
	mux.Handle("GET /api/auth/session", a.authenticate(http.HandlerFunc(a.handleSession)))
	mux.Handle("POST /api/auth/logout", a.authenticate(a.csrf(http.HandlerFunc(a.handleLogout))))
	mux.Handle("GET /api/config", a.authenticate(http.HandlerFunc(a.handleGetConfig)))
	mux.Handle("PUT /api/config", a.authenticate(a.csrf(http.HandlerFunc(a.handlePutConfig))))
	mux.Handle("POST /api/config/reload", a.authenticate(a.csrf(http.HandlerFunc(a.handleReload))))
	mux.Handle("GET /api/monitor", a.authenticate(http.HandlerFunc(a.handleMonitor)))
	mux.Handle("POST /api/nodes/probe", a.authenticate(a.csrf(http.HandlerFunc(a.handleProbeNodes))))
	mux.Handle("GET /api/debug/models", a.authenticate(http.HandlerFunc(a.handleDebugModels)))
	mux.Handle("POST /api/models/refresh", a.authenticate(a.csrf(http.HandlerFunc(a.handleRefreshModels))))
	mux.Handle("GET /api/logs", a.authenticate(http.HandlerFunc(a.handleLogs)))
	mux.Handle("GET /api/logs/stream", a.authenticate(http.HandlerFunc(a.handleLogStream)))
	mux.Handle("/", a.staticHandler())
	return a.securityHeaders(telemetry.Recover(a.logger, mux))
}

func (a *Server) staticHandler() http.Handler {
	assets := webui.Assets
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAdminError(w, http.StatusNotFound, "not_found", "management endpoint not found")
			return
		}
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			if _, err := fs.Stat(assets, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		// Vite 产物文件名带内容哈希，可以长期强缓存；入口 HTML 必须每次校验。
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func (a *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Naive UI 在运行时注入组件样式，且模板使用行内 style 属性，因此
		// style-src 需要 'unsafe-inline'。脚本仍然严格限制为同源。
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self' data:")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type SecretView struct {
	ID      string `json:"id"`
	Display string `json:"display"`
}

type ConfigView struct {
	Listen      string                   `json:"listen"`
	ServerKeys  []SecretView             `json:"server_keys"`
	ZenKeys     []SecretView             `json:"zen_keys"`
	GoKeys      []SecretView             `json:"go_keys"`
	Anonymous   bool                     `json:"anonymous"`
	Proxies     []SecretView             `json:"proxies"`
	ProxyFile   string                   `json:"proxyfile"`
	Vless       config.VlessConfig       `json:"vless"`
	Upstream    config.UpstreamConfig    `json:"upstream"`
	Retry       config.RetryConfig       `json:"retry"`
	Models      config.ModelsConfig      `json:"models"`
	Performance config.PerformanceConfig `json:"performance"`
	Logging     config.LoggingConfig     `json:"logging"`
	Prefer      config.Tier              `json:"prefer"`
	Reasoning   config.ReasoningConfig   `json:"reasoning"`
	WebUI       WebUIView                `json:"webui"`
	Effective   EffectiveView            `json:"effective"`
	Restart     []string                 `json:"restart_required_fields,omitempty"`
}

type EffectiveView struct {
	Listen       string `json:"listen"`
	WebUIListen  string `json:"webui_listen"`
	WebUIEnabled bool   `json:"webui_enabled"`
}

type WebUIView struct {
	Enabled           bool   `json:"enabled"`
	Listen            string `json:"listen"`
	Username          string `json:"username"`
	SessionTTLMinutes int    `json:"session_ttl_minutes"`
}

type SecretInput struct {
	ID    string `json:"id,omitempty"`
	Value string `json:"value,omitempty"`
}

type ConfigUpdate struct {
	Listen      string                   `json:"listen"`
	ServerKeys  []SecretInput            `json:"server_keys"`
	ZenKeys     []SecretInput            `json:"zen_keys"`
	GoKeys      []SecretInput            `json:"go_keys"`
	Anonymous   bool                     `json:"anonymous"`
	Proxies     []SecretInput            `json:"proxies"`
	ProxyFile   string                   `json:"proxyfile"`
	Vless       config.VlessConfig       `json:"vless"`
	Upstream    config.UpstreamConfig    `json:"upstream"`
	Retry       config.RetryConfig       `json:"retry"`
	Models      config.ModelsConfig      `json:"models"`
	Performance config.PerformanceConfig `json:"performance"`
	Logging     config.LoggingConfig     `json:"logging"`
	Prefer      config.Tier              `json:"prefer"`
	// A pointer distinguishes an omitted field (legacy clients should keep the
	// current reasoning configuration) from an explicit empty object (clear it).
	Reasoning *config.ReasoningConfig `json:"reasoning,omitempty"`
	WebUI     WebUIView               `json:"webui"`
}

func (a *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, a.configView())
}

func (a *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var update ConfigUpdate
	if err := decodeAdminJSON(w, r, &update); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	current := a.manager.Config()
	serverKeys, err := resolveSecrets(update.ServerKeys, current.ServerKeys)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_server_keys", err.Error())
		return
	}
	zenKeys, err := resolveSecrets(update.ZenKeys, current.ZenKeys)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_zen_keys", err.Error())
		return
	}
	goKeys, err := resolveSecrets(update.GoKeys, current.GoKeys)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_go_keys", err.Error())
		return
	}
	proxies, err := resolveSecrets(update.Proxies, current.Proxies)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_proxies", err.Error())
		return
	}
	reasoning := current.Reasoning
	if update.Reasoning != nil {
		reasoning = *update.Reasoning
	}
	candidate := config.Config{
		Listen: update.Listen, ServerKeys: serverKeys, ZenKeys: zenKeys, GoKeys: goKeys, Anonymous: update.Anonymous, Proxies: proxies, ProxyFile: update.ProxyFile,
		Vless:    update.Vless,
		Upstream: update.Upstream, Retry: update.Retry, Models: update.Models, Performance: update.Performance, Logging: update.Logging, Prefer: update.Prefer,
		Reasoning: reasoning,
		WebUI:     config.WebUIConfig{Enabled: update.WebUI.Enabled, Listen: update.WebUI.Listen, Username: current.WebUI.Username, PasswordHash: current.WebUI.PasswordHash, SessionTTLMinutes: update.WebUI.SessionTTLMinutes},
	}
	result, err := a.manager.Apply(candidate, true)
	if err != nil {
		a.logger.Warn("configuration update rejected", "component", "config", "event", "config_rejected", "error", err)
		writeAdminError(w, http.StatusBadRequest, "configuration_rejected", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"result": result, "config": a.configView()})
}

func (a *Server) handleReload(w http.ResponseWriter, _ *http.Request) {
	result, err := a.manager.Reload()
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "reload_failed", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (a *Server) handleMonitor(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"version": buildinfo.Version, "metrics": a.monitor.Snapshot(), "resources": a.manager.Resources(),
	})
}

// handleProbeNodes measures the latency of every configured proxy on demand.
// The probe never mutates proxy health, so it cannot disturb live traffic.
func (a *Server) handleProbeNodes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), proxyProbeBudget)
	defer cancel()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"probes": a.manager.ProbeProxies(ctx, proxyProbeTimeout)})
}

func (a *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, gap := a.logs.Recent(after, limit)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": events, "gap": gap})
}

func (a *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAdminError(w, http.StatusInternalServerError, "stream_unsupported", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	if queryAfter, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64); err == nil && queryAfter > after {
		after = queryAfter
	}
	recent, gap := a.logs.Recent(after, 2000)
	if gap {
		_ = httpx.WriteSSE(w, "gap", 0, map[string]any{"message": "older log events have expired"})
	}
	for _, event := range recent {
		if err := httpx.WriteSSE(w, "log", event.Sequence, event); err != nil {
			return
		}
		after = event.Sequence
	}
	flusher.Flush()
	stream, unsubscribe := a.logs.Subscribe()
	defer unsubscribe()
	cookie, _ := r.Cookie(adminCookieName)
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-stream:
			if after > 0 && event.Sequence > after+1 {
				_ = httpx.WriteSSE(w, "gap", 0, map[string]any{"message": "slow client missed log events", "after": after, "next": event.Sequence})
			}
			if err := httpx.WriteSSE(w, "log", event.Sequence, event); err != nil {
				return
			}
			after = event.Sequence
			flusher.Flush()
		case <-keepAlive.C:
			if cookie == nil || !a.sessionTokenValid(cookie.Value) {
				return
			}
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (a *Server) sessionTokenValid(token string) bool {
	a.mu.Lock()
	session, ok := a.sessions[tokenDigest(token)]
	if ok && time.Now().After(session.Expires) {
		delete(a.sessions, tokenDigest(token))
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return false
	}
	cfg := a.manager.Config()
	return session.Username == cfg.WebUI.Username && session.AuthVersion == config.Fingerprint(cfg.WebUI.PasswordHash)
}

func (a *Server) configView() ConfigView {
	cfg := a.manager.Config()
	effective, restart := a.manager.RestartStatus()
	return ConfigView{
		Listen: cfg.Listen, ServerKeys: maskSecrets(cfg.ServerKeys, false), ZenKeys: maskSecrets(cfg.ZenKeys, false), GoKeys: maskSecrets(cfg.GoKeys, false), Anonymous: cfg.Anonymous,
		Proxies: maskSecrets(cfg.Proxies, true), ProxyFile: cfg.ProxyFile, Vless: cfg.Vless, Upstream: cfg.Upstream, Retry: cfg.Retry, Models: cfg.Models,
		Performance: cfg.Performance, Logging: cfg.Logging, Prefer: cfg.Prefer, Reasoning: cfg.Reasoning,
		WebUI:     WebUIView{Enabled: cfg.WebUI.Enabled, Listen: cfg.WebUI.Listen, Username: cfg.WebUI.Username, SessionTTLMinutes: cfg.WebUI.SessionTTLMinutes},
		Effective: EffectiveView{Listen: effective.API, WebUIListen: effective.WebUI, WebUIEnabled: effective.WebUIEnabled},
		Restart:   restart,
	}
}

func maskSecrets(values []string, proxy bool) []SecretView {
	result := make([]SecretView, 0, len(values))
	for _, value := range values {
		display := config.MaskValue(value)
		if proxy {
			display = config.RedactURL(value)
		}
		result = append(result, SecretView{ID: config.Fingerprint(value), Display: display})
	}
	return result
}

func resolveSecrets(inputs []SecretInput, existing []string) ([]string, error) {
	known := make(map[string]string, len(existing))
	for _, value := range existing {
		known[config.Fingerprint(value)] = value
	}
	result := make([]string, 0, len(inputs))
	for _, input := range inputs {
		switch {
		case strings.TrimSpace(input.Value) != "":
			result = append(result, strings.TrimSpace(input.Value))
		case input.ID != "":
			value, ok := known[input.ID]
			if !ok {
				return nil, fmt.Errorf("unknown or stale secret id %q", input.ID)
			}
			result = append(result, value)
		default:
			return nil, errors.New("each item must contain id or value")
		}
	}
	return result, nil
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeAdminError(w http.ResponseWriter, status int, code, message string) {
	httpx.WriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
