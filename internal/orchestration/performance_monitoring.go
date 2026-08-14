package orchestration

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// PerformanceMetrics tracks performance metrics for orchestration operations.
type PerformanceMetrics struct {
	OperationName    string
	TotalOperations  int64
	SuccessfulOps    int64
	FailedOps        int64
	TotalDuration    time.Duration
	MinDuration      time.Duration
	MaxDuration      time.Duration
	LatencySamples   []time.Duration
	LastUpdatedAt    time.Time
	StartTime        time.Time
	SuccessRate      float64
	AverageDuration  time.Duration
	ThroughputPerSec float64
}

// HistogramBucket represents a latency histogram bucket.
type HistogramBucket struct {
	Boundary time.Duration
	Count    int64
}

// MetricsSnapshot represents a point-in-time snapshot of metrics.
type MetricsSnapshot struct {
	Timestamp        time.Time
	OperationName    string
	TotalOperations  int64
	SuccessfulOps    int64
	FailedOps        int64
	AverageDuration  time.Duration
	P50Latency       time.Duration
	P95Latency       time.Duration
	P99Latency       time.Duration
	ThroughputPerSec float64
	SuccessRate      float64
	LatencyHistogram []*HistogramBucket
}

// MetricsCollector defines the interface for collecting metrics.
type MetricsCollector interface {
	RecordOperation(name string, duration time.Duration, success bool)
	GetMetrics(name string) *PerformanceMetrics
	GetSnapshot(name string) *MetricsSnapshot
	Reset(name string)
	GetAllMetrics() map[string]*PerformanceMetrics
}

// InMemoryMetricsCollector collects metrics in memory.
type InMemoryMetricsCollector struct {
	mu      sync.RWMutex
	metrics map[string]*PerformanceMetrics
}

// NewInMemoryMetricsCollector creates a new in-memory metrics collector.
func NewInMemoryMetricsCollector() *InMemoryMetricsCollector {
	return &InMemoryMetricsCollector{
		metrics: make(map[string]*PerformanceMetrics),
	}
}

// RecordOperation records a single operation's metrics.
func (imc *InMemoryMetricsCollector) RecordOperation(name string, duration time.Duration, success bool) {
	imc.mu.Lock()
	defer imc.mu.Unlock()

	if _, exists := imc.metrics[name]; !exists {
		imc.metrics[name] = &PerformanceMetrics{
			OperationName:  name,
			StartTime:      time.Now(),
			LatencySamples: make([]time.Duration, 0),
		}
	}

	metrics := imc.metrics[name]
	metrics.TotalOperations++
	metrics.TotalDuration += duration
	metrics.LastUpdatedAt = time.Now()

	if success {
		metrics.SuccessfulOps++
	} else {
		metrics.FailedOps++
	}

	// Track min/max
	if metrics.MinDuration == 0 || duration < metrics.MinDuration {
		metrics.MinDuration = duration
	}
	if duration > metrics.MaxDuration {
		metrics.MaxDuration = duration
	}

	// Keep last 1000 samples for percentile calculation
	if len(metrics.LatencySamples) < 1000 {
		metrics.LatencySamples = append(metrics.LatencySamples, duration)
	} else {
		// Circular buffer - overwrite oldest
		metrics.LatencySamples = append(metrics.LatencySamples[1:], duration)
	}

	// Update derived metrics
	metrics.updateDerivedMetrics()
}

// GetMetrics returns the metrics for a given operation.
func (imc *InMemoryMetricsCollector) GetMetrics(name string) *PerformanceMetrics {
	imc.mu.RLock()
	defer imc.mu.RUnlock()

	metrics, exists := imc.metrics[name]
	if !exists {
		return nil
	}

	return metrics
}

// GetSnapshot returns a snapshot of metrics.
func (imc *InMemoryMetricsCollector) GetSnapshot(name string) *MetricsSnapshot {
	imc.mu.RLock()
	defer imc.mu.RUnlock()

	metrics, exists := imc.metrics[name]
	if !exists {
		return nil
	}

	snapshot := &MetricsSnapshot{
		Timestamp:        time.Now(),
		OperationName:    metrics.OperationName,
		TotalOperations:  metrics.TotalOperations,
		SuccessfulOps:    metrics.SuccessfulOps,
		FailedOps:        metrics.FailedOps,
		AverageDuration:  metrics.AverageDuration,
		ThroughputPerSec: metrics.ThroughputPerSec,
		SuccessRate:      metrics.SuccessRate,
	}

	// Calculate percentiles
	if len(metrics.LatencySamples) > 0 {
		sorted := make([]time.Duration, len(metrics.LatencySamples))
		copy(sorted, metrics.LatencySamples)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i] < sorted[j]
		})

		snapshot.P50Latency = sorted[len(sorted)/2]
		snapshot.P95Latency = sorted[len(sorted)*95/100]
		snapshot.P99Latency = sorted[len(sorted)*99/100]

		snapshot.LatencyHistogram = buildHistogram(sorted)
	}

	return snapshot
}

// Reset resets metrics for an operation.
func (imc *InMemoryMetricsCollector) Reset(name string) {
	imc.mu.Lock()
	defer imc.mu.Unlock()

	delete(imc.metrics, name)
}

// GetAllMetrics returns all metrics.
func (imc *InMemoryMetricsCollector) GetAllMetrics() map[string]*PerformanceMetrics {
	imc.mu.RLock()
	defer imc.mu.RUnlock()

	result := make(map[string]*PerformanceMetrics, len(imc.metrics))
	for name, metrics := range imc.metrics {
		result[name] = metrics
	}

	return result
}

