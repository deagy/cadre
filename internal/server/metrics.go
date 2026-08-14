package server

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MetricsCollector collects and exports Prometheus-style metrics.
type MetricsCollector struct {
	mu                  sync.RWMutex
	counters            map[string]float64            // name -> value
	gauges              map[string]float64            // name -> value
	histograms          map[string]*Histogram         // name -> histogram
	counterLabels       map[string]map[string]float64 // name:label -> value
	gaugeLabels         map[string]map[string]float64 // name:label -> value
	histogramLabels     map[string]map[string]*Histogram
	startTime           time.Time
	requestCount        int64
	errorCount          int64
	totalRequestLatency int64 // nanoseconds
}

// Histogram tracks distribution of values (latencies, sizes, etc.).
type Histogram struct {
	mu      sync.Mutex
	buckets map[int64]int64 // boundary -> count
	sum     float64
	count   int64
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		counters:        make(map[string]float64),
		gauges:          make(map[string]float64),
		histograms:      make(map[string]*Histogram),
		counterLabels:   make(map[string]map[string]float64),
		gaugeLabels:     make(map[string]map[string]float64),
		histogramLabels: make(map[string]map[string]*Histogram),
		startTime:       time.Now(),
	}
}

// Record records a metric value with optional labels.
func (mc *MetricsCollector) Record(name string, value float64, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := mc.buildKey(name, labels)

	// Infer type from name suffix and record
	var metricType string
	switch {
	case strings.HasSuffix(name, "_total"):
		metricType = "counter"
	case strings.HasSuffix(name, "_ms"), strings.HasSuffix(name, "_duration"):
		metricType = "histogram"
	default:
		metricType = "gauge"
	}

	switch metricType {
	case "counter":
		if _, exists := mc.counterLabels[key]; !exists {
			mc.counterLabels[key] = make(map[string]float64)
		}
		current := mc.counterLabels[key]
		labelKey := mc.labelKey(labels)
		current[labelKey] += value
		mc.requestCount++
	case "histogram":
		if _, exists := mc.histogramLabels[key]; !exists {
			mc.histogramLabels[key] = make(map[string]*Histogram)
		}
		histograms := mc.histogramLabels[key]
		labelKey := mc.labelKey(labels)
		if _, exists := histograms[labelKey]; !exists {
			histograms[labelKey] = NewHistogram()
		}
		histograms[labelKey].Observe(value)
		mc.totalRequestLatency += int64(value * 1e6) // convert to nanoseconds
	case "gauge":
		if _, exists := mc.gaugeLabels[key]; !exists {
			mc.gaugeLabels[key] = make(map[string]float64)
		}
		current := mc.gaugeLabels[key]
		labelKey := mc.labelKey(labels)
		current[labelKey] = value
	}
}

// RecordError records an error occurrence.
func (mc *MetricsCollector) RecordError(errorType string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.errorCount++
	key := fmt.Sprintf("errors_total{type=\"%s\"}", errorType)
	mc.counters[key]++
}

// Export exports metrics in Prometheus text format.
func (mc *MetricsCollector) Export() string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var b strings.Builder

	// Write timestamp
	fmt.Fprintf(&b, "# HELP process_uptime_seconds Time since process start\n")
	fmt.Fprintf(&b, "# TYPE process_uptime_seconds gauge\n")
	uptime := time.Since(mc.startTime).Seconds()
	fmt.Fprintf(&b, "process_uptime_seconds %f\n\n", uptime)

	// Export counters
	if len(mc.counterLabels) > 0 {
		b.WriteString("# HELP http_requests_total Total HTTP requests\n")
		b.WriteString("# TYPE http_requests_total counter\n")
		for key, labelMap := range mc.counterLabels {
			for labelKey, value := range labelMap {
				fmt.Fprintf(&b, "%s{%s} %f\n", key, labelKey, value)
			}
		}
		b.WriteString("\n")
	}

	// Export gauges
	if len(mc.gaugeLabels) > 0 {
		b.WriteString("# HELP gauges Gauge metrics\n")
		b.WriteString("# TYPE gauges gauge\n")
		for key, labelMap := range mc.gaugeLabels {
			for labelKey, value := range labelMap {
				fmt.Fprintf(&b, "%s{%s} %f\n", key, labelKey, value)
			}
		}
		b.WriteString("\n")
	}

	// Export histograms
	if len(mc.histogramLabels) > 0 {
		b.WriteString("# HELP http_request_duration_ms HTTP request duration\n")
		b.WriteString("# TYPE http_request_duration_ms histogram\n")
		for key, histMap := range mc.histogramLabels {
			for labelKey, hist := range histMap {
				hist.mu.Lock()
				// Buckets
				buckets := []int64{10, 50, 100, 500, 1000, 5000}
				for _, bucket := range buckets {
					count := int64(0)
					for b, c := range hist.buckets {
						if b <= bucket {
							count += c
						}
					}
					fmt.Fprintf(&b, "%s_bucket{%s,le=\"%d\"} %d\n", key, labelKey, bucket, count)
				}
				// +Inf bucket
				fmt.Fprintf(&b, "%s_bucket{%s,le=\"+Inf\"} %d\n", key, labelKey, hist.count)
				// Sum and count
				fmt.Fprintf(&b, "%s_sum{%s} %f\n", key, labelKey, hist.sum)
				fmt.Fprintf(&b, "%s_count{%s} %d\n", key, labelKey, hist.count)
				hist.mu.Unlock()
			}
		}
		b.WriteString("\n")
	}

	// Summary
	b.WriteString("# Summary\n")
	fmt.Fprintf(&b, "# Total Requests: %d\n", mc.requestCount)
	fmt.Fprintf(&b, "# Total Errors: %d\n", mc.errorCount)
	if mc.requestCount > 0 {
		avgLatency := mc.totalRequestLatency / mc.requestCount
		fmt.Fprintf(&b, "# Average Latency: %d ns\n", avgLatency)
	}

	return b.String()
}

// NewHistogram creates a new histogram.
func NewHistogram() *Histogram {
	return &Histogram{
		buckets: make(map[int64]int64),
		sum:     0,
		count:   0,
	}
}

// Observe records a value in the histogram.
func (h *Histogram) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sum += value
	h.count++

	// Find the bucket
	buckets := []int64{10, 50, 100, 500, 1000, 5000, 10000}
	for _, bucket := range buckets {
		if int64(value) <= bucket {
			h.buckets[bucket]++
			break
		}
	}
}

// Percentile calculates a percentile value.
func (h *Histogram) Percentile(p float64) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.count == 0 {
		return 0
	}

	threshold := int64(float64(h.count) * p / 100)
	var sum int64

	// Sort buckets
	var buckets []int64
	for b := range h.buckets {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i] < buckets[j]
	})

	for _, bucket := range buckets {
		sum += h.buckets[bucket]
		if sum >= threshold {
			return float64(bucket)
		}
	}

	return float64(buckets[len(buckets)-1])
}

// Helper functions

func (mc *MetricsCollector) buildKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	var parts []string
	for k := range labels {
		parts = append(parts, k)
	}
	sort.Strings(parts)

	return name
}

func (mc *MetricsCollector) labelKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	var parts []string
	for k := range labels {
		parts = append(parts, k)
	}
	sort.Strings(parts)

	var b strings.Builder
	for i, k := range parts {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%s=\"%s\"", k, labels[k])
	}

	return b.String()
}
