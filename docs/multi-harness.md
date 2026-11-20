# Multi-harness profiles

OCP installs native configuration for OpenCode, Claude Code, and Codex. Launch each application normally. There is no new launcher or separate profile format.

## Destinations

An existing source without `harnesses` continues to manage OpenCode only. To enable more destinations, add this root setting in `ocp.yaml`:

```yaml
version: 1
harnesses: [opencode, claude, codex]
```

The list must be non-empty and contain unique known names. It is not part of profile inheritance. Repeat `--harness` on `apply` or `use` to override the list for that invocation, including a destination not normally enabled:

```sh
ocp apply --harness claude --harness codex
ocp use openai --harness codex
```

## One modular source

Keep `profiles/*.yaml` or `profiles/<name>/profile.yaml`, optional `guide.md`, reusable `agents/`, and selected `skills/`. Root defaults, profile inheritance, and machine overlays continue to compose before target rendering.

```yaml
# profiles/openai/profile.yaml
config:
  model: openai/your-model
agents:
  reviewer:
    file: agents/reviewer.md
    model: openai/your-review-model
skills: [code-review]
```

The same agent instruction body and portable skills are reused. Native agents require a non-empty description, normally supplied in the source Markdown frontmatter:

```markdown
---
description: Review changes for correctness and regressions.
mode: subagent
---
Read the changed code and report actionable findings.
```

## Models

Scalar assignments retain their OpenCode meaning, including custom provider IDs. For other harnesses, OCP translates known provider prefixes only: `openai/<id>` to Codex's `<id>`, and `anthropic/<id>` to Claude's `<id>`. Cross-provider assignments are incompatible; there is no model substitution. Recognizable native scalar names are accepted for their own harness.

For exceptions, replace a scalar with a mapping:

```yaml
config:
  model:
    opencode: openrouter/your-model
    claude: opus
    codex: your-model
agents:
  reviewer:
    model:
      opencode: openai/your-review-model
      claude: sonnet
      codex: your-review-model
```

Mappings also work in `agents.<name>.config.model`, native `config.agent.<name>.model`, and agent-file frontmatter. Missing target entries mean unavailable, not an implicit fallback. Within profile inheritance, mappings merge per harness; scalar/map changes replace the previous value. An agent assignment overrides its source-frontmatter model rather than filling missing entries from that source.

Model compatibility covers representation, not account access, model availability, or equivalent capabilities. Native user/project/managed configuration precedence still applies.

## Selection and refresh

- `ocp use <profile>` selects that profile for each compatible enabled harness.
- `ocp apply` refreshes each target's own active profile; it does not synchronize their selections.
- A target without a selection uses `default` if present, otherwise the first compatible profile in sorted order.
- A removed active profile is reported as unavailable; select a replacement explicitly.
- An explicit incompatible target aborts before configuration writes, including when several targets were explicitly requested.
- Untargeted incompatible destinations are visibly skipped, preserving their files and previous selection. If all targets are skipped, the command fails.
- One incompatible effective agent model rejects the entire profile for that target.
- `ocp list` shows compatibility and selections when `harnesses` is declared. `ocp status` includes installed native selections.
- `ocp sync` honors configured destinations.

## What carries over

Supported shared content includes instruction files, profile guides, host instructions, portable skills, MCP servers, and agent bodies/descriptions/models. The agent adapter accepts `name`, `description`, `model`, `prompt` for config-only agents, and `mode: subagent` or `all`. Other options are rejected, not silently weakened.

From `config:`, the native adapters translate `model`, Codex's `model_reasoning_effort` and `mcp`. Settings that only shape OpenCode itself are omitted with a warning: `provider`, `providers`, `plugin`, `plugins`, `$schema`, `compaction`, `theme`, `autoupdate`, `share`, `keybinds`, `tui`, `formatter`, `lsp`, `small_model`, `username`, `layout`, `snapshot` and `watcher`. A profile's `files/` directory is OpenCode-only too. Anything else, such as `permission`, `tools`, `hooks` or `experimental`, would change behaviour if dropped, so it makes the profile unavailable for Claude Code and Codex.

## MCP servers

OCP reads MCP servers from `config.mcp`, in either OpenCode shape (`mcp.servers.<name>` or the older `mcp.<name>`), after root, profile and machine overlays are merged. Disabled servers are left out.

- **Codex:** each server becomes an `[mcp_servers.<name>]` table in `~/.codex/config.toml`.
- **Claude Code:** each server is added with `claude mcp add-json -s user` and removed with `claude mcp remove -s user`. OCP never writes `~/.claude.json` itself, because Claude keeps account state there and rewrites it constantly; it only reads the file to confirm each change. The `claude` command must be on PATH, otherwise the profile is unavailable for Claude Code. Each add or remove is journaled, and an interrupted apply resumes with `ocp apply --recover --harness claude`.

Secrets are never written into native files:

