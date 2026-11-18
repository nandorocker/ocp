<p align="center">
  <img src="docs/logo.png" alt="OCP logo" width="200">
</p>

<h1 align="center">OCP</h1>

<p align="center">
  OCP generates and activates reproducible <a href="https://opencode.ai/">OpenCode</a> configuration profiles from one <code>ocp.yaml</code> source.
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

## Commands

Run `ocp` or `ocp --help` for the command overview. Use `ocp help <command>` or `ocp <command> --help` for command options and examples.

```text
ocp setup
ocp sync
ocp apply
ocp use <profile>
ocp run <profile> [args...]
ocp list
ocp status
ocp ui [--no-open]
ocp version
ocp import [path]
ocp upgrade [skill <name>]
ocp reset
```

`ocp --version` prints the version embedded at build time. Put `--no-color` before a command, or set `NO_COLOR`, to disable ANSI styling.

The UI binds only to loopback. To use it through a trusted reverse proxy, opt in to one exact browser-facing origin with `ocp ui --trusted-origin https://host.example:4100`. OCP does not trust forwarded host or scheme headers.

`upgrade` is intentionally deferred in the current MVP implementation. Git-backed skills are initialized during setup or sync and reproduced from `ocp.lock`.

See [`docs/PRD.md`](docs/PRD.md) for the product requirements and configuration model.

## License

Copyright © 2026 Nando Rossi

Released under the [GNU Affero General Public License v3.0](LICENSE). In short: you may use, study, and modify OCP, but if you run a modified version as a network service you must publish your source.
