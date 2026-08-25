package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nando/ocp/internal/cli"
)

var version = "dev"

func main() {
	err := (&cli.Runner{Version: version}).Run(os.Args[1:])
	if err == nil {
		return
	}
	var exit *cli.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.Code)
	}
	fmt.Fprintln(os.Stderr, "ocp:", err)
	var usage *cli.UsageError
	if errors.As(err, &usage) {
		os.Exit(2)
	}
	os.Exit(1)
}
