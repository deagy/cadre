package orchestration

import (
	"strings"
	"testing"
	"time"
)

func TestNewInMemoryMetricsCollector(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	if collector == nil {
		t.Errorf("collector should not be nil")
	}
}

func TestRecordOperation(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("test-op", 100*time.Millisecond, true)

	metrics := collector.GetMetrics("test-op")
	if metrics == nil {
		t.Fatalf("metrics should not be nil")
	}

	if metrics.TotalOperations != 1 {
		t.Errorf("expected 1 operation")
	}

	if metrics.SuccessfulOps != 1 {
		t.Errorf("expected 1 successful operation")
	}
}

func TestRecordMultipleOperations(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("test-op", 100*time.Millisecond, true)
	collector.RecordOperation("test-op", 200*time.Millisecond, false)
	collector.RecordOperation("test-op", 150*time.Millisecond, true)

	metrics := collector.GetMetrics("test-op")
	if metrics.TotalOperations != 3 {
		t.Errorf("expected 3 operations, got %d", metrics.TotalOperations)
	}

	if metrics.SuccessfulOps != 2 {
		t.Errorf("expected 2 successful operations, got %d", metrics.SuccessfulOps)
	}

	if metrics.FailedOps != 1 {
		t.Errorf("expected 1 failed operation, got %d", metrics.FailedOps)
	}
}

func TestMetricsMinMax(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("test-op", 100*time.Millisecond, true)
	collector.RecordOperation("test-op", 50*time.Millisecond, true)
	collector.RecordOperation("test-op", 200*time.Millisecond, true)

	metrics := collector.GetMetrics("test-op")

	if metrics.MinDuration != 50*time.Millisecond {
		t.Errorf("expected min 50ms, got %v", metrics.MinDuration)
	}

	if metrics.MaxDuration != 200*time.Millisecond {
		t.Errorf("expected max 200ms, got %v", metrics.MaxDuration)
	}
}

func TestMetricsAverageDuration(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("test-op", 100*time.Millisecond, true)
	collector.RecordOperation("test-op", 200*time.Millisecond, true)

	metrics := collector.GetMetrics("test-op")

	expected := 150 * time.Millisecond
	if metrics.AverageDuration != expected {
		t.Errorf("expected avg %v, got %v", expected, metrics.AverageDuration)
	}
}

func TestMetricsSuccessRate(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("test-op", 100*time.Millisecond, true)
	collector.RecordOperation("test-op", 100*time.Millisecond, true)
	collector.RecordOperation("test-op", 100*time.Millisecond, false)

	metrics := collector.GetMetrics("test-op")

	expectedRate := 2.0 / 3.0
	if metrics.SuccessRate != expectedRate {
		t.Errorf("expected success rate %.2f, got %.2f", expectedRate, metrics.SuccessRate)
	}
}

func TestGetSnapshot(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	for i := 0; i < 100; i++ {
		duration := time.Duration((i+1)*10) * time.Millisecond
		collector.RecordOperation("test-op", duration, true)
	}

	snapshot := collector.GetSnapshot("test-op")

	if snapshot == nil {
		t.Fatalf("snapshot should not be nil")
	}

	if snapshot.TotalOperations != 100 {
		t.Errorf("expected 100 operations")
	}

	if snapshot.P50Latency == 0 {
		t.Errorf("P50 should be non-zero")
	}

	if snapshot.P95Latency == 0 {
		t.Errorf("P95 should be non-zero")
	}

	if snapshot.P99Latency == 0 {
		t.Errorf("P99 should be non-zero")
	}

	// P50 < P95 < P99
	if snapshot.P50Latency >= snapshot.P95Latency {
		t.Errorf("P50 should be less than P95")
	}

	if snapshot.P95Latency >= snapshot.P99Latency {
		t.Errorf("P95 should be less than P99")
	}
}

func TestReset(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("test-op", 100*time.Millisecond, true)

	metrics := collector.GetMetrics("test-op")
	if metrics == nil || metrics.TotalOperations != 1 {
		t.Errorf("metrics should exist before reset")
	}

	collector.Reset("test-op")

	metrics = collector.GetMetrics("test-op")
	if metrics != nil {
		t.Errorf("metrics should be nil after reset")
	}
}

func TestPerformanceMonitorStartTimer(t *testing.T) {
	collector := NewInMemoryMetricsCollector()
	monitor := NewPerformanceMonitor(collector)

	timerID := monitor.StartTimer("test-op")

	if timerID == "" {
		t.Errorf("timer ID should not be empty")
	}
}

func TestPerformanceMonitorEndTimer(t *testing.T) {
	collector := NewInMemoryMetricsCollector()
	monitor := NewPerformanceMonitor(collector)

	timerID := monitor.StartTimer("test-op")
	time.Sleep(50 * time.Millisecond)
	duration := monitor.EndTimer(timerID, true)

	if duration < 50*time.Millisecond {
		t.Errorf("duration should be at least 50ms, got %v", duration)
	}
}

