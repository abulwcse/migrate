package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/source"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const (
	defaultTimeFormat = "20060102150405"
	defaultTimezone   = "UTC"
	createUsage       = `create [-ext E] [-dir D] [-seq] [-digits N] [-format] [-tz] NAME
	   Create a set of timestamped up/down migrations titled NAME, in directory D with extension E.
	   Use -seq option to generate sequential up/down migrations with N digits.
	   Use -format option to specify a Go time format string. Note: migrations with the same time cause "duplicate migration version" error.
           Use -tz option to specify the timezone that will be used when generating non-sequential migrations (defaults: UTC).
`
	gotoUsage = `goto V       Migrate to version V`
	upUsage   = `up [N]       Apply all or N up migrations`
	downUsage = `down [N] [-all]    Apply all or N down migrations
	Use -all to apply all down migrations`
	dropUsage = `drop [-f]    Drop everything inside database
	Use -f to bypass confirmation`
	forceUsage = `force V      Set version V but don't run migration (ignores dirty state)`
)

const name = "github.com/golang-migrate/migrate/v4"

var (
	tracer = otel.Tracer(name)
	// meter  = otel.Meter(name)
	// logger = otellog.NewLogger(name)
)

func handleSubCmdHelp(help bool, usage string, flagSet *flag.FlagSet) {
	if help {
		fmt.Fprintln(os.Stderr, usage)
		flagSet.PrintDefaults()
		os.Exit(0)
	}
}

func newFlagSetWithHelp(name string) (*flag.FlagSet, *bool) {
	flagSet := flag.NewFlagSet(name, flag.ExitOnError)
	helpPtr := flagSet.Bool("help", false, "Print help information")
	return flagSet, helpPtr
}

// set main log
var log = &Log{}

func printUsageAndExit() int {
	flag.Usage()

	// If a command is not found we exit with a status 2 to match the behavior
	// of flag.Parse() with flag.ExitOnError when parsing an invalid flag.
	return 2
}

