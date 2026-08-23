package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nando/ocp/internal/cli"
)

func main() {
	err := (&cli.Runner{}).Run(os.Args[1:])
	if err == nil {
		return
	}
	var exit *cli.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.Code)
	}
	fmt.Fprintln(os.Stderr, "ocp:", err)
	os.Exit(1)
}
