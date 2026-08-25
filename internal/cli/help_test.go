package cli

import (
	"bytes"
	"strings"
	"testing"

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
				"OCP", "Usage: ocp [global options] <command> [arguments]", "Getting Started", "ocp setup",
				"Commands:", "setup", "sync", "apply", "use", "run", "list", "status", "import", "upgrade", "reset",
				"Global Options:", "--help", "--version", "--no-color", "https://github.com/nando/ocp",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("help missing %q:\n%s", want, got)
				}
			}
		})
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
