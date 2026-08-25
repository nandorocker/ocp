# CLI Basics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give OCP structured global and command help, build-time version output, standard usage errors, typo suggestions, and conventional color controls without adding a CLI framework.

**Architecture:** Keep `Runner.Run` and the existing command functions as the dispatch core. Add a focused `help.go` metadata/renderer unit, reuse flag configuration functions for parsing and help output, inject a version string into `Runner`, and classify usage failures with a dedicated `UsageError`. Extend the existing color package rather than introducing a presentation dependency.

**Tech Stack:** Go 1.26, standard-library `flag`, existing `internal/color`, Make, table-driven tests with `bytes.Buffer`.

---

## File Structure

- Create `internal/cli/help.go`: command metadata, help rendering, typo matching, and flag-set factories used by help.
- Create `internal/cli/help_test.go`: focused global help, command help, version, suggestion, and usage tests.
- Modify `internal/cli/cli.go`: global option preprocessing, dispatch integration, shared flag configuration, and usage-error returns.
- Modify `internal/cli/cli_test.go`: preserve command integration expectations and add argument-classification cases where command behavior owns validation.
- Modify `cmd/ocp/main.go`: inject version and preserve explicit exit classifications.
- Modify `Makefile`: inject `git describe` through `-ldflags` for `make build`.
- Modify `internal/color/color.go`: honor `NO_COLOR`, `TERM=dumb`, and explicit disable for every presentation helper.
- Modify `internal/color/color_test.go`: cover environment and prompt behavior without leaking global state.
- Modify `README.md`: document help/version entry points.

### Task 1: Structured Global And Command Help

**Files:**
- Create: `internal/cli/help.go`
- Create: `internal/cli/help_test.go`
- Modify: `internal/cli/cli.go:81-131,184-196,554-578,597-617,762-834,1108-1147`

- [ ] **Step 1: Write failing global-help tests**

Add tests that invoke an injected `Runner` with no arguments and with `--help`. Assert both outputs contain these stable lines:

```go
for _, want := range []string{
	"OCP manages reproducible OpenCode profiles.",
	"Usage:",
	"ocp [global options] <command> [arguments]",
	"Getting Started:",
	"ocp setup",
	"Commands:",
	"setup",
	"Configure OCP",
	"Global Options:",
	"--help",
	"--version",
	"--no-color",
} {
	if !strings.Contains(out.String(), want) {
		t.Errorf("help missing %q:\n%s", want, out.String())
	}
}
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run: `go test ./internal/cli -run 'TestGlobalHelp'`

Expected: FAIL because current help is a single command list.

- [ ] **Step 3: Add command metadata and global renderer**

Create `commandSpec` and one ordered registry in `help.go`:

```go
type commandSpec struct {
	Name, Usage, Summary, Description string
	Examples                          []string
	Flags                             func(*flag.FlagSet)
}

var commandSpecs = []commandSpec{
	{Name: "setup", Usage: "ocp setup [options]", Summary: "Configure OCP", Description: "Create or connect the canonical OCP source and activate a profile.", Flags: func(f *flag.FlagSet) { setupFlags(f, &setupOptions{}) }},
	{Name: "sync", Usage: "ocp sync [--force]", Summary: "Synchronize and render profiles", Flags: func(f *flag.FlagSet) { syncFlags(f, &forceOptions{}) }},
	{Name: "apply", Usage: "ocp apply [--force]", Summary: "Render canonical configuration", Flags: func(f *flag.FlagSet) { applyFlags(f, &forceOptions{}) }},
	{Name: "use", Usage: "ocp use <profile>", Summary: "Activate a profile"},
	{Name: "run", Usage: "ocp run <profile> [opencode arguments...]", Summary: "Run OpenCode with a profile"},
	{Name: "list", Usage: "ocp list", Summary: "List generated profiles"},
	{Name: "status", Usage: "ocp status", Summary: "Show OCP status"},
	{Name: "import", Usage: "ocp import [options] [path]", Summary: "Import OpenCode configuration", Flags: importFlags},
	{Name: "reset", Usage: "ocp reset [--force]", Summary: "Restore the previous configuration", Flags: resetFlags},
	{Name: "upgrade", Usage: "ocp upgrade", Summary: "Upgrade dependencies (deferred)"},
}
```

Implement `globalHelp(io.Writer)` with aligned command columns and the approved sections. Keep content plain so redirected output stays readable.

- [ ] **Step 4: Reuse real flag definitions**

Extract each flag declaration into option structs and configuration functions. Commands bind live option structs; help binds throwaway structs through the metadata closures:

```go
type forceOptions struct{ force bool }

