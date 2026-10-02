package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

// metricBucket is the per-minute aggregate the console still reads: request
// counts and a latency histogram. Token, endpoint, tier and per-key detail was
// removed together with the usage/diagnostics pages that consumed it, because
// it was pure per-request CPU on the forwarding hot path.
type metricBucket struct {
	minute    int64
	total     uint64
	success   uint64
	errors    uint64
	duration  uint64
	histogram [11]uint64
}

type Monitor struct {
	started       time.Time
	active        atomic.Int64
	activeStreams atomic.Int64
	total         atomic.Uint64
	success       atomic.Uint64
	errors        atomic.Uint64
	mu            sync.Mutex
	buckets       [60]metricBucket
}

func NewMonitor() *Monitor {
	return &Monitor{started: time.Now().UTC()}
}

// BeginStream records a stream entering its response body phase.
func (m *Monitor) BeginStream() { m.activeStreams.Add(1) }

// EndStream releases a stream recorded by BeginStream.
func (m *Monitor) EndStream() { m.activeStreams.Add(-1) }

var latencyBounds = [...]uint64{50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000}

func (m *Monitor) Record(status int, duration time.Duration, meta *RequestMeta) {
	success := requestSucceeded(status, meta)
	m.total.Add(1)
	if success {
		m.success.Add(1)
	} else {
		m.errors.Add(1)
	}
	minute := time.Now().Unix() / 60
	milliseconds := uint64(max(duration.Milliseconds(), 0))
	index := len(latencyBounds)
	for i, bound := range latencyBounds {
		if milliseconds <= bound {
			index = i
			break
		}
	}
	m.mu.Lock()
	bucket := &m.buckets[minute%60]
	if bucket.minute != minute {
		*bucket = metricBucket{minute: minute}
	}
	bucket.total++
	if success {
		bucket.success++
	} else {
		bucket.errors++
	}
	bucket.duration += milliseconds
	bucket.histogram[index]++
	m.mu.Unlock()
}

// A stream can fail after HTTP headers have been sent. Preserve its HTTP
// status while using the terminal result for request success and error counts.
func requestSucceeded(status int, meta *RequestMeta) bool {
	return status >= 200 && status < 400 && (meta == nil || meta.Outcome == "")
}

func requestOutcome(status int, channel string) string {
	if channel == "" || channel == "not_routed" {
		return "not_routed"
	}
	if status >= 200 && status < 400 {
		return "success"
	}
	if status >= 400 && status < 500 {
		return "client_error"
	}
	if status >= 500 {
		return "server_error"
	}
	return "unknown"
}

type MonitorSnapshot struct {
	StartedAt     time.Time     `json:"started_at"`
	UptimeSeconds int64         `json:"uptime_seconds"`
	Active        int64         `json:"active_requests"`
	ActiveStreams int64         `json:"active_streams"`
	Lifetime      MetricSummary `json:"lifetime"`
	Window        MetricSummary `json:"last_hour"`
}

type MetricSummary struct {
	Total       uint64  `json:"total"`
	Success     uint64  `json:"success"`
	Errors      uint64  `json:"errors"`
	SuccessRate float64 `json:"success_rate"`
	AverageMS   float64 `json:"average_ms,omitempty"`
	P50MS       uint64  `json:"p50_ms,omitempty"`
	P95MS       uint64  `json:"p95_ms,omitempty"`
	P99MS       uint64  `json:"p99_ms,omitempty"`
}

func (m *Monitor) Snapshot() MonitorSnapshot {
	nowMinute := time.Now().Unix() / 60
	window := MetricSummary{}
	var histogram [11]uint64
	m.mu.Lock()
	for offset := int64(59); offset >= 0; offset-- {
		bucket := &m.buckets[(nowMinute-offset)%60]
		if bucket.minute != nowMinute-offset {
			continue
		}
		window.Total += bucket.total
		window.Success += bucket.success
		window.Errors += bucket.errors
		window.AverageMS += float64(bucket.duration)
		for i := range histogram {
			histogram[i] += bucket.histogram[i]
		}
	}
	m.mu.Unlock()
	if window.Total > 0 {
		window.SuccessRate = float64(window.Success) / float64(window.Total)
		window.AverageMS /= float64(window.Total)
		window.P50MS = histogramPercentile(histogram, window.Total, 0.50)
		window.P95MS = histogramPercentile(histogram, window.Total, 0.95)
		window.P99MS = histogramPercentile(histogram, window.Total, 0.99)
	}
	lifetime := MetricSummary{Total: m.total.Load(), Success: m.success.Load(), Errors: m.errors.Load()}
	if lifetime.Total > 0 {
		lifetime.SuccessRate = float64(lifetime.Success) / float64(lifetime.Total)
	}
	return MonitorSnapshot{
		StartedAt: m.started, UptimeSeconds: int64(time.Since(m.started).Seconds()), Active: m.active.Load(),
		ActiveStreams: m.activeStreams.Load(), Lifetime: lifetime, Window: window,
	}
}

func histogramPercentile(histogram [11]uint64, total uint64, percentile float64) uint64 {
	target := uint64(float64(total)*percentile + 0.999)
	var count uint64
	for i, value := range histogram {
		count += value
		if count >= target {
			if i < len(latencyBounds) {
				return latencyBounds[i]
			}
			return latencyBounds[len(latencyBounds)-1] + 1
		}
	}
	return 0
}
