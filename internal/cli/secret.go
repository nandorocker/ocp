package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/nando/ocp/internal/ocp"
)

// secret is the runtime helper that harness configuration calls back into, so
// secrets are read when needed instead of being written into native files.
// It reads no OCP state or configuration and prints only its result.
//
//	ocp secret print '<template>'
//	ocp secret exec NAME=<template>... -- <command> [args...]
//	ocp secret headers NAME=<template>...   (prints a JSON object)
func (r *Runner) secret(args []string) error {
	if len(args) == 0 {
		return usageError(errors.New("secret requires print or exec"))
	}
	switch args[0] {
	case "print":
		if len(args) != 2 {
			return usageError(errors.New("secret print requires exactly one template"))
		}
		value, err := ocp.ExpandSubstitutions(args[1])
		if err != nil {
			return err
		}
		fmt.Fprint(r.Out, value)
		return nil
	case "headers":
		if len(args) < 2 {
			return usageError(errors.New("secret headers requires NAME=template..."))
		}
		headers := map[string]string{}
		for _, pair := range args[1:] {
			name, template, ok := strings.Cut(pair, "=")
			if !ok || name == "" {
				return usageError(fmt.Errorf("secret headers: %q is not NAME=template", pair))
			}
			value, err := ocp.ExpandSubstitutions(template)
			if err != nil {
				return fmt.Errorf("secret headers %s: %w", name, err)
			}
			headers[name] = value
		}
		return json.NewEncoder(r.Out).Encode(headers)
	case "exec":
		sep := -1
		for i, a := range args {
			if a == "--" {
				sep = i
				break
			}
		}
		if sep < 0 || sep == len(args)-1 {
			return usageError(errors.New("secret exec requires NAME=template... -- command"))
		}
		env := os.Environ()
		for _, pair := range args[1:sep] {
			name, template, ok := strings.Cut(pair, "=")
			if !ok || name == "" {
				return usageError(fmt.Errorf("secret exec: %q is not NAME=template", pair))
			}
			value, err := ocp.ExpandSubstitutions(template)
			if err != nil {
				return fmt.Errorf("secret exec %s: %w", name, err)
			}
			env = setEnv(env, name, value)
		}
		command := args[sep+1:]
		return r.execReplace(command, env)
	}
	return usageError(fmt.Errorf("unknown secret mode %q", args[0]))
}

// execReplace replaces this process with the command, so signals and stdio
// go straight to it. The command is looked up on the child's PATH.
func (r *Runner) execReplace(command, env []string) error {
	if r.ExecReplace != nil {
		return r.ExecReplace(command, env)
	}
	path := command[0]
	if !strings.Contains(path, "/") {
		for _, kv := range env {
			if dirs, ok := strings.CutPrefix(kv, "PATH="); ok {
				os.Setenv("PATH", dirs)
			}
		}
		found, err := exec.LookPath(path)
		if err != nil {
			return err
		}
		path = found
	}
	return syscall.Exec(path, command, env)
}
