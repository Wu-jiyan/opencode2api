package gateway

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/httpx"
)

const (
	// proxyProbeURL returns a tiny plain-text body, so the measurement is
	// dominated by the connection setup through the proxy.
	proxyProbeURL           = "https://cloudflare.com/cdn-cgi/trace"
	proxyProbeDefaultLimit  = 8 * time.Second
	proxyProbeConcurrency   = 12
	proxyProbeReadLimit     = 32 << 10
	proxyProbeOverallBudget = 30 * time.Second
)

// ProxyProbe is the outcome of one latency measurement for a transport.
type ProxyProbe struct {
	Index   int    `json:"index"`
	Address string `json:"address"`
	OK      bool   `json:"ok"`
	Millis  int64  `json:"millis"`
	Error   string `json:"error,omitempty"`
}

// ProbeProxies measures every transport concurrently and reports the time to
// the first response byte. It is a diagnostic only: proxy health is left
// untouched so a manual test never evicts a working route.
func (g *Gateway) ProbeProxies(ctx context.Context, timeout time.Duration) []ProxyProbe {
	items := g.transports.snapshot()
	if timeout <= 0 {
		timeout = proxyProbeDefaultLimit
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, proxyProbeOverallBudget)
		defer cancel()
	}
	results := make([]ProxyProbe, len(items))
	sem := make(chan struct{}, proxyProbeConcurrency)
	var wg sync.WaitGroup
	for i, proxy := range items {
		if proxy == nil {
			continue
		}
		wg.Add(1)
		go func(index int, target *proxyTransport) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[index] = probeTransport(ctx, index, target, timeout)
		}(i, proxy)
	}
	wg.Wait()
	return results
}

func probeTransport(ctx context.Context, index int, proxy *proxyTransport, timeout time.Duration) ProxyProbe {
	result := ProxyProbe{Index: index, Address: config.RedactURL(proxy.name)}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, proxyProbeURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", httpx.UserAgent())
	start := time.Now()
	resp, err := proxy.client.Do(req)
	result.Millis = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, proxyProbeReadLimit))
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		result.Error = "HTTP " + strconv.Itoa(resp.StatusCode)
		return result
	}
	result.OK = true
	return result
}
