package source

import (
	"context"
	"errors"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var (
	SourceDriverKey  = attribute.Key("migrate.source.driver")
	SourceVersionKey = attribute.Key("migrate.source.version")
)

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
	spanCtx, span := d.tracer.Start(ctx, "source.Close", trace.WithAttributes(
		SourceDriverKey.String(d.name),
	))
	defer span.End()

	err := d.delegate.Close(spanCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
}

// First implements Driver.
func (d instrumentedDriver) First(ctx context.Context) (version uint, err error) {
	spanCtx, span := d.tracer.Start(ctx, "source.First", trace.WithAttributes(
		SourceDriverKey.String(d.name),
	))
	defer span.End()

	version, err = d.delegate.First(spanCtx)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return version, err
}

// Next implements Driver.
func (d instrumentedDriver) Next(ctx context.Context, version uint) (nextVersion uint, err error) {
	spanCtx, span := d.tracer.Start(ctx, "source.Next", trace.WithAttributes(
		SourceDriverKey.String(d.name),
		SourceVersionKey.Int64(int64(version)),
	))
	defer span.End()

	nextVersion, err = d.delegate.Next(spanCtx, version)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return nextVersion, err
}

// Open implements Driver.
func (d instrumentedDriver) Open(ctx context.Context, url string) (Driver, error) {
	spanCtx, span := d.tracer.Start(ctx, "source.Open", trace.WithAttributes(
		SourceDriverKey.String(d.name),
	))
	defer span.End()

	driver, err := d.delegate.Open(spanCtx, url)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return NewInstrumentedDriver(driver, d.name, d.tracer), err
}

// Prev implements Driver.
func (d instrumentedDriver) Prev(ctx context.Context, version uint) (prevVersion uint, err error) {
	spanCtx, span := d.tracer.Start(ctx, "source.Prev", trace.WithAttributes(
		SourceDriverKey.String(d.name),
		SourceVersionKey.Int64(int64(version)),
	))
	defer span.End()

	prevVersion, err = d.delegate.Prev(spanCtx, version)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return prevVersion, err
}

// ReadDown implements Driver.
func (d instrumentedDriver) ReadDown(ctx context.Context, version uint) (r io.ReadCloser, identifier string, err error) {
	spanCtx, span := d.tracer.Start(ctx, "source.ReadDown", trace.WithAttributes(
		SourceDriverKey.String(d.name),
		SourceVersionKey.Int64(int64(version)),
	))
	defer span.End()

	r, identifier, err = d.delegate.ReadDown(spanCtx, version)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return r, identifier, err
}

// ReadUp implements Driver.
func (d instrumentedDriver) ReadUp(ctx context.Context, version uint) (r io.ReadCloser, identifier string, err error) {
	spanCtx, span := d.tracer.Start(ctx, "source.ReadUp", trace.WithAttributes(
		SourceDriverKey.String(d.name),
		SourceVersionKey.Int64(int64(version)),
	))
	defer span.End()

	r, identifier, err = d.delegate.ReadUp(spanCtx, version)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return r, identifier, err
}
