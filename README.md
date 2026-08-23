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

Create `ocp.yaml` in a directory you want to use as the canonical source:

```yaml
version: 1

config:
  model: openrouter/example-model

profiles:
  default: {}
  deep:
    config:
      model: openrouter/example-reasoning-model
```

Then initialize and switch profiles:

```bash
ocp setup --source /path/to/opencode-config
ocp use deep
opencode
```

OCP preserves an existing global OpenCode configuration during setup. `ocp reset` restores it and leaves the canonical OCP source untouched.

For a Git-backed canonical source on another machine:

```bash
ocp setup --repo git@github.com:user/opencode-config.git --source ~/.config/ocp
ocp sync
opencode
```

`ocp sync` uses the current branch's configured upstream, safely merges non-conflicting divergence, pushes local canonical changes when automatic commits are enabled, resolves locked Git skills, renders profiles and preserves the active profile.

## Commands

```text
ocp setup
ocp sync
ocp apply
ocp use <profile>
ocp run <profile> [args...]
ocp list
ocp status
ocp import [path]
ocp upgrade [skill <name>]
ocp reset
```

`upgrade` is intentionally deferred in the current MVP implementation. Git-backed skills are initialized during setup or sync and reproduced from `ocp.lock`.

See [`docs/PRD.md`](docs/PRD.md) for the product requirements and configuration model.
