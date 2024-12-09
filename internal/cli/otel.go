package cli

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	otellog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func setupOTelSDK(ctx context.Context, res *resource.Resource) (shutdown func(context.Context) error, err error) {
	var shutdownFuncs []func(context.Context) error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown = func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	res, err = resource.Merge(resource.Default(), res)
	if err != nil {
		handleErr(err)
		return
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// Set up trace provider.
	tracerProvider, err := newTraceProvider(ctx, res)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Set up meter provider.
	meterProvider, err := newMeterProvider(ctx, res)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider(ctx, res)
	if err != nil {
		handleErr(err)
		return
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newTraceProvider(ctx context.Context, res *resource.Resource) (*trace.TracerProvider, error) {
	traceExporter, err := autoexport.NewSpanExporter(ctx, autoexport.WithFallbackSpanExporter(func(ctx context.Context) (trace.SpanExporter, error) {
		return noopSpanExporter{}, nil
	}))
	if err != nil {
		return nil, err
	}

	traceProvider := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithBatcher(traceExporter,
			// Default is 5s. Set to 1s for demonstrative purposes.
			trace.WithBatchTimeout(time.Second)),
	)
	return traceProvider, nil
}

func newMeterProvider(ctx context.Context, res *resource.Resource) (*metric.MeterProvider, error) {
	metricReader, err := autoexport.NewMetricReader(ctx, autoexport.WithFallbackMetricReader(func(ctx context.Context) (metric.Reader, error) {
		return noopMetricReader{}, nil
	}))
	if err != nil {
		return nil, err
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metricReader),
	)
	return meterProvider, nil
}

func newLoggerProvider(ctx context.Context, res *resource.Resource) (*otellog.LoggerProvider, error) {
	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, err
	}

	loggerProvider := otellog.NewLoggerProvider(
		otellog.WithResource(res),
		otellog.WithProcessor(otellog.NewBatchProcessor(logExporter)),
	)
	return loggerProvider, nil
}

// noopSpanExporter is an implementation of trace.SpanExporter that performs no operations.
type noopSpanExporter struct{}

var _ trace.SpanExporter = noopSpanExporter{}

// ExportSpans is part of trace.SpanExporter interface.
func (e noopSpanExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	return nil
}

// Shutdown is part of trace.SpanExporter interface.
func (e noopSpanExporter) Shutdown(ctx context.Context) error {
	return nil
}

// IsNoneSpanExporter returns true for the exporter returned by [NewSpanExporter]
// when OTEL_TRACES_EXPORTER environment variable is set to "none".
func IsNoneSpanExporter(e trace.SpanExporter) bool {
	_, ok := e.(noopSpanExporter)
	return ok
}

type noopMetricReader struct {
	*metric.ManualReader
}

func newNoopMetricReader() noopMetricReader {
	return noopMetricReader{metric.NewManualReader()}
}

// IsNoneMetricReader returns true for the exporter returned by [NewMetricReader]
// when OTEL_METRICS_EXPORTER environment variable is set to "none".
func IsNoneMetricReader(e metric.Reader) bool {
	_, ok := e.(noopMetricReader)
	return ok
}

type noopMetricProducer struct{}

func (e noopMetricProducer) Produce(ctx context.Context) ([]metricdata.ScopeMetrics, error) {
	return nil, nil
}

func newNoopMetricProducer() noopMetricProducer {
	return noopMetricProducer{}
}

// noopLogExporter is an implementation of log.SpanExporter that performs no operations.
type noopLogExporter struct{}

var _ otellog.Exporter = noopLogExporter{}

// ExportSpans is part of log.Exporter interface.
func (e noopLogExporter) Export(ctx context.Context, records []otellog.Record) error {
	return nil
}

// Shutdown is part of log.Exporter interface.
func (e noopLogExporter) Shutdown(ctx context.Context) error {
	return nil
}

// ForceFlush is part of log.Exporter interface.
func (e noopLogExporter) ForceFlush(ctx context.Context) error {
	return nil
}

// IsNoneLogExporter returns true for the exporter returned by [NewLogExporter]
// when OTEL_LOGSS_EXPORTER environment variable is set to "none".
func IsNoneLogExporter(e otellog.Exporter) bool {
	_, ok := e.(noopLogExporter)
	return ok
}
