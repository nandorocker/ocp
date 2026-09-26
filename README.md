# OCP

OCP generates and activates reproducible [OpenCode](https://opencode.ai/) configuration profiles from one `ocp.yaml` source.

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

An optional `profiles/deep/guide.md` is included in that profile's generated `AGENTS.md`. Paths in profile YAML remain relative to the OCP source root.

`agents.<name>.model` is shorthand for `agents.<name>.config.model`. A native `model` in referenced agent Markdown frontmatter is the default, but a profile agent assignment model overrides it.

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
