package cli

import (
	"errors"
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
		{"run", "run <profile> [opencode arguments...]", "Run OpenCode with a profile.", "Run OpenCode with the selected profile without changing the active profile.", []string{"ocp run default --prompt hello"}, nil},
		{"list", "list", "List rendered profiles.", "List rendered profiles and mark the active profile.", []string{"ocp list"}, nil},
		{"status", "status", "Show OCP status.", "Show the configured source and active profile.", []string{"ocp status"}, nil},
		{"ui", "ui [options]", "Manage profiles in a local web interface.", "Start the local OCP profile and agent editor.", []string{"ocp ui", "ocp ui --no-open", "ocp ui --trusted-origin https://host.example:4100"}, func(f *flag.FlagSet) {
			f.Bool("no-open", false, "do not open the default browser")
			f.String("trusted-origin", "", "exact HTTPS origin trusted to access the UI")
		}},
		{"version", "version", "Show the OCP version.", "Print the version embedded when OCP was built.", []string{"ocp version", "ocp --version"}, nil},
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
	fmt.Fprint(r.Out, `OCP manages reproducible OpenCode profiles.

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

Documentation: https://github.com/nandorocker/ocp
`)
}

func (r *Runner) commandHelp(c *command) {
	fmt.Fprintf(r.Out, "Usage: ocp %s\n\n%s\n\n%s\n", c.usage, c.summary, c.description)
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

func unknownCommand(name string) error {
	message := fmt.Sprintf("unknown command %q", name)
	if suggestion := commandSuggestion(name); suggestion != "" {
		message += fmt.Sprintf(". Did you mean %q?", suggestion)
	}
	return usageError(errors.New(message))
}

func commandSuggestion(name string) string {
	best, bestDistance, tied := "", 3, false
	for _, command := range commands() {
		distance := editDistance(name, command.name)
		if distance < bestDistance {
			best, bestDistance, tied = command.name, distance, false
		} else if distance == bestDistance {
			tied = true
		}
	}
	if tied || bestDistance > 2 {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	aRunes, bRunes := []rune(a), []rune(b)
	previous := make([]int, len(bRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, ar := range aRunes {
		current := make([]int, len(previous))
		current[0] = i + 1
		for j, br := range bRunes {
			cost := 0
			if ar != br {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(bRunes)]
}
