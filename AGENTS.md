# OCP Agent Instructions

## Commands

```bash
make build          # go build -o ocp ./cmd/ocp
make install        # builds + copies to ~/.local/bin/ocp
make uninstall      # removes installed binary
make clean          # removes local build artifacts
go test ./...       # all tests; no special setup
```

No CI, no linter — `go vet ./...` is sufficient before shipping changes.

## Architecture

Single Go binary (`github.com/nando/ocp`). One entry point:

```
cmd/ocp/main.go → internal/cli.Runner.Run(os.Args[1:])
```

Packages (all `internal/`):

| Package | Responsibility |
|---------|----------------|
| `cli`   | CLI command dispatch, interactive prompts, TTY UX |
| `config`| YAML parse of `ocp.yaml`, profile inheritance & merge |
| `ocp`   | State management, profile rendering, drift detection, OpenCode activation |
| `repository` | Git sync (commit/fetch/merge/push) against upstream |
| `skills` | Lockfile (`ocp.lock`) management and Git-backed skill checkout/cache |
| `importer` | Best-effort import from OpenCode and OPM configurations |
| `color` | Semantic terminal coloring wrapper |

CLI uses stdlib `flag` — not cobra/spf13. Command surface is flat: `setup, sync, apply, use, run, list, status, import, upgrade(deferred), reset`.

## Conventions

- **Inject for testability.** `Runner` fields `In`, `Out`, `Err`, `Paths`, `Getwd`, `Exec` default to real implementations but are swappable. Write tests by injecting fake values and using `t.TempDir()`. See `cli_test.go:testPaths()` and `runner()` helpers.
- **Tests use `bytes.Buffer`** for stdout/stderr assertions, never real TTY I/O (except when the TTY check itself is being tested).
- **Strict YAML keys.** `config.mapping()` rejects unknown keys at any level. If adding schema fields, register them in every `mapping()` call that accepts them (see `parseDocument`, `profiles`, `agents`, `skill` mappings).
- **Profile resolution walks DAG.** Inheritance cycles produce error messages like "profile inheritance cycle at …". Adding new composition fields requires updates to `mergeComposition`, `cloneAgents`, `cloneMap`, `cloneValue`, and `cloneProfileSpec`.
- **Git isolation.** All git commands set `GIT_TERMINAL_PROMPT=0`, `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`. Never assume system/user git config exists during sync or skill checkout.
- **Atomic writes everywhere.** All state files use temp-file-then-rename (`atomicFile`, `writeLock`). Generated profiles render into a temp dir, then rename to an immutable timestamped release and update a symlink atomically via temp-symlink-rename.
- **File permissions.** Private data gets `0o600` (state, lock, generated content). Directories get `0o700`.
- **Advisory lock.** `syscalls.Flock` on `~/.config/.opencode.ocp.lock` prevents concurrent OCP operations. Every command that modifies state calls `AcquireLock`.
- **Drift tracking.** Each rendered profile contains `.ocp-manifest.json` with SHA-256 hashes. `ocp.Render()` compares current files against the manifest before applying.
- **Source safety.** Path walking validates no symlinks escape the source tree (`sourcePathSafe`, `within`). Skill sources cannot reference outside the canonical directory.
- **XDG paths.** Default locations derive from `HOME` + `XDG_CONFIG_HOME`/`XDG_DATA_HOME`/`XDG_STATE_HOME`. Override via env vars or by constructing custom `ocp.Paths` in tests.
- **`upgrade` is not implemented.** Returns exit code 2. Documented in PRD §15 but deferred.
