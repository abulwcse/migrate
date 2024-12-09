package main

import (
	"os"

	"github.com/golang-migrate/migrate/v4/internal/cli"
)

func main() {
	exitCode := cli.Main(Version)
	os.Exit(exitCode)
}
