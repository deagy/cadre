package observability

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

type contextKey string

const (
	traceIDKey contextKey = "trace_id"
	spanIDKey  contextKey = "span_id"
	tracerKey  contextKey = "tracer"
)

// Tracer manages distributed tracing with OpenTelemetry-compatible spans.
type Tracer struct {
	mu          sync.RWMutex
	spans       map[string]*Span
	traceID     string
	serviceName string
	environment string
	version     string
	isEnabled   bool
}

// Span represents a distributed trace span.
type Span struct {
	SpanID        string
	TraceID       string
	ParentSpanID  string
	Name          string
	StartTime     time.Time
	EndTime       time.Time
	Duration      time.Duration
	Status        string // "ok", "error", "unset"
	Attributes    map[string]interface{}
	Events        []Event
	ErrorMessage  string
	ServiceName   string
	ResourceAttrs map[string]string
	mu            sync.RWMutex
}

// Event represents a span event.
type Event struct {
	Name       string
	Timestamp  time.Time
	Attributes map[string]interface{}
}

// NewTracer creates a new tracer.
func NewTracer(serviceName string) *Tracer {
	traceID := generateTraceID()

	return &Tracer{
		spans:       make(map[string]*Span),
		traceID:     traceID,
		serviceName: serviceName,
		environment: os.Getenv("ENVIRONMENT"),
		version:     os.Getenv("VERSION"),
		isEnabled:   os.Getenv("TRACING_ENABLED") != "false",
	}
}

// StartSpan starts a new span.
func (t *Tracer) StartSpan(ctx context.Context, name string) (*Span, context.Context) {
	if !t.isEnabled {
		return &Span{Name: name}, ctx
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	spanID := generateSpanID()
	now := time.Now()

	span := &Span{
		SpanID:        spanID,
		TraceID:       t.traceID,
		Name:          name,
		StartTime:     now,
		Status:        "unset",
		Attributes:    make(map[string]interface{}),
		Events:        []Event{},
		ServiceName:   t.serviceName,
		ResourceAttrs: map[string]string{},
	}

	t.spans[spanID] = span

	// Propagate trace context
	newCtx := context.WithValue(ctx, traceIDKey, t.traceID)
	newCtx = context.WithValue(newCtx, spanIDKey, spanID)
	newCtx = context.WithValue(newCtx, tracerKey, t)

	return span, newCtx
}

// EndSpan marks a span as complete.
func (t *Tracer) EndSpan(span *Span) {
	if !t.isEnabled || span == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	span.mu.Lock()
	defer span.mu.Unlock()

	if span.EndTime.IsZero() {
		span.EndTime = time.Now()
		span.Duration = span.EndTime.Sub(span.StartTime)
	}
}

// RecordError records an error in the current span.
func (span *Span) RecordError(err error) {
	if span == nil || err == nil {
		return
	}

	span.mu.Lock()
	defer span.mu.Unlock()

	span.ErrorMessage = err.Error()
	span.Status = "error"

	span.Events = append(span.Events, Event{
		Name:      "exception",
		Timestamp: time.Now(),
		Attributes: map[string]interface{}{
			"exception.message": err.Error(),
		},
	})
}

// AddAttribute adds an attribute to the span.
func (span *Span) AddAttribute(key string, value interface{}) {
	if span == nil {
		return
	}

	span.mu.Lock()
	defer span.mu.Unlock()

	span.Attributes[key] = value
}

// AddEvent adds an event to the span.
func (span *Span) AddEvent(name string, attrs map[string]interface{}) {
	if span == nil {
		return
	}

	span.mu.Lock()
	defer span.mu.Unlock()

	event := Event{
		Name:       name,
		Timestamp:  time.Now(),
		Attributes: attrs,
	}

	span.Events = append(span.Events, event)
}

// SetStatus sets the span status.
func (span *Span) SetStatus(status string) {
	if span == nil {
		return
	}

	span.mu.Lock()
	defer span.mu.Unlock()

	span.Status = status
}

// ExportSpan exports a span in JSON-compatible format.
func (t *Tracer) ExportSpan(spanID string) map[string]interface{} {
	t.mu.RLock()
	span, exists := t.spans[spanID]
	t.mu.RUnlock()

	if !exists || span == nil {
		return nil
	}

	span.mu.RLock()
	defer span.mu.RUnlock()

	attrs := make([]map[string]interface{}, 0)
	for k, v := range span.Attributes {
		attrs = append(attrs, map[string]interface{}{
			"key":   k,
			"value": v,
		})
	}

	events := make([]map[string]interface{}, 0)
	for _, e := range span.Events {
		events = append(events, map[string]interface{}{
			"name":       e.Name,
			"timestamp":  e.Timestamp.Format(time.RFC3339Nano),
			"attributes": e.Attributes,
		})
	}

	return map[string]interface{}{
		"trace_id":       span.TraceID,
		"span_id":        span.SpanID,
		"parent_span_id": span.ParentSpanID,
		"name":           span.Name,
		"start_time":     span.StartTime.Format(time.RFC3339Nano),
		"end_time":       span.EndTime.Format(time.RFC3339Nano),
		"duration_ms":    span.Duration.Milliseconds(),
		"status":         span.Status,
		"error_message":  span.ErrorMessage,
		"service_name":   span.ServiceName,
		"attributes":     attrs,
		"events":         events,
	}
}

// ExportAllSpans exports all spans.
func (t *Tracer) ExportAllSpans() []map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var spans []map[string]interface{}
	for spanID := range t.spans {
		if exported := t.ExportSpan(spanID); exported != nil {
			spans = append(spans, exported)
		}
	}

	return spans
}

