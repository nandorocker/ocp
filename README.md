<p align="center">
  <img src="docs/logo.png" alt="OCP logo" width="200">
</p>

<h1 align="center">OCP</h1>

<p align="center">
  OCP generates and activates reproducible configuration profiles for OpenCode, Claude Code, and Codex from one <code>ocp.yaml</code> source.
</p>

<p align="center">
  <a href="LICENSE"><img alt="AGPL-3.0 license" src="https://img.shields.io/badge/license-AGPL--3.0-blue.svg"></a>
</p>

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/nandorocker/ocp/main/install.sh | sh
```

The installer detects your platform, downloads the matching release archive, verifies its SHA-256 checksum, and installs the binary to `~/.local/bin/ocp`. If that directory is not on your `PATH`, add it to your shell profile.

Pin a specific version with `OCP_VERSION`, or override the destination with `OCP_INSTALL`:

```bash
curl -fsSL https://raw.githubusercontent.com/nandorocker/ocp/main/install.sh | OCP_VERSION=0.2.0 OCP_INSTALL=/usr/local/bin sh
```

Then initialize OCP:

```bash
ocp setup
```

## Developer Setup

Go 1.26 or newer is required. The simplest way to install OCP for local development:

```bash
make install
```

This builds the binary and installs it to `~/.local/bin/ocp`, creating that directory if needed. If `~/.local/bin` is not yet on your `PATH`, you will see a warning; add it to your shell profile.

To remove only the installed binary (without touching any OCP configuration or user data):

```bash
make uninstall
```

Both `PREFIX` and `BINDIR` can be overridden, e.g.:

```bash
make install BINDIR=/usr/local/bin
```

The same commands used by end users form the core developer test path:

```bash
make install
ocp setup
ocp status
ocp sync
```

## Quick Start

Create `ocp.yaml` for shared configuration:

```yaml
version: 1

config:
  model: openrouter/example-model
```

For multiple profiles, use one YAML file per profile. Flat files remain supported, and profiles can also have a folder with `profile.yaml` and an optional `guide.md`:

```yaml
# profiles/default/profile.yaml
{}
```

```yaml
# profiles/deep/profile.yaml
extends: default
config:
  model: openrouter/example-reasoning-model
agents:
  implementer:
    model: openai/example-coding-model
```

An optional `profiles/deep/guide.md` is included in that profile's generated `AGENTS.md`. Agent and instruction paths in profile YAML are relative to the OCP source root; skill and plugin references use their own standard folders.

### Extra files

Anything under `profiles/<name>/files/` is copied into the rendered profile root, so tools and plugins that read their own files from the OpenCode config directory can be managed by a profile. For example, `profiles/deep/files/my-plugin.jsonc` renders to `~/.config/opencode/my-plugin.jsonc`, and `files/conf/a.json` renders to `conf/a.json`.

- Extra files are covered by drift detection like any other generated file; edit the source and run `ocp apply` instead of editing the rendered copy.
- With `extends`, the parent's files are rendered first and the child's files win per path.
- Files render with mode `0600`. Symlinks and non-regular files are rejected.
- Names that collide with generated output (`opencode.json`, `AGENTS.md`, `agents/`, `skills/`) are rejected.
- Rendered releases are immutable, so state a tool writes next to its config file is per release and is not carried over by `ocp apply`.
- `machines:` overlays cannot add files; `files/` applies on every machine that gets the profile.

During `ocp setup`, OCP asks for this machine's name (defaulting to the hostname, overridable with `--machine`). The name is stored in local state and drives machine-aware rendering:

```yaml
# profiles/hybrid/profile.yaml — shared base plus per-machine overlays
config:
  model: openai/gpt-6-sol
skills:
  - stop-slop
machines:
  windy:
    config:
      provider:
        ollama:
          options:
            baseURL: http://127.0.0.1:11434/v1
```

Root `ocp.yaml` accepts the same `machines:` and `hosts:` keys. A root `machines.<name>` overlay applies to every profile rendered on that machine (profile-level overlays for the same machine merge on top), so a machine-only MCP server is declared once:

```yaml
# ocp.yaml
version: 1
machines:
  cosmonaut:
    config:
      mcp:
        servers:
          playwright:
            type: local
            command: [npx, -y, "@playwright/mcp@latest"]
```

A root `hosts:` list is the default availability for every profile; a profile's own `hosts` replaces it.

A profile with `hosts: [windy]` renders only on Windy; without `hosts` it renders everywhere. When the machine is named `windy`, OCP automatically appends `hosts/windy.md` to that profile's `AGENTS.md` — no declaration needed.

Local skills live under `skills/` (including nested folders such as `skills/apple/`). List paths relative to that folder: `skills: [stop-slop, apple/swiftlint]`. Each profile explicitly lists the skills it uses; folder names do not enable skills automatically. Existing `./skills/...` declarations remain supported. Git-backed skills use a Git URL (`https://`, `ssh://` or `git@host:path`, any host) or the `github:owner/repo` shorthand. Append `//<subpath>` to take one skill folder from a repository that holds many, and `@<ref>` to pin a tag, branch or commit:

