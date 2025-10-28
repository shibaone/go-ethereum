package tracing

import (
	"go.opentelemetry.io/otel/trace"
)

// TracerAdapter is an interface for adapting different tracing providers
type TracerAdapter interface {
	StartSpan(name string) (trace.Span, error)
	EndSpan(span trace.Span)
}

// DefaultTracer is a no-op implementation of TracerAdapter
type DefaultTracer struct{}

func (t *DefaultTracer) StartSpan(name string) (trace.Span, error) {
	return nil, nil
}

func (t *DefaultTracer) EndSpan(span trace.Span) {
	// No-op
}

var defaultTracer = &DefaultTracer{}

// GetTracer returns the current tracer
func GetTracer() TracerAdapter {
	return defaultTracer
}

// SetTracer sets the global tracer
func SetTracer(t TracerAdapter) {
	defaultTracer = t.(*DefaultTracer)
}