package main

import (
	"os"

	"github.com/golang-migrate/migrate/v4/internal/cli"
)

// Deprecated, please use cmd/migrate
func main() {
	exitCode := cli.Main(Version)
	os.Exit(exitCode)
}