```yaml
skills:
  - stop-slop                                    # local: skills/stop-slop
  - github:anthropics/skills//skills/pdf         # one folder of a repo
  - github:anthropics/skills//skills/docx@v1.2   # pinned
  - https://gitlab.com/team/skills@v2            # whole repo is the skill
  - git: git@myserver:team/skills.git            # expanded form, any URL
    path: debugging/tracing
    ref: main
```

The subpath must stay inside the repository, must not pass through symlinks, and must contain `SKILL.md`. Each skill is locked separately in `ocp.lock`, and the skill is named after its last path segment. The `@ref` suffix works on `https://` and `github:` entries; use the expanded form for a ref on `ssh://` or `git@` URLs. Once `ocp.lock` contains a `path`, every machine syncing that source needs an `ocp` build that supports subpaths; older builds reject the lock. Cached checkouts that the lock no longer references are removed automatically.

To switch a skill or plugin off without deleting it, name it in `disabled:`. It stays declared, validated and pinned in `ocp.lock`, but is not rendered:

```yaml
# profiles/lean/profile.yaml
extends: default
disabled: [karpathy-guidelines, opencode-model-router]
```

- Skills are named by their last path segment, as they render. Plugins are named by file name without extension, or by npm package name without a version.
- `disabled:` works at the root, in a profile and in a `machines.<name>` overlay. Lists add up down the inheritance chain, so a profile can switch off what its parent or the root adds. To turn something back on, remove it from the list.
- A name that matches nothing is a warning, not an error, so removing a skill upstream does not break the profile.
- `ocp show` lists disabled entries from the root and profiles. A `disabled:` list inside a machine overlay is applied on that machine but not shown yet.

`agents.<name>.model` is shorthand for `agents.<name>.config.model`. A native `model` in referenced agent Markdown frontmatter is the default, but a profile agent assignment model overrides it.

Local plugin files live under `plugins/`. List `.ts`/`.js` filenames relative to that folder in `plugins` or `config.plugin`, for example `harness-bridge.ts`. Existing `./plugins/...` declarations remain supported. OCP validates local files and renders `file://` URLs using this machine's source path; npm package declarations pass through unchanged. Local skills and plugins outside their standard folders are rejected.

Then initialize and switch profiles:

```bash
ocp setup --source /path/to/opencode-config
ocp use deep
opencode
```

OCP preserves an existing global OpenCode configuration during setup. `ocp reset` restores it and leaves the canonical OCP source untouched.

Legacy inline profiles remain readable. Migrate them explicitly after setup with:

```bash
ocp setup --migrate-profiles
```

Before initial setup, pass the legacy source explicitly: `ocp setup --migrate-profiles --source /path/to/source`.

For a Git-backed canonical source on another machine:

```bash
ocp setup --repo git@github.com:user/opencode-config.git --source ~/.config/ocp
ocp sync
opencode
```

`ocp sync` uses the current branch's configured upstream, safely merges non-conflicting divergence, pushes local canonical changes when automatic commits are enabled, resolves locked Git skills, renders profiles and preserves the active profile.

## Bring an existing setup

Most people already have instructions, skills and MCP servers spread across Claude Code, Codex and OpenCode. `ocp adopt` lists them, marking each one as already in the source, new, or different between harnesses. It also flags an instruction file that is an older copy of one in your source. It only reads.

```bash
ocp adopt                         # what each harness holds outside OCP
ocp adopt --json                  # the same, for an agent with the OCP skill
ocp adopt --write ~/.config/ocp   # no source yet: create one from it
```

`--write` copies skills, uses the first instruction file as `AGENTS.md`, and converts MCP servers to OpenCode's format.

- **Secrets:** values that look like secrets become `{env:NAME}` references, and the command lists each variable to set. This covers environment values, headers, URL credentials and query parameters, and command flags such as `--token`. Files such as `.env`, `*.pem` and `*.key` are left out of copied skills.
- **Check before committing:** review the copied skills and `AGENTS.md`, which are copied as they are, before you commit the source.
- **Conflicts:** `--write` dry-runs the install and lists hand-made entries that would conflict with `ocp setup`, such as an MCP server whose secret is now read from the environment. Remove those first.
- **Hand-copied files:** when OCP installs into a harness, a hand-copied file identical to the source's is adopted rather than refused. If OCP later changes or drops it, the original file and its permissions are put back.

## Let your agent manage OCP

Agents edit configuration now more often than people do. OCP ships a skill that teaches Claude Code, Codex and OpenCode to make changes in the OCP source rather than in one harness's files. With it, "add the Playwright MCP, Mac only" lands in the right overlay and reaches every harness. Add it like any Git skill:

