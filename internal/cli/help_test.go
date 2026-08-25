package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/color"
	"github.com/nando/ocp/internal/ocp"
)

func helpRunner(out *bytes.Buffer) *Runner {
	return &Runner{
		In:  strings.NewReader(""),
		Out: out,
		Err: out,
		Paths: func() (ocp.Paths, error) {
			panic("help must not resolve paths")
		},
	}
}

func TestGlobalHelpDoesNotResolvePaths(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"-h"}, {"help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := helpRunner(&out).Run(args); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, want := range []string{
				"OCP manages reproducible OpenCode profiles.", "Usage: ocp [global options] <command> [arguments]", "Getting Started", "ocp setup",
				"Commands:", "setup", "sync", "apply", "use", "run", "list", "status", "import", "upgrade", "reset",
				"Global Options:", "--help", "--version", "--no-color", "https://github.com/nandorocker/ocp",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("help missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestUsageErrorsAndSuggestion(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown", args: []string{"stats"}, want: `Did you mean "status"?`},
		{name: "unknown option", args: []string{"--wat"}, want: "unknown global option"},
		{name: "missing use profile", args: []string{"use"}, want: "use requires exactly one profile"},
		{name: "missing run profile", args: []string{"run"}, want: "run requires a profile"},
		{name: "unexpected list argument", args: []string{"list", "extra"}, want: "list: unexpected arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			r := runner(testPaths(t), &out)
			err := r.Run(test.args)
			var usage *UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want UsageError containing %q", err, test.want)
			}
			if test.args[0] == "use" && !strings.Contains(err.Error(), "ocp help use") {
				t.Fatalf("error missing command usage hint: %v", err)
			}
		})
	}
}

func TestRuntimeErrorIsNotUsageError(t *testing.T) {
	var out bytes.Buffer
	err := runner(testPaths(t), &out).Run([]string{"status"})
	var usage *UsageError
	if err == nil || errors.As(err, &usage) {
		t.Fatalf("error = %v, want runtime error", err)
	}
}

func TestGlobalNoColor(t *testing.T) {
	color.Reset()
	t.Cleanup(color.Reset)
	var out bytes.Buffer
	if err := helpRunner(&out).Run([]string{"--no-color", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("help = %q", out.String())
	}
	out.Reset()
	err := runner(testPaths(t), &out).Run([]string{"--no-color", "status"})
	if err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("--no-color was not consumed as a global option: %v", err)
	}
}

func TestVersionDoesNotResolvePaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		version string
		want    string
	}{
		{name: "flag", args: []string{"--version"}, version: "v1.2.3", want: "ocp v1.2.3\n"},
		{name: "command", args: []string{"version"}, version: "v1.2.3", want: "ocp v1.2.3\n"},
		{name: "default", args: []string{"--version"}, want: "ocp dev\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := helpRunner(&out)
			r.Version = tc.version
			if err := r.Run(tc.args); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVersionRejectsExtraArguments(t *testing.T) {
	for _, args := range [][]string{{"--version", "extra"}, {"version", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			err := helpRunner(&out).Run(args)
			if err == nil || !strings.Contains(err.Error(), "version: unexpected arguments: extra") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCommandHelpDoesNotResolvePaths(t *testing.T) {
	for _, args := range [][]string{{"help", "setup"}, {"setup", "--help"}, {"setup", "-h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := helpRunner(&out).Run(args); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, want := range []string{"Usage: ocp setup", "Initialize OCP from a local source or repository.", "Set up OCP", "--no-auto-commit", "--force", "--migrate-profiles", "--repo", "--source", "Examples:"} {
				if !strings.Contains(got, want) {
					t.Errorf("help missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestHelpRejectsExtraArguments(t *testing.T) {
	var out bytes.Buffer
	err := helpRunner(&out).Run([]string{"help", "setup", "extra"})
	if err == nil || !strings.Contains(err.Error(), "help accepts exactly one command") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunHelpDoesNotResolvePaths(t *testing.T) {
	var out bytes.Buffer
	if err := helpRunner(&out).Run([]string{"run", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Usage: ocp run <profile> [opencode arguments...]") {
		t.Fatalf("help = %q", out.String())
	}
}

func TestRunDoesNotInterceptArgumentsAfterProfile(t *testing.T) {
	p := testPaths(t)
	var out bytes.Buffer
	r := runner(p, &out)
	err := r.Run([]string{"run", "missing", "--help"})
	if err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("error = %v, want regular run behavior", err)
	}
}
