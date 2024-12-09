package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/stub" // TODO remove again
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var (
	errInvalidSequenceWidth     = errors.New("Digits must be positive")
	errIncompatibleSeqAndFormat = errors.New("The seq and format options are mutually exclusive")
	errInvalidTimeFormat        = errors.New("Time format may not be empty")
)

func nextSeqVersion(matches []string, seqDigits int) (string, error) {
	if seqDigits <= 0 {
		return "", errInvalidSequenceWidth
	}

	nextSeq := uint64(1)

	if len(matches) > 0 {
		filename := matches[len(matches)-1]
		matchSeqStr := filepath.Base(filename)
		idx := strings.Index(matchSeqStr, "_")

		if idx < 1 { // Using 1 instead of 0 since there should be at least 1 digit
			return "", fmt.Errorf("Malformed migration filename: %s", filename)
		}

		var err error
		matchSeqStr = matchSeqStr[0:idx]
		nextSeq, err = strconv.ParseUint(matchSeqStr, 10, 64)

		if err != nil {
			return "", err
		}

		nextSeq++
	}

	version := fmt.Sprintf("%0[2]*[1]d", nextSeq, seqDigits)

	if len(version) > seqDigits {
		return "", fmt.Errorf("Next sequence number %s too large. At most %d digits are allowed", version, seqDigits)
	}

	return version, nil
}

func timeVersion(startTime time.Time, format string) (version string, err error) {
	switch format {
	case "":
		err = errInvalidTimeFormat
	case "unix":
		version = strconv.FormatInt(startTime.Unix(), 10)
	case "unixNano":
		version = strconv.FormatInt(startTime.UnixNano(), 10)
	default:
		version = startTime.Format(format)
	}

	return
}

// createCmd (meant to be called via a CLI command) creates a new migration
func createCmd(ctx context.Context, dir string, startTime time.Time, format string, name string, ext string, seq bool, seqDigits int, print bool) error {
	_, span := tracer.Start(ctx, "createCmd")
	defer span.End()

	if seq && format != defaultTimeFormat {
		span.RecordError(errIncompatibleSeqAndFormat)
		span.SetStatus(codes.Error, errIncompatibleSeqAndFormat.Error())
		return errIncompatibleSeqAndFormat
	}

	var version string
	var err error

	dir = filepath.Clean(dir)
	ext = "." + strings.TrimPrefix(ext, ".")

	if seq {
		matches, err := filepath.Glob(filepath.Join(dir, "*"+ext))

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}

		version, err = nextSeqVersion(matches, seqDigits)

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	} else {
		version, err = timeVersion(startTime, format)

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}

	versionGlob := filepath.Join(dir, version+"_*"+ext)
	matches, err := filepath.Glob(versionGlob)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	if len(matches) > 0 {
		err := fmt.Errorf("duplicate migration version: %s", version)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	if err = os.MkdirAll(dir, os.ModePerm); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	for _, direction := range []string{"up", "down"} {
		basename := fmt.Sprintf("%s_%s.%s%s", version, name, direction, ext)
		filename := filepath.Join(dir, basename)

		if err = createFile(filename); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}

		if print {
			absPath, _ := filepath.Abs(filename)
			log.Println(absPath)
		}
	}

	return nil
}

func createFile(filename string) error {
	// create exclusive (fails if file already exists)
	// os.Create() specifies 0666 as the FileMode, so we're doing the same
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)

	if err != nil {
		return err
	}

	return f.Close()
}

func gotoCmd(ctx context.Context, m *migrate.Migrate, v uint) error {
	ctx, span := tracer.Start(ctx, "gotoCmd", trace.WithAttributes(attribute.Int("v", int(v))))
	defer span.End()

	if err := m.Migrate(ctx, v); err != nil {
		if err != migrate.ErrNoChange {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		log.Println(err)
	}
	return nil
}

func upCmd(ctx context.Context, m *migrate.Migrate, limit int) error {
	ctx, span := tracer.Start(ctx, "upCmd", trace.WithAttributes(attribute.Int("limit", limit)))
	defer span.End()

	if limit >= 0 {
		if err := m.Steps(ctx, limit); err != nil {
			if err != migrate.ErrNoChange {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			log.Println(err)
		}
	} else {
		if err := m.Up(ctx); err != nil {
			if err != migrate.ErrNoChange {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			log.Println(err)
		}
	}
	return nil
}

func downCmd(ctx context.Context, m *migrate.Migrate, limit int) error {
	ctx, span := tracer.Start(ctx, "downCmd", trace.WithAttributes(attribute.Int("limit", limit)))
	defer span.End()

	if limit >= 0 {
		if err := m.Steps(ctx, -limit); err != nil {
			if err != migrate.ErrNoChange {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			log.Println(err)
		}
	} else {
		if err := m.Down(ctx); err != nil {
			if err != migrate.ErrNoChange {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return err
			}
			log.Println(err)
		}
	}
	return nil
}

func dropCmd(ctx context.Context, m *migrate.Migrate) error {
	ctx, span := tracer.Start(ctx, "dropCmd")
	defer span.End()

	if err := m.Drop(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func forceCmd(ctx context.Context, m *migrate.Migrate, v int) error {
	ctx, span := tracer.Start(ctx, "forceCmd", trace.WithAttributes(attribute.Int("v", int(v))))
	defer span.End()

	if err := m.Force(ctx, v); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func versionCmd(ctx context.Context, m *migrate.Migrate) error {
	ctx, span := tracer.Start(ctx, "versionCmd")
	defer span.End()

	v, dirty, err := m.Version(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if dirty {
		log.Printf("%v (dirty)\n", v)
		span.SetAttributes(attribute.Int("v", int(v)))
		span.SetAttributes(attribute.Bool("dirty", true))
	} else {
		log.Println(v)
		span.SetAttributes(attribute.Int("v", int(v)))
	}
	return nil
}

// numDownMigrationsFromArgs returns an int for number of migrations to apply
// and a bool indicating if we need a confirm before applying
func numDownMigrationsFromArgs(applyAll bool, args []string) (int, bool, error) {
	if applyAll {
		if len(args) > 0 {
			return 0, false, errors.New("-all cannot be used with other arguments")
		}
		return -1, false, nil
	}

	switch len(args) {
	case 0:
		return -1, true, nil
	case 1:
		downValue := args[0]
		n, err := strconv.ParseUint(downValue, 10, 64)
		if err != nil {
			return 0, false, errors.New("can't read limit argument N")
		}
		return int(n), false, nil
	default:
		return 0, false, errors.New("too many arguments")
	}
}