```yaml
skills:
  - github:nandorocker/ocp//skills/ocp
```

The skill relies on `ocp status --json` to find the source and `ocp show --json` to read the effective configuration.

## Commands

### Multiple harnesses

Existing configurations remain OpenCode-only. Opt in to additional native destinations:

```yaml
version: 1
harnesses: [opencode, claude, codex]

config:
  model:
    opencode: openai/your-model
    claude: opus
    codex: your-model
```

Keep your existing profiles, shared instructions, agents, and skills. Scalar models such as `openai/your-model` still work: OCP preserves them for OpenCode, strips the known provider prefix for Codex, and reports incompatibility for Claude. Use a model mapping only where targets need different assignments.

```sh
ocp apply --preview
ocp use openai
ocp apply --harness claude
ocp reset --harness codex
```

Launch each harness normally—no wrapper is needed. Explicit incompatible targets fail before configuration writes; default operations skip incompatible targets and keep their previous setup. Instructions, skills, agents and models carry over. MCP servers carry over to Claude Code and Codex, with secrets read at run time rather than written into their config. Model providers differ per harness, so each harness gets its own `claude:` or `codex:` block for them. Permissions and hooks are not translated. See [multi-harness support](docs/multi-harness.md) for syntax, supported content, and safety behavior.

Run `ocp` or `ocp --help` for the command overview. Use `ocp help <command>` or `ocp <command> --help` for command options and examples.

```text
ocp setup
ocp sync
ocp apply
ocp use <profile>
ocp run <profile> [args...]
ocp list
ocp status [--json]
ocp show [profile] [--json] [--all]
ocp version
ocp import [path]
ocp adopt [--json] [--write <dir>]
ocp upgrade [--preview] [--force] [skill <name>]
ocp reset
```

`ocp show` describes the whole configuration file and what it affects, not just what works on this machine. It resolves from the source config, so it works before `ocp apply`, and it lists every profile and every machine's overlays. `ocp show` draws what applies to every profile (`Global`) and then the profiles as a tree, nested under the profile they extend, listing only what each profile adds. `ocp show <profile>` lists that profile's model, skills, plugins, MCP servers, providers, agents, instructions and extra files.

- **Origin column:** entries the profile declares itself carry no mark. Everything else says where it comes from: `global`, `extends <profile>`, or the machine whose overlay supplies it. Your current machine is bold.
- **Overrides:** an entry marked `*` replaces a different value that something lower already defined (a parent profile, `global` or an earlier layer). New entries and exact repeats are not overrides.
- **Host-limited profiles:** a profile with `hosts:` shows `(windy only)` after its name in the tree and a `hosts` row in its detail, and only machine overlays that can apply to it are listed.
- **Sources:** Git skills show just their repository, since Git is the default. Other sources are labelled `local`, `npm`, `path`, `remote` or `stdio`. MCP servers appear by name and type only; settings, headers and secrets are never printed, and credentials embedded in Git URLs are stripped.
- **Width:** name lists in the tree are fitted to the terminal (`COLUMNS` when piped) and end in `+ N others`. In the detail view, more than eight skills are grouped by repository unless you pass `--all`.
- **Warnings:** local skill folders and plugin files that no profile references are listed under the tree. A clean configuration prints nothing extra.
- **Context cost:** each profile shows roughly how many tokens it loads before the first prompt on this machine: instructions (including the host file), skill names and descriptions, and agent descriptions. `ocp show <profile>` breaks it down and notes what loads later (skill bodies) or isn't measured (MCP tool definitions, which come from running the servers). `ocp list` shows the same total. Estimates are characters ÷ 4, not a specific model's tokenizer, and Git skills that are not cached yet are listed as not measured.
- **Plain output:** piped output and `NO_COLOR` produce ASCII with no escapes. `--json` prints the same data for scripts.

`ocp --version` prints the version embedded at build time. Put `--no-color` before a command, or set `NO_COLOR`, to disable ANSI styling.

Git-backed skills are locked to a commit in `ocp.lock` the first time `ocp setup`, `ocp apply` or `ocp sync` sees them, so every machine renders the same files. `ocp upgrade` moves skills that follow a branch (no `@ref`, or a branch name) to the branch's latest commit, updates `ocp.lock` and re-renders. Skills pinned to a tag or a full commit ID never move; to move one, change its ref in the source and run `ocp apply`, which locks the new ref. `ocp upgrade skill <name>` upgrades one skill and `--preview` shows the changes without writing anything. If re-rendering fails, for example because of drift, `ocp.lock` is put back as it was. Run `ocp sync` afterwards to commit the new lock and share it.

See [`docs/PRD.md`](docs/PRD.md) for the product requirements and configuration model.

## License

Copyright © 2026 Nando Rossi

Released under the [GNU Affero General Public License v3.0](LICENSE). In short: you may use, study, and modify OCP, but if you run a modified version as a network service you must publish your source.