func applyFlags(f *flag.FlagSet, options *forceOptions) {
	f.BoolVar(&options.force, "force", false, "overwrite generated drift")
}
```

Apply the same pointer-backed pattern to setup, sync, reset, and import.

- [ ] **Step 5: Write failing command-help tests**

Test both forms:

```go
for _, args := range [][]string{{"help", "setup"}, {"setup", "--help"}, {"setup", "-h"}} {
	// Assert usage, description, --source, --repo, --force, and an example.
}
```

Also assert `ocp help missing` returns a usage-classified error.

- [ ] **Step 6: Implement command help dispatch**

Handle `help [command]` and `<command> --help` before resolving `ocp.Paths`, so help works without a configured home directory. Render options by creating a `flag.FlagSet`, applying the command's real `Flags` function, and iterating `VisitAll` in deterministic order.

Do not intercept arguments after `run <profile>`; they belong to OpenCode.

- [ ] **Step 7: Run focused and package tests**

Run: `go test ./internal/cli -run 'Test(Global|Command)Help' && go test ./internal/cli`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/help.go internal/cli/help_test.go internal/cli/cli.go internal/cli/cli_test.go
git commit -m "feat(cli): add structured help"
```

### Task 2: Version Output And Build Injection

**Files:**
- Modify: `internal/cli/cli.go:39-48,81-117`
- Modify: `cmd/ocp/main.go:11-22`
- Modify: `Makefile:1-25`
- Test: `internal/cli/help_test.go`

- [ ] **Step 1: Write failing version tests**

Inject `Version: "v1.2.3"` into a `Runner` and verify both forms return no error and print exactly:

```text
ocp v1.2.3
```

Also verify an empty injected value prints `ocp dev`.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/cli -run TestVersion`

Expected: FAIL because `Runner.Version` and version dispatch do not exist.

- [ ] **Step 3: Implement version dispatch**

Add `Version string` to `Runner` and normalize it in `defaults()`:

```go
if r.Version == "" {
	r.Version = "dev"
}
```

Handle top-level `--version` and `version` before path resolution with `fmt.Fprintf(r.Out, "ocp %s\n", r.Version)`.

- [ ] **Step 4: Inject the build version**

Add a package variable in `cmd/ocp/main.go`:

```go
var version = "dev"

err := (&cli.Runner{Version: version}).Run(os.Args[1:])
```

Update the Makefile:

```make
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o "$(TARGET)" ./cmd/ocp
```

- [ ] **Step 5: Verify tests and binary output**

Run: `go test ./internal/cli && make build && ./ocp --version`

Expected: tests PASS and output starts with `ocp ` followed by the current `git describe` value.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cli.go internal/cli/help_test.go cmd/ocp/main.go Makefile
git commit -m "feat(cli): add version output"
```

### Task 3: Standard Usage Errors And Typo Suggestions

**Files:**
- Modify: `internal/cli/help.go`
- Modify: `internal/cli/cli.go:26-29,81-131,682-729,1108-1147`
- Modify: `cmd/ocp/main.go:11-22`
- Test: `internal/cli/help_test.go`
- Test: `internal/cli/cli_test.go`

- [ ] **Step 1: Write failing classification tests**

Cover unknown command, malformed flag, missing `use` profile, missing `run` profile, unexpected list argument, and extra import arguments. Assert `errors.As(err, *ExitError)` and code `2`. Confirm an injected path/state failure remains an ordinary error.

- [ ] **Step 2: Write failing typo tests**

Verify `ocp stats` includes:

```text
unknown command "stats"
Did you mean "status"?
```

Verify a distant input such as `nonsense` gets no suggestion.

- [ ] **Step 3: Run tests and confirm failure**

Run: `go test ./internal/cli -run 'Test(UsageErrors|CommandSuggestion)'`

