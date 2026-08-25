package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type command struct {
	name, usage, summary, description string
	examples                          []string
	configure                         func(*flag.FlagSet)
}

type setupOptions struct {
	noAuto, force, migrate bool
	repo, source           string
}

func configureSetup(f *flag.FlagSet, o *setupOptions) {
	f.BoolVar(&o.noAuto, "no-auto-commit", false, "disable automatic commits")
	f.BoolVar(&o.force, "force", false, "overwrite generated drift")
	f.BoolVar(&o.migrate, "migrate-profiles", false, "move inline profiles into profiles/*.yaml")
	f.StringVar(&o.repo, "repo", "", "repository URL for non-interactive mode")
	f.StringVar(&o.source, "source", "", "source directory for non-interactive mode")
}

type forceOptions struct{ force bool }

func configureForce(f *flag.FlagSet, o *forceOptions, description string) {
	f.BoolVar(&o.force, "force", false, description)
}

type importOptions struct {
	source string
	force  bool
}

func configureImport(f *flag.FlagSet, o *importOptions, source string) {
	f.StringVar(&o.source, "source", source, "source directory")
	f.BoolVar(&o.force, "force", false, "overwrite target files")
}

func commands() []command {
	return []command{
		{"setup", "setup [options]", "Initialize OCP from a local source or repository.", "Set up OCP.", []string{"ocp setup", "ocp setup --source ./ocp-config"}, func(f *flag.FlagSet) { configureSetup(f, &setupOptions{}) }},
		{"sync", "sync [options]", "Synchronize configuration and apply profiles.", "Sync the configured source repository and render profiles.", []string{"ocp sync", "ocp sync --force"}, func(f *flag.FlagSet) { configureForce(f, &forceOptions{}, "overwrite generated drift") }},
		{"apply", "apply [options]", "Render the current configuration.", "Apply the configured source without synchronizing it.", []string{"ocp apply", "ocp apply --force"}, func(f *flag.FlagSet) { configureForce(f, &forceOptions{}, "overwrite generated drift") }},
		{"use", "use <profile>", "Activate a rendered profile.", "Set the active OpenCode profile.", []string{"ocp use default"}, nil},
		{"run", "run <profile> [arguments]", "Run OpenCode with a profile.", "Run OpenCode with the selected profile without changing the active profile.", []string{"ocp run default --prompt hello"}, nil},
		{"list", "list", "List rendered profiles.", "List rendered profiles and mark the active profile.", []string{"ocp list"}, nil},
		{"status", "status", "Show OCP status.", "Show the configured source and active profile.", []string{"ocp status"}, nil},
		{"import", "import [options] [path]", "Import an OpenCode configuration.", "Import an OpenCode configuration into an OCP source.", []string{"ocp import ~/.config/opencode", "ocp import --source ./ocp-config"}, func(f *flag.FlagSet) { configureImport(f, &importOptions{}, "") }},
		{"upgrade", "upgrade [arguments]", "Upgrade OCP components.", "Upgrade is deferred and not implemented in this MVP.", []string{"ocp upgrade"}, nil},
		{"reset", "reset [options]", "Detach OCP from OpenCode.", "Remove OCP-managed OpenCode configuration.", []string{"ocp reset", "ocp reset --force"}, func(f *flag.FlagSet) { configureForce(f, &forceOptions{}, "replace user-owned OpenCode config") }},
	}
}

func commandByName(name string) *command {
	for _, c := range commands() {
		if c.name == name {
			return &c
		}
	}
	return nil
}

func (r *Runner) help() {
	fmt.Fprint(r.Out, `OCP manages declarative OpenCode configuration.

Usage: ocp [global options] <command> [arguments]

Getting Started:
  ocp setup    Initialize your OCP configuration.

Commands:
`)
	for _, c := range commands() {
		fmt.Fprintf(r.Out, "  %-8s %s\n", c.name, c.summary)
	}
	fmt.Fprint(r.Out, `
Global Options:
  --help       Show help.
  --version    Show the version.
  --no-color   Disable color output.

Examples:
  ocp setup
  ocp sync

Documentation: https://github.com/nando/ocp
`)
}

func (r *Runner) commandHelp(c *command) {
	fmt.Fprintf(r.Out, "Usage: ocp %s\n\n%s\n", c.usage, c.description)
	if c.configure != nil {
		f := flag.NewFlagSet(c.name, flag.ContinueOnError)
		f.SetOutput(io.Discard)
		c.configure(f)
		fmt.Fprintln(r.Out, "\nOptions:")
		f.VisitAll(func(fl *flag.Flag) {
			name := "--" + fl.Name
			if _, ok := fl.Value.(interface{ IsBoolFlag() bool }); !ok {
				name += " value"
			}
			fmt.Fprintf(r.Out, "  %-24s %s\n", name, fl.Usage)
		})
	}
	if len(c.examples) != 0 {
		fmt.Fprintln(r.Out, "\nExamples:")
		fmt.Fprintln(r.Out, "  "+strings.Join(c.examples, "\n  "))
	}
}
