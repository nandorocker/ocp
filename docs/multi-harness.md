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
- `ocp sync` and the UI's apply operation honor configured destinations. The UI remains OpenCode-focused; YAML is the multi-harness authoring surface.

## Initial adapter scope

Supported shared content includes instruction files, profile guides, host instructions, portable skills, and agent bodies/descriptions/models. The initial agent adapter accepts `name`, `description`, `model`, `prompt` for config-only agents, and `mode: subagent` or `all`. Other options are rejected, not silently weakened.

The native root settings adapter supports `model`, plus Codex's `model_reasoning_effort`. OpenCode plugins and `$schema` are omitted elsewhere with warnings. Other native OpenCode settings, including permissions, providers, tools, MCP, and hooks, make the profile unavailable to adapters that cannot represent them. OpenCode's existing renderer retains its native configuration support.

Skills need standard `name` and `description` frontmatter for native installation. Codex rejects unsupported skill extensions rather than ignoring invocation controls or execution semantics. Skill prose can still refer to harness-specific tools; OCP does not rewrite it.

Claude agents are emitted as Markdown under `~/.claude/agents/`; Codex agents are standalone TOML under `~/.codex/agents/`, following the current native format. This implementation does not provide a legacy Codex role-registration format. Use current CLI releases supporting standalone agents.

## Files and safety

Claude output uses `~/.claude/settings.json`, a bounded block in `~/.claude/CLAUDE.md`, agent files, and selected entries under `~/.claude/skills/`.

Codex output uses `~/.codex/config.toml`, a bounded block in `~/.codex/AGENTS.md`, standalone agent files, and selected entries under `~/.agents/skills/`. A non-empty global `AGENTS.override.md` is rejected when it would hide OCP instructions.

OCP preserves unrelated settings and guide text. Codex TOML edits preserve unrelated comments and formatting; Claude JSON may be reformatted. Existing values of owned settings are recorded for reset. Unmanaged generated-file collisions, edited owned content, and destination symlinks are refused. Credentials, runtime state, history, and entire native home directories are not taken over. Custom `CLAUDE_CONFIG_DIR`/`CODEX_HOME` environments are currently refused rather than installing into the wrong location.

Each native target has separate ownership records. OpenCode activation in multi-harness mode is pinned to an immutable release, so updates of another target cannot change a skipped OpenCode setup.

```sh
ocp apply --preview
ocp reset --harness claude --preview
ocp reset --harness claude
```

Preview performs no native configuration/state publication. Git-backed skills must already be locked and cached; run `ocp sync` first if needed.

Native updates are individually atomic file replacements, not one transaction across applications. OCP preflights selected targets and journals each Claude/Codex update. On an interrupted update, resume explicitly:

```sh
ocp apply --recover --harness claude
```

Recovery refuses files changed since the interrupted operation. Earlier targets may already have been updated if a later filesystem operation fails. `--force` applies to legacy OpenCode drift behavior only; it does not override native ownership conflicts. `ocp reset` detaches all installed destinations; a scoped reset preserves the other destinations and the canonical source.