Expected: FAIL because unknown and argument errors currently exit through code `1`.

- [ ] **Step 4: Add a usage error type**

Preserve messages while classifying exit status:

```go
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }
```

Update `main` to map `UsageError` to exit `2` after printing `ocp: <message>`. Keep `ExitError` for child/deferred command statuses.

- [ ] **Step 5: Wrap parser and positional validation failures**

Make the shared parser wrap `flag` parse failures and unexpected arguments. Return `UsageError` from `use`, `run`, and `import` shape validation. Do not wrap filesystem, state, rendering, Git, or child-process failures.

- [ ] **Step 6: Add conservative typo matching**

Implement a small Levenshtein distance helper in `help.go`. Suggest one command only when the nearest unique command has distance at most `2`. Include the `ocp --help` hint in every unknown-command error.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/cli ./cmd/ocp`

Expected: PASS (`cmd/ocp` may report no test files).

- [ ] **Step 8: Commit**

```bash
git add internal/cli/help.go internal/cli/help_test.go internal/cli/cli.go internal/cli/cli_test.go cmd/ocp/main.go
git commit -m "feat(cli): standardize usage errors"
```

### Task 4: Conventional Color Controls

**Files:**
- Modify: `internal/color/color.go`
- Modify: `internal/color/color_test.go`
- Modify: `internal/cli/cli.go:50-79,81-117`
- Test: `internal/cli/help_test.go`

- [ ] **Step 1: Write failing color tests**

Add table cases for a new unexported `colorAllowed(terminal bool)` helper. With `terminal=true`, assert `NO_COLOR=1`, `TERM=dumb`, and explicit disable each return false. Assert `Writer.Success` and `Writer.Prompt` contain no `\x1b[` sequence when color is disabled. Fix global-state cleanup by adding an auto-mode reset used with `t.Cleanup`.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/color`

Expected: FAIL because environment variables and `Prompt` are not honored consistently.

- [ ] **Step 3: Centralize color eligibility**

Update `colorEnabled()` to return false when disabled explicitly, `NO_COLOR` is present, or `TERM=dumb`. Route `Prompt` through `applyColor`:

```go
func colorAllowed(terminal bool) bool {
	return colorDisabled.Load() == 0 && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && terminal
}

func colorEnabled() bool { return colorAllowed(term.IsTerminal(2)) }

func (c *Writer) Prompt(num int, label string) string {
	return fmt.Sprintf("  %s) %s", applyColor(ANSIGreen, strconv.Itoa(num)), label)
}
```

Add `Reset()` for tests and repeated injected `Runner` invocations.

- [ ] **Step 4: Write and implement global `--no-color` tests**

Test `ocp --no-color --help` and `ocp --no-color status` parsing. Preprocess global options only before the command token. Call `color.Disable()` before rendering or dispatch. A bare `ocp --no-color` prints global help.

- [ ] **Step 5: Run color and CLI tests**

Run: `go test ./internal/color ./internal/cli`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/color/color.go internal/color/color_test.go internal/cli/cli.go internal/cli/help_test.go
git commit -m "feat(cli): honor standard color controls"
```

### Task 5: Documentation And Final Verification

**Files:**
- Modify: `README.md:73-105`

- [ ] **Step 1: Update user documentation**

Document `ocp`, `ocp help <command>`, `ocp <command> --help`, `ocp --version`, and global `--no-color`. State that bare `ocp` prints help and that setup remains explicit.

- [ ] **Step 2: Exercise representative output**

Run:

```bash
make build
./ocp
./ocp help setup
./ocp --version
NO_COLOR=1 ./ocp --help
```

Expected: structured help, setup options, one-line version output, and no ANSI escapes under `NO_COLOR`.

- [ ] **Step 3: Run repository verification**

Run:

```bash
go test ./...
go vet ./...
make build
git diff --check
```

Expected: all commands exit successfully.

- [ ] **Step 4: Inspect the final diff**

Run: `git status --short`, `git diff --stat`, and `git diff origin/main...HEAD`.

Confirm the branch contains only the design, plan, CLI implementation, tests, Makefile, and README changes.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md
git commit -m "docs: document CLI help and version"
```