// GetTraceID returns the current trace ID.
func (t *Tracer) GetTraceID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.traceID
}

// GetSpanCount returns the number of spans created.
func (t *Tracer) GetSpanCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.spans)
}

// Helper functions

func generateTraceID() string {
	return fmt.Sprintf("%016x%016x", time.Now().UnixNano(), uint64(os.Getpid()))
}

func generateSpanID() string {
	return fmt.Sprintf("%016x", time.Now().UnixNano())
}

// ContextExtractor extracts trace context from environment or request.
type ContextExtractor struct {
	traceID string
	spanID  string
	baggage map[string]string
}

// NewContextExtractor creates a new context extractor.
func NewContextExtractor() *ContextExtractor {
	return &ContextExtractor{
		traceID: os.Getenv("TRACE_ID"),
		spanID:  os.Getenv("SPAN_ID"),
		baggage: make(map[string]string),
	}
}

// ExtractTraceID extracts trace ID from environment.
func (ce *ContextExtractor) ExtractTraceID() string {
	if ce.traceID == "" {
		ce.traceID = generateTraceID()
	}

	return ce.traceID
}

// ExtractSpanID extracts span ID from environment.
func (ce *ContextExtractor) ExtractSpanID() string {
	if ce.spanID == "" {
		ce.spanID = generateSpanID()
	}

	return ce.spanID
}

// ExtractBaggage extracts baggage items from environment.
func (ce *ContextExtractor) ExtractBaggage() map[string]string {
	// Extract from environment variables with BAGGAGE_ prefix
	for _, env := range os.Environ() {
		if len(env) > 8 && env[:8] == "BAGGAGE_" {
			key := env[8:]
			if idx := findIndex(key, '='); idx > 0 {
				k := key[:idx]
				v := env[idx+9:]
				ce.baggage[k] = v
			}
		}
	}

	return ce.baggage
}

func findIndex(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}

	return -1
}

// TraceContext holds trace and span IDs for propagation.
type TraceContext struct {
	TraceID string
	SpanID  string
	Baggage map[string]string
}

// InjectIntoEnv injects trace context into environment variables.
func (tc *TraceContext) InjectIntoEnv(env []string) []string {
	result := env
	result = append(result, fmt.Sprintf("TRACE_ID=%s", tc.TraceID))
	result = append(result, fmt.Sprintf("SPAN_ID=%s", tc.SpanID))

	for k, v := range tc.Baggage {
		result = append(result, fmt.Sprintf("BAGGAGE_%s=%s", k, v))
	}

	return result
}