| OpenCode value | Claude Code | Codex |
|---|---|---|
| `{env:NAME}` in an environment value | `${NAME}` (Claude expands it) | `env_vars = ["NAME"]` when the name matches; otherwise through `ocp secret exec` |
| `{env:NAME}` in a header | `${NAME}` | `env_http_headers`, or `bearer_token_env_var` for `Authorization: Bearer {env:NAME}` |
| `{env:NAME}` in a url | `${NAME}` | server skipped with a warning |
| `{file:path}` in an environment value | the server runs through `ocp secret exec`, which reads the file when the server starts | same |
| `{file:path}` in a header | `headersHelper` runs `ocp secret headers` when Claude connects | server skipped with a warning |
| `{file:path}` in the command or url | server skipped with a warning | same |

`ocp secret exec` is called by its absolute path, so keep `ocp` installed where it is; reinstalling it elsewhere means running `ocp apply` again. Relative `{file:}` paths are made absolute against the OCP source; `~/` is expanded when the secret is read.

An `[mcp_servers.<name>]` table you wrote yourself is never replaced. If it is identical to what OCP would write, OCP adopts it and leaves it in place on reset; otherwise `apply` stops and names it.

## Providers and other native settings

Model providers work differently in each harness: Claude Code talks to an Anthropic-compatible endpoint, Codex to an OpenAI Responses endpoint, OpenCode to almost anything. So OCP does not translate `config.provider`. Each harness gets its own block, in its own format, at the root, in a profile or in a `machines.<name>` overlay. Blocks merge like `config:`, later layers winning.

```yaml
config:
  model:
    opencode: zai/glm-5.1
    claude: glm-5.1
    codex: gpt-5.5
claude:
  env:
    ANTHROPIC_BASE_URL: https://api.z.ai/api/anthropic
  apiKeyHelper: "{ocp} secret print '{file:~/.config/zai/key}'"
codex:
  model_provider: myproxy
  model_providers:
    myproxy:
      name: My Responses proxy
      base_url: http://127.0.0.1:4000/v1
      env_key: MYPROXY_KEY
```

- **`claude:`** accepts `env`, `apiKeyHelper` and `model`; they go to `~/.claude/settings.json`. `env` variables are owned one by one, so variables you set yourself stay.
- **`codex:`** accepts `model`, `model_provider`, `model_providers` and `model_reasoning_effort`; they go to `~/.codex/config.toml`. Codex only speaks the Responses API (`wire_api = "responses"`), which many GLM, Kimi and DeepSeek endpoints do not offer; put a proxy that does in front of them. The built-in `openai`, `ollama` and `lmstudio` providers cannot be redefined.
- **Secrets:** `{env:}` and `{file:}` are rejected inside these blocks, because the value would be written as is. For Claude Code use `apiKeyHelper` with `{ocp} secret print '<template>'`; `{ocp}` becomes the quoted path of the `ocp` binary. For Codex use `env_key`.
- **Collisions:** a key set both in a block and through `config:` (for example `model`) is an error. Set it in one place.
- `null` is not accepted anywhere in a block.

Every machine that syncs a source using `claude:` or `codex:` blocks needs an `ocp` build that understands them; older builds reject the unknown keys. Upgrade OCP on all machines first.

Skills need standard `name` and `description` frontmatter for native installation. Codex rejects unsupported skill extensions rather than ignoring invocation controls or execution semantics. Skill prose can still refer to harness-specific tools; OCP does not rewrite it.

Claude agents are emitted as Markdown under `~/.claude/agents/`; Codex agents are standalone TOML under `~/.codex/agents/`, following the current native format. This implementation does not provide a legacy Codex role-registration format. Use current CLI releases supporting standalone agents.

## Files and safety

Claude output uses `~/.claude/settings.json`, a bounded block in `~/.claude/CLAUDE.md`, agent files, and selected entries under `~/.claude/skills/`.

Codex output uses `~/.codex/config.toml`, a bounded block in `~/.codex/AGENTS.md`, standalone agent files, and selected entries under `~/.agents/skills/`. A non-empty global `AGENTS.override.md` is rejected when it would hide OCP instructions.

OCP preserves unrelated settings and guide text. Codex TOML edits preserve unrelated comments and formatting; Claude JSON may be reformatted. Existing values of owned settings are recorded for reset. Unmanaged generated-file collisions, edited owned content, and destination symlinks are refused. Credentials, runtime state, history, and entire native home directories are not taken over. Custom `CLAUDE_CONFIG_DIR`/`CODEX_HOME` environments are currently refused rather than installing into the wrong location.

Each native target has separate ownership records, stored as version 4. An older OCP refuses them rather than forgetting what it owns. OpenCode activation in multi-harness mode is pinned to an immutable release, so updates of another target cannot change a skipped OpenCode setup.

```sh
ocp apply --preview
ocp reset --harness claude --preview
ocp reset --harness claude
```

Preview performs no native configuration/state publication. Git-backed skills must already be locked and cached; run `ocp apply` first if needed.

Native updates are individually atomic file replacements, not one transaction across applications. OCP preflights selected targets and journals each Claude/Codex update. On an interrupted update, resume explicitly:

```sh
ocp apply --recover --harness claude
```

Recovery refuses files changed since the interrupted operation. Earlier targets may already have been updated if a later filesystem operation fails. `--force` applies to legacy OpenCode drift behavior only; it does not override native ownership conflicts. `ocp reset` detaches all installed destinations; a scoped reset preserves the other destinations and the canonical source.
