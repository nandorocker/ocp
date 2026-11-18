# CLI output preview

A live, in-terminal preview for designing how OCP prints things: colors, effects, layout, and the plain/piped fallback. Leave it running in a spare terminal; every time a file changes it re-renders, so there is no command to re-run. Think of it as a browser preview for the CLI.

```bash
make preview                       # all scenarios
make preview SCENARIOS="overview router"
tools/preview/watch.sh apple       # same thing, without make
```

Stop with `Ctrl-C`. Judge colors in **your own terminal**: its theme changes how basic ANSI colors look, which is the point.

## What is here

| File | Purpose |
|------|---------|
| `theme.go` | Every color and effect as a named style (`origin.root`, `src.local`, `active`, `muted`, …). **Start here.** The header comment lists the ANSI options. |
| `render.go` | Layout: tree, tables, collapsing, ASCII fallback. |
| `data.go` | Fixture profiles that mirror the real config (root skills, `extends`, machine MCP, disabled entries, an 84-skill profile). Edit it to test an edge case. |
| `main.go` | Scenario picker and the plain-output section. |
| `watch.sh` | The watcher. `inotifywait` on Linux, polling elsewhere. |

Scenarios: `overview` (root and profile tree), `router` (single-profile detail), `mixed` (a profile that overrides inherited values), `apple` (long list collapsed by repository), `notices` (warnings, drift, ok), `plain` (no color, ASCII glyphs, as when piped or under `NO_COLOR`), `all`.

## Choosing colors

- **Basic ANSI** (`30`–`37`, `90`–`97`) follows the user's terminal theme, so it works on light and dark backgrounds. It is also what `internal/color` uses today. Prefer it.
- **256-color** (`38;5;N`) and **truecolor** (`38;2;R;G;B`) give exact shades but do not adapt to the terminal theme.
- Effects: `1` bold, `2` dim, `3` italic, `4` underline, `7` reverse, `9` strike. Combine with `;`, e.g. `1;32`.
- Never rely on color alone. Meaning must survive `NO_COLOR` and piping (labels, glyphs, position). Check the `plain` scenario.

## Promoting a design into the real CLI

This folder is a sandbox with its own `go.mod`, so `go build ./...` and `go test ./...` in the repo ignore it. It does not import `internal/` and is not shipped.

When a look is settled:

1. Add the chosen styles to `internal/color` as semantic helpers (the package already wraps ANSI codes; keep `NO_COLOR`, `TERM=dumb` and non-TTY handling).
2. Implement the command in `internal/cli` and have it render through those helpers.
3. Cover it with tests that inject `Out` and compare against a `bytes.Buffer`, with color forced off and, where it matters, on.
4. Keep `data.go` fixtures in step with real output so the preview does not drift from the shipped behavior.
