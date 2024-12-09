package database

import (
	"context"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var (
	DatabaseDriverKey  = attribute.Key("migrate.database.driver")
	DatabaseVersionKey = attribute.Key("migrate.database.version")
	DatabaseDirtyKey   = attribute.Key("migrate.database.dirty")
)

type ContextKey struct{}

var ContextSpanAttributes ContextKey

type instrumentedDriver struct {
	delegate Driver
	name     string
	tracer   trace.Tracer
}

func NewInstrumentedDriver(driver Driver, name string, tracer trace.Tracer) Driver {
	return instrumentedDriver{
		delegate: driver,
		name:     name,
		tracer:   tracer,
	}
}

// Close implements Driver.
func (d instrumentedDriver) Close(ctx context.Context) error {
	spanCtx, span := d.tracer.Start(ctx, "database.Close", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	err := d.delegate.Close(spanCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// Drop implements Driver.
func (d instrumentedDriver) Drop(ctx context.Context) error {
	spanCtx, span := d.tracer.Start(ctx, "database.Drop", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	err := d.delegate.Drop(spanCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// Lock implements Driver.
func (d instrumentedDriver) Lock(ctx context.Context) error {
	spanCtx, span := d.tracer.Start(ctx, "database.Lock", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	err := d.delegate.Lock(spanCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// Open implements Driver.
func (d instrumentedDriver) Open(ctx context.Context, url string) (Driver, error) {
	spanCtx, span := d.tracer.Start(ctx, "database.Open", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	driver, err := d.delegate.Open(spanCtx, url)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return NewInstrumentedDriver(driver, d.name, d.tracer), err
}

// Run implements Driver.
func (d instrumentedDriver) Run(ctx context.Context, migration io.Reader) error {
	spanCtx, span := d.tracer.Start(ctx, "database.Run", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	attributes := ctx.Value(ContextSpanAttributes)
	if attributes != nil {
		spanAttributes := attributes.([]attribute.KeyValue)
		span.SetAttributes(spanAttributes...)
	}

	err := d.delegate.Run(spanCtx, migration)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// SetVersion implements Driver.
func (d instrumentedDriver) SetVersion(ctx context.Context, version int, dirty bool) error {
	spanCtx, span := d.tracer.Start(ctx, "database.SetVersion", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
		DatabaseVersionKey.Int64(int64(version)),
		DatabaseDirtyKey.Bool(dirty),
	))
	defer span.End()

	attributes := ctx.Value(ContextSpanAttributes)
	if attributes != nil {
		spanAttributes := attributes.([]attribute.KeyValue)
		span.SetAttributes(spanAttributes...)
	}

	err := d.delegate.SetVersion(spanCtx, version, dirty)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// Unlock implements Driver.
func (d instrumentedDriver) Unlock(ctx context.Context) error {
	spanCtx, span := d.tracer.Start(ctx, "database.Unlock", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	err := d.delegate.Unlock(spanCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// Version implements Driver.
func (d instrumentedDriver) Version(ctx context.Context) (version int, dirty bool, err error) {
	spanCtx, span := d.tracer.Start(ctx, "database.Version", trace.WithAttributes(
		DatabaseDriverKey.String(d.name),
	))
	defer span.End()

	version, dirty, err = d.delegate.Version(spanCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return version, dirty, err
}