func TestPerformanceMonitorMetrics(t *testing.T) {
	collector := NewInMemoryMetricsCollector()
	monitor := NewPerformanceMonitor(collector)

	timerID1 := monitor.StartTimer("test-op")
	time.Sleep(10 * time.Millisecond)
	monitor.EndTimer(timerID1, true)

	timerID2 := monitor.StartTimer("test-op")
	time.Sleep(20 * time.Millisecond)
	monitor.EndTimer(timerID2, true)

	metrics := monitor.GetMetrics(timerID1)
	if metrics == nil {
		t.Fatalf("metrics should not be nil")
	}

	if metrics.TotalOperations < 1 {
		t.Errorf("should have recorded operations")
	}
}

func TestLatencyStats(t *testing.T) {
	samples := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}

	stats := ComputeLatencyStats(samples)

	if stats.Min != 10*time.Millisecond {
		t.Errorf("expected min 10ms, got %v", stats.Min)
	}

	if stats.Max != 50*time.Millisecond {
		t.Errorf("expected max 50ms, got %v", stats.Max)
	}

	expectedMean := 30 * time.Millisecond
	if stats.Mean != expectedMean {
		t.Errorf("expected mean %v, got %v", expectedMean, stats.Mean)
	}
}

func TestHistogramBuckets(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	for i := 1; i <= 100; i++ {
		duration := time.Duration(i) * time.Millisecond
		collector.RecordOperation("test-op", duration, true)
	}

	snapshot := collector.GetSnapshot("test-op")

	if len(snapshot.LatencyHistogram) == 0 {
		t.Errorf("histogram should have buckets")
	}

	// Buckets should be in increasing order
	for i := 0; i < len(snapshot.LatencyHistogram)-1; i++ {
		if snapshot.LatencyHistogram[i].Boundary >= snapshot.LatencyHistogram[i+1].Boundary {
			t.Errorf("buckets should be in increasing order")
		}
	}
}

func TestPercentileLatency(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	for i := 1; i <= 100; i++ {
		duration := time.Duration(i) * time.Millisecond
		collector.RecordOperation("test-op", duration, true)
	}

	snapshot := collector.GetSnapshot("test-op")

	p50 := snapshot.PercentileLatency(50)
	p95 := snapshot.PercentileLatency(95)

	if p50 == 0 {
		t.Errorf("P50 should be non-zero")
	}

	if p95 == 0 {
		t.Errorf("P95 should be non-zero")
	}

	if p50 >= p95 {
		t.Errorf("P50 should be less than P95")
	}
}

func TestGetAllMetrics(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	collector.RecordOperation("op1", 100*time.Millisecond, true)
	collector.RecordOperation("op2", 200*time.Millisecond, true)
	collector.RecordOperation("op3", 150*time.Millisecond, true)

	allMetrics := collector.GetAllMetrics()

	if len(allMetrics) != 3 {
		t.Errorf("expected 3 metrics, got %d", len(allMetrics))
	}

	if _, exists := allMetrics["op1"]; !exists {
		t.Errorf("op1 should exist")
	}

	if _, exists := allMetrics["op2"]; !exists {
		t.Errorf("op2 should exist")
	}

	if _, exists := allMetrics["op3"]; !exists {
		t.Errorf("op3 should exist")
	}
}

func TestReport(t *testing.T) {
	collector := NewInMemoryMetricsCollector()
	monitor := NewPerformanceMonitor(collector)

	monitor.RecordMetric("test-op", 100*time.Millisecond, true)
	monitor.RecordMetric("test-op", 200*time.Millisecond, true)

	report := monitor.Report()

	if report == "" {
		t.Errorf("report should not be empty")
	}

	if !strings.Contains(report, "test-op") {
		t.Errorf("report should contain operation name")
	}

	if !strings.Contains(report, "Success Rate") {
		t.Errorf("report should contain success rate")
	}
}

func TestThroughput(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	for i := 0; i < 10; i++ {
		collector.RecordOperation("test-op", 100*time.Millisecond, true)
	}

	metrics := collector.GetMetrics("test-op")

	if metrics.ThroughputPerSec == 0 {
		t.Errorf("throughput should be non-zero")
	}
}

func TestPerformanceMonitorNilCollector(t *testing.T) {
	monitor := NewPerformanceMonitor(nil)

	if monitor == nil {
		t.Errorf("monitor should not be nil")
	}

	timerID := monitor.StartTimer("test-op")
	if timerID == "" {
		t.Errorf("timer ID should not be empty")
	}

	time.Sleep(10 * time.Millisecond)
	duration := monitor.EndTimer(timerID, true)

	if duration == 0 {
		t.Errorf("duration should be non-zero")
	}
}

func TestComputeLatencyStatsEmpty(t *testing.T) {
	stats := ComputeLatencyStats([]time.Duration{})

	if stats.Min != 0 {
		t.Errorf("min should be 0 for empty samples")
	}
}

func TestRecordOperationLatencySamples(t *testing.T) {
	collector := NewInMemoryMetricsCollector()

	for i := 0; i < 50; i++ {
		collector.RecordOperation("test-op", time.Duration(i+1)*time.Millisecond, true)
	}

	metrics := collector.GetMetrics("test-op")

	if len(metrics.LatencySamples) != 50 {
		t.Errorf("expected 50 samples, got %d", len(metrics.LatencySamples))
	}
}