// updateDerivedMetrics calculates derived metrics.
func (pm *PerformanceMetrics) updateDerivedMetrics() {
	if pm.TotalOperations > 0 {
		pm.SuccessRate = float64(pm.SuccessfulOps) / float64(pm.TotalOperations)
		pm.AverageDuration = pm.TotalDuration / time.Duration(pm.TotalOperations)

		// Calculate throughput
		elapsed := time.Since(pm.StartTime)
		if elapsed > 0 {
			pm.ThroughputPerSec = float64(pm.TotalOperations) / elapsed.Seconds()
		}
	}
}

// PerformanceMonitor monitors performance of orchestration operations.
type PerformanceMonitor struct {
	mu        sync.RWMutex
	collector MetricsCollector
	timers    map[string]time.Time
}

// NewPerformanceMonitor creates a new performance monitor.
func NewPerformanceMonitor(collector MetricsCollector) *PerformanceMonitor {
	if collector == nil {
		collector = NewInMemoryMetricsCollector()
	}

	return &PerformanceMonitor{
		collector: collector,
		timers:    make(map[string]time.Time),
	}
}

// StartTimer starts a timer for an operation.
func (pm *PerformanceMonitor) StartTimer(name string) string {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	timerID := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	pm.timers[timerID] = time.Now()

	return timerID
}

// EndTimer ends a timer and records the duration.
func (pm *PerformanceMonitor) EndTimer(timerID string, success bool) time.Duration {
	pm.mu.Lock()
	startTime, exists := pm.timers[timerID]
	if !exists {
		pm.mu.Unlock()
		return 0
	}
	delete(pm.timers, timerID)
	pm.mu.Unlock()

	duration := time.Since(startTime)
	pm.collector.RecordOperation(timerID, duration, success)

	return duration
}

// RecordMetric records a metric directly.
func (pm *PerformanceMonitor) RecordMetric(name string, duration time.Duration, success bool) {
	pm.collector.RecordOperation(name, duration, success)
}

// GetMetrics returns metrics for an operation.
func (pm *PerformanceMonitor) GetMetrics(name string) *PerformanceMetrics {
	return pm.collector.GetMetrics(name)
}

// GetSnapshot returns a snapshot of metrics.
func (pm *PerformanceMonitor) GetSnapshot(name string) *MetricsSnapshot {
	return pm.collector.GetSnapshot(name)
}

// Report returns a human-readable performance report.
func (pm *PerformanceMonitor) Report() string {
	allMetrics := pm.collector.GetAllMetrics()

	report := "Performance Report\n"
	report += "==================\n\n"

	for name, metrics := range allMetrics {
		report += fmt.Sprintf("Operation: %s\n", name)
		report += fmt.Sprintf("  Total: %d | Success: %d | Failed: %d\n",
			metrics.TotalOperations, metrics.SuccessfulOps, metrics.FailedOps)
		report += fmt.Sprintf("  Avg Duration: %v\n", metrics.AverageDuration)
		report += fmt.Sprintf("  Min/Max: %v / %v\n", metrics.MinDuration, metrics.MaxDuration)
		report += fmt.Sprintf("  Success Rate: %.1f%%\n", metrics.SuccessRate*100)
		report += fmt.Sprintf("  Throughput: %.2f ops/sec\n", metrics.ThroughputPerSec)
		report += "\n"
	}

	return report
}

// PercentileLatency returns the latency at a given percentile.
func (snapshot *MetricsSnapshot) PercentileLatency(percentile int) time.Duration {
	if snapshot == nil || len(snapshot.LatencyHistogram) == 0 {
		return 0
	}

	// Find the bucket that corresponds to this percentile
	threshold := snapshot.TotalOperations * int64(percentile) / 100
	for _, bucket := range snapshot.LatencyHistogram {
		if bucket.Count >= threshold {
			return bucket.Boundary
		}
	}

	return snapshot.LatencyHistogram[len(snapshot.LatencyHistogram)-1].Boundary
}

// Helper functions

func buildHistogram(sorted []time.Duration) []*HistogramBucket {
	if len(sorted) == 0 {
		return nil
	}

	// Create 10 buckets
	buckets := make([]*HistogramBucket, 10)
	min := sorted[0]
	max := sorted[len(sorted)-1]
	step := (max - min) / 10

	for i := 0; i < 10; i++ {
		boundary := min + step*time.Duration(i+1)
		count := 0
		for _, duration := range sorted {
			if duration <= boundary {
				count++
			}
		}
		buckets[i] = &HistogramBucket{
			Boundary: boundary,
			Count:    int64(count),
		}
	}

	return buckets
}

// LatencyStats provides latency statistics.
type LatencyStats struct {
	Min    time.Duration
	Max    time.Duration
	Mean   time.Duration
	Median time.Duration
	P95    time.Duration
	P99    time.Duration
}

// ComputeLatencyStats computes latency statistics from samples.
func ComputeLatencyStats(samples []time.Duration) *LatencyStats {
	if len(samples) == 0 {
		return &LatencyStats{}
	}

	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	var sum time.Duration
	for _, s := range sorted {
		sum += s
	}

	return &LatencyStats{
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
		Mean:   sum / time.Duration(len(sorted)),
		Median: sorted[len(sorted)/2],
		P95:    sorted[len(sorted)*95/100],
		P99:    sorted[len(sorted)*99/100],
	}
}
