package observability

import (
	"context"
	"testing"
	"time"
)

func TestTracerCreation(t *testing.T) {
	tracer := NewTracer("test-service")

	if tracer == nil {
		t.Errorf("Tracer should be created")
	}

	if tracer.GetTraceID() == "" {
		t.Errorf("Trace ID should be generated")
	}
}

func TestStartSpan(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, newCtx := tracer.StartSpan(ctx, "test-span")

	if span == nil {
		t.Errorf("Span should be created")
	}

	if span.Name != "test-span" {
		t.Errorf("Span name should match")
	}

	if newCtx == nil {
		t.Errorf("Context should be returned")
	}

	if tracer.GetSpanCount() != 1 {
		t.Errorf("Span count should be 1")
	}
}

func TestEndSpan(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "test-span")

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	tracer.EndSpan(span)

	if span.Duration == 0 {
		t.Errorf("Span duration should be recorded")
	}

	if span.EndTime.IsZero() {
		t.Errorf("Span end time should be set")
	}
}

func TestRecordError(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "test-span")

	err := &testError{"test error"}
	span.RecordError(err)

	if span.Status != "error" {
		t.Errorf("Span status should be 'error'")
	}

	if span.ErrorMessage == "" {
		t.Errorf("Error message should be recorded")
	}
}

func TestAddAttribute(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "test-span")

	span.AddAttribute("key1", "value1")
	span.AddAttribute("key2", 42)

	if span.Attributes["key1"] != "value1" {
		t.Errorf("Attribute should be recorded")
	}

	if span.Attributes["key2"] != 42 {
		t.Errorf("Numeric attribute should be recorded")
	}
}

func TestAddEvent(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "test-span")

	attrs := map[string]interface{}{"event_key": "event_value"}
	span.AddEvent("test-event", attrs)

	if len(span.Events) != 1 {
		t.Errorf("Event should be added")
	}

	if span.Events[0].Name != "test-event" {
		t.Errorf("Event name should match")
	}
}

func TestSetStatus(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "test-span")

	span.SetStatus("ok")

	if span.Status != "ok" {
		t.Errorf("Span status should be updated")
	}
}

func TestExportSpan(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span, _ := tracer.StartSpan(ctx, "test-span")
	span.AddAttribute("test", "value")
	tracer.EndSpan(span)

	exported := tracer.ExportSpan(span.SpanID)

	if exported == nil {
		t.Errorf("Span should be exported")
	}

	if exported["name"] != "test-span" {
		t.Errorf("Exported span name should match")
	}

	if exported["trace_id"] == "" {
		t.Errorf("Trace ID should be in export")
	}
}

func TestExportAllSpans(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	span1, _ := tracer.StartSpan(ctx, "span1")
	span2, _ := tracer.StartSpan(ctx, "span2")

	tracer.EndSpan(span1)
	tracer.EndSpan(span2)

	exported := tracer.ExportAllSpans()

	if len(exported) != 2 {
		t.Errorf("Should export 2 spans, got %d", len(exported))
	}
}

func TestContextExtractor(t *testing.T) {
	ce := NewContextExtractor()

	traceID := ce.ExtractTraceID()
	if traceID == "" {
		t.Errorf("Trace ID should be extracted or generated")
	}

	spanID := ce.ExtractSpanID()
	if spanID == "" {
		t.Errorf("Span ID should be extracted or generated")
	}
}

func TestTraceContextInject(t *testing.T) {
	tc := &TraceContext{
		TraceID: "trace-123",
		SpanID:  "span-456",
		Baggage: map[string]string{"key": "value"},
	}

	env := []string{"PATH=/usr/bin"}
	injected := tc.InjectIntoEnv(env)

	if len(injected) < 3 {
		t.Errorf("Should inject at least 3 items")
	}

	found := false
	for _, item := range injected {
		if item == "TRACE_ID=trace-123" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("TRACE_ID should be injected")
	}
}

func TestSpanCount(t *testing.T) {
	tracer := NewTracer("test-service")
	ctx := context.Background()

	if tracer.GetSpanCount() != 0 {
		t.Errorf("Initial span count should be 0")
	}

	tracer.StartSpan(ctx, "span1")
	if tracer.GetSpanCount() != 1 {
		t.Errorf("Span count should be 1")
	}

	tracer.StartSpan(ctx, "span2")
	if tracer.GetSpanCount() != 2 {
		t.Errorf("Span count should be 2")
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
