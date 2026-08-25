# CLI Basics Design

## Goal

Give OCP the presentation and conventions users expect from a small command-line tool without replacing the standard-library `flag` parser or changing profile-management behavior.

## Command Surface

OCP will support these help and version forms:

```text
ocp
ocp --help
ocp -h
ocp help
ocp help <command>
ocp <command> --help
ocp <command> -h
ocp --version
ocp version
```

A bare `ocp` invocation prints the full concise help and exits successfully. It never starts setup or changes state. The help page places `ocp setup` under a prominent Getting Started section.

Global options precede the command, as in `ocp --no-color status`. OCP does not consume global options after `run` because those arguments belong to OpenCode.

## Help Content

Global help contains:

- OCP's one-line purpose
- usage syntax
- a Getting Started section
- aligned command names and summaries
- global options
- short examples
- the documentation URL

Command help contains:

- purpose and description
- exact usage syntax
- options derived from the command's real `flag.FlagSet`
- relevant examples

A small metadata table in `internal/cli` stores command usage, summaries, descriptions, and examples. Existing command functions and dispatch remain in place. Option help comes from the actual flag definitions so parser behavior and documentation stay synchronized.

## Version

`ocp --version` and `ocp version` print one line:

```text
ocp <version>
```

The build injects the version with `-ldflags`. `make build` uses `git describe --tags --always --dirty`; a raw `go build` reports `dev`. Tagged release builds therefore display the tag while local builds remain identifiable.

## Errors And Exit Codes

Help and version requests exit `0`.

Unknown commands, malformed options, missing command arguments, and unexpected arguments exit `2`. Their output contains a concise error and the relevant usage hint. A close unknown command gets one suggestion, such as `stats` suggesting `status`.

Runtime and state failures exit `1`. `ocp run` continues to preserve the child process exit status.

## Terminal Conventions

OCP disables ANSI styling when any of these conditions apply:

- output is not a terminal
- `NO_COLOR` is set
- `TERM=dumb`
- the user passes global `--no-color`

Help remains readable without color. Terminal styling stays restrained and semantic.

## Scope Boundaries

This work does not add a CLI framework, shell completion, JSON output, command aliases, or a `doctor` command. Those features add public behavior and should receive separate designs.

## Tests

Tests cover:

- bare invocation and global help structure
- both command-help forms
- option lists sourced from command flag definitions
- version injection and output
- unknown-command suggestions
- usage-error versus runtime-error exit classification
- `NO_COLOR`, `TERM=dumb`, and `--no-color`

Tests assert stable sections and important lines rather than whitespace-sensitive full-page snapshots.