// Main function of a cli application. It is public for backwards compatibility with `cli` package
func Main(version string) int {
	helpPtr := flag.Bool("help", false, "")
	versionPtr := flag.Bool("version", false, "")
	verbosePtr := flag.Bool("verbose", false, "")
	prefetchPtr := flag.Uint("prefetch", 10, "")
	lockTimeoutPtr := flag.Uint("lock-timeout", 15, "")
	pathPtr := flag.String("path", "", "")
	databasePtr := flag.String("database", "", "")
	sourcePtr := flag.String("source", "", "")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			`Usage: migrate OPTIONS COMMAND [arg...]
       migrate [ -version | -help ]

Options:
  -source          Location of the migrations (driver://url)
  -path            Shorthand for -source=file://path
  -database        Run migrations against this database (driver://url)
  -prefetch N      Number of migrations to load in advance before executing (default 10)
  -lock-timeout N  Allow N seconds to acquire database lock (default 15)
  -verbose         Print verbose logging
  -version         Print version
  -help            Print usage

Commands:
  %s
  %s
  %s
  %s
  %s
  %s
  version      Print current migration version

Source drivers: `+strings.Join(source.List(), ", ")+`
Database drivers: `+strings.Join(database.List(), ", ")+"\n", createUsage, gotoUsage, upUsage, downUsage, dropUsage, forceUsage)
	}

	flag.Parse()

	// initialize logger
	log.verbose = *verbosePtr

	// show cli version
	if *versionPtr {
		fmt.Fprintln(os.Stderr, version)
		return 0
	}

	// show help
	if *helpPtr {
		flag.Usage()
		return 0
	}

	// translate -path into -source if given
	if *sourcePtr == "" && *pathPtr != "" {
		*sourcePtr = fmt.Sprintf("file://%v", *pathPtr)
	}

	ctx := context.Background()

	// Set up OpenTelemetry.
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("migrate"),
			semconv.ServiceVersionKey.String(version),
		),
		resource.WithFromEnv(),      // Discover and provide attributes from OTEL_RESOURCE_ATTRIBUTES and OTEL_SERVICE_NAME environment variables.
		resource.WithTelemetrySDK(), // Discover and provide information about the OpenTelemetry SDK used.
		resource.WithProcess(),      // Discover and provide process information.
		resource.WithOS(),           // Discover and provide OS information.
		resource.WithContainer(),    // Discover and provide container information.
		resource.WithHost(),         // Discover and provide host information.
	)
	if errors.Is(err, resource.ErrPartialResource) || errors.Is(err, resource.ErrSchemaURLConflict) {
		log.Println(err)
	} else if err != nil {
		return log.fatalErr(err)
	}

	otelShutdown, err := setupOTelSDK(ctx, res)
	if err != nil {
		return 1
	}
	// Handle shutdown properly so nothing leaks.
	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
	}()

	ctx, span := tracer.Start(ctx, "Main")
	defer span.End()

	// initialize migrate
	// don't catch migraterErr here and let each command decide
	// how it wants to handle the error
	migrater, migraterErr := migrate.NewInstrumented(ctx, tracer, *sourcePtr, *databasePtr)

	defer func() {
		if migraterErr == nil {
			if _, err := migrater.Close(ctx); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				log.Println(err)
			}
		}
	}()
	if migraterErr == nil {
		migrater.Log = log
		migrater.PrefetchMigrations = *prefetchPtr
		migrater.LockTimeout = time.Duration(int64(*lockTimeoutPtr)) * time.Second

		// handle Ctrl+c
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT)
		go func() {
			for range signals {
				log.Println("Stopping after this running migration ...")
				migrater.GracefulStop <- true
				return
			}
		}()
	}

	startTime := time.Now()

	if len(flag.Args()) < 1 {
		return printUsageAndExit()
	}
	args := flag.Args()[1:]

	switch flag.Arg(0) {
	case "create":

		seq := false
		seqDigits := 6

		createFlagSet, help := newFlagSetWithHelp("create")
		extPtr := createFlagSet.String("ext", "", "File extension")
		dirPtr := createFlagSet.String("dir", "", "Directory to place file in (default: current working directory)")
		formatPtr := createFlagSet.String("format", defaultTimeFormat, `The Go time format string to use. If the string "unix" or "unixNano" is specified, then the seconds or nanoseconds since January 1, 1970 UTC respectively will be used. Caution, due to the behavior of time.Time.Format(), invalid format strings will not error`)
		timezoneName := createFlagSet.String("tz", defaultTimezone, `The timezone that will be used for generating timestamps (default: utc)`)
		createFlagSet.BoolVar(&seq, "seq", seq, "Use sequential numbers instead of timestamps (default: false)")
		createFlagSet.IntVar(&seqDigits, "digits", seqDigits, "The number of digits to use in sequences (default: 6)")

		if err := createFlagSet.Parse(args); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		handleSubCmdHelp(*help, createUsage, createFlagSet)

		if createFlagSet.NArg() == 0 {
			span.SetStatus(codes.Error, "please specify name")
			return log.fatal("error: please specify name")
		}
		name := createFlagSet.Arg(0)

		if *extPtr == "" {
			span.SetStatus(codes.Error, "-ext flag must be specified")
			return log.fatal("error: -ext flag must be specified")
		}

		timezone, err := time.LoadLocation(*timezoneName)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatal(err)
		}

		if err := createCmd(ctx, *dirPtr, startTime.In(timezone), *formatPtr, name, *extPtr, seq, seqDigits, true); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

	case "goto":

		gotoSet, helpPtr := newFlagSetWithHelp("goto")

		if err := gotoSet.Parse(args); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		handleSubCmdHelp(*helpPtr, gotoUsage, gotoSet)

		if migraterErr != nil {
			span.RecordError(migraterErr)
			span.SetStatus(codes.Error, migraterErr.Error())
			return log.fatalErr(migraterErr)
		}

		if gotoSet.NArg() == 0 {
			span.SetStatus(codes.Error, "please specify version argument V")
			return log.fatal("error: please specify version argument V")
		}

		v, err := strconv.ParseUint(gotoSet.Arg(0), 10, 64)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatal("error: can't read version argument V")
		}

		if err := gotoCmd(ctx, migrater, uint(v)); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		if log.verbose {
			log.Println("Finished after", time.Since(startTime))
		}

	case "up":
		upSet, helpPtr := newFlagSetWithHelp("up")

		if err := upSet.Parse(args); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		handleSubCmdHelp(*helpPtr, upUsage, upSet)

		if migraterErr != nil {
			span.RecordError(migraterErr)
			span.SetStatus(codes.Error, migraterErr.Error())
			return log.fatalErr(migraterErr)
		}

		limit := -1
		if upSet.NArg() > 0 {
			n, err := strconv.ParseUint(upSet.Arg(0), 10, 64)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return log.fatal("error: can't read limit argument N")
			}
			limit = int(n)
		}

		if err := upCmd(ctx, migrater, limit); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		if log.verbose {
			log.Println("Finished after", time.Since(startTime))
		}

	case "down":
		downFlagSet, helpPtr := newFlagSetWithHelp("down")
		applyAll := downFlagSet.Bool("all", false, "Apply all down migrations")

		if err := downFlagSet.Parse(args); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		handleSubCmdHelp(*helpPtr, downUsage, downFlagSet)

		if migraterErr != nil {
			span.RecordError(migraterErr)
			span.SetStatus(codes.Error, migraterErr.Error())
			return log.fatalErr(migraterErr)
		}

		downArgs := downFlagSet.Args()
		num, needsConfirm, err := numDownMigrationsFromArgs(*applyAll, downArgs)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}
		if needsConfirm {
			log.Println("Are you sure you want to apply all down migrations? [y/N]")
			var response string
			_, _ = fmt.Scanln(&response)
			response = strings.ToLower(strings.TrimSpace(response))

			if response == "y" {
				log.Println("Applying all down migrations")
			} else {
				return log.fatal("Not applying all down migrations")
			}
		}

		if err := downCmd(ctx, migrater, num); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		if log.verbose {
			log.Println("Finished after", time.Since(startTime))
		}

	case "drop":
		dropFlagSet, help := newFlagSetWithHelp("drop")
		forceDrop := dropFlagSet.Bool("f", false, "Force the drop command by bypassing the confirmation prompt")

		if err := dropFlagSet.Parse(args); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		handleSubCmdHelp(*help, dropUsage, dropFlagSet)

		if !*forceDrop {
			log.Println("Are you sure you want to drop the entire database schema? [y/N]")
			var response string
			_, _ = fmt.Scanln(&response)
			response = strings.ToLower(strings.TrimSpace(response))

			if response == "y" {
				log.Println("Dropping the entire database schema")
			} else {
				return log.fatal("Aborted dropping the entire database schema")
			}
		}

		if migraterErr != nil {
			span.RecordError(migraterErr)
			span.SetStatus(codes.Error, migraterErr.Error())
			return log.fatalErr(migraterErr)
		}

		if err := dropCmd(ctx, migrater); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		if log.verbose {
			log.Println("Finished after", time.Since(startTime))
		}

	case "force":
		forceSet, helpPtr := newFlagSetWithHelp("force")

		if err := forceSet.Parse(args); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		handleSubCmdHelp(*helpPtr, forceUsage, forceSet)

		if migraterErr != nil {
			span.RecordError(migraterErr)
			span.SetStatus(codes.Error, migraterErr.Error())
			return log.fatalErr(migraterErr)
		}

		if forceSet.NArg() == 0 {
			span.SetStatus(codes.Error, "please specify version argument V")
			return log.fatal("error: please specify version argument V")
		}

		v, err := strconv.ParseInt(forceSet.Arg(0), 10, 64)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatal("error: can't read version argument V")
		}

		if v < -1 {
			span.SetStatus(codes.Error, "argument V must be >= -1")
			return log.fatal("error: argument V must be >= -1")
		}

		if err := forceCmd(ctx, migrater, int(v)); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

		if log.verbose {
			log.Println("Finished after", time.Since(startTime))
		}

	case "version":
		if migraterErr != nil {
			span.RecordError(migraterErr)
			span.SetStatus(codes.Error, migraterErr.Error())
			return log.fatalErr(migraterErr)
		}

		if err := versionCmd(ctx, migrater); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return log.fatalErr(err)
		}

	default:
		return printUsageAndExit()
	}

	return 0
}
