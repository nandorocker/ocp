# OCP — MVP Product Requirements Document

## 1. Summary

**OCP** is a declarative configuration and profile manager for OpenCode.

It replaces manually maintained OpenCode/OPM profile directories with a single human-readable source of truth that can generate complete OpenCode environments.

Core flow:

```text
ocp.yaml
   ↓
resolve configuration
   ↓
generate OpenCode environment(s)
   ↓
activate profile
```

With Git sync configured:

```text
canonical Git repo
        ↓
     ocp sync
        ↓
sync source → resolve dependencies → render → activate
```

The intended everyday remote-machine workflow is:

```bash
ocp sync
opencode
```

OCP should make that machine current and ready to work.

---

## 2. Problem

OpenCode environments can include:

- `opencode.json`
- `AGENTS.md`
- agent Markdown files
- skills and their supporting files
- plugin declarations
- package/runtime artifacts
- other OpenCode-supported configuration

Maintaining several complete profiles creates duplication and drift, especially when:

- most profiles share the same base configuration;
- only models, agents, skills, or plugins differ;
- the same setup is used across local and remote machines.

OCP separates:

**canonical source configuration** from **generated OpenCode environments**.

---

## 3. Product Principles

### 3.1 Declarative source of truth

Users describe what their OpenCode environment should contain.

OCP generates the filesystem structure OpenCode expects.

Generated environments are outputs, not canonical configuration.

### 3.2 Additive by default

Root-level configuration applies to all profiles.

Profiles add to or override inherited configuration.

Users should not need empty declarations such as:

```yaml
global:
  skills: none
```

If something is absent, it simply does not exist.

### 3.3 Minimal configs stay minimal

Profiles are optional.

A user may use OCP only to maintain and sync one OpenCode configuration.

### 3.4 Native OpenCode configuration stays native

OCP should not recreate OpenCode's configuration schema.

Native OpenCode options live under:

```yaml
config:
```

and are rendered into `opencode.json`.

OCP-specific concepts should be limited primarily to:

- composition
- profiles
- inheritance
- source resolution
- rendering
- synchronization

### 3.5 One-way generation

Normal flow is:

```text
OCP source → generated OpenCode environment
```

not:

```text
OCP source ↔ generated environment
```

Manual edits to OCP-generated files count as drift.

### 3.6 Seamless until destructive

Safe routine operations should proceed without unnecessary prompts.

Before unexpected destructive behavior, OCP must:

1. clearly explain what will be overwritten or lost;
2. allow interactive users to cancel;
3. fail in non-interactive mode unless an explicit force flag is supplied.

### 3.7 Automatic decisions must be visible

OCP may make safe decisions automatically, but should clearly communicate meaningful ones.

Example:

```text
Active profile "deep" no longer exists.
Switched to "default".
```

### 3.8 Platform scope

MVP supports:

- Linux
- macOS

Native Windows support is out of scope for MVP.

---

## 4. Canonical Repository

An OCP installation has one canonical source directory.

Example:

```text
my-opencode/
├── ocp.yaml
├── ocp.lock
├── AGENTS.md
├── agents/
├── skills/
└── plugins/
```

The directory may optionally be a Git repository.

Git is the synchronization transport.

OCP manages Git operations but does not manage Git authentication.

If a repository is private, normal Git authentication on that machine must already work.

---

## 5. Configuration Model

Default configuration file:

```text
ocp.yaml
```

YAML is preferred for human readability and economical syntax.

### 5.1 Minimal configuration

This is valid:

```yaml
version: 1
```

It represents one implicit profile named:

```text
default
```

A simple single-profile configuration might be:

```yaml
version: 1

skills:
  - https://github.com/example/debugging

plugins:
  - opencode-wakatime

agents:
  implementer:
    file: ./agents/implementer.md
```

Profiles are therefore optional.

### 5.2 Root schema

Core MVP keys:

```yaml
version: 1

config:
  # native OpenCode configuration

instructions:
  - ./AGENTS.md

skills:
  - ...

plugins:
  - ...

agents:
  ...

profiles:
  ...
```

MVP does not include a generic `files:` abstraction.

Arbitrary file materialization may be reconsidered later if a concrete need appears.

### 5.3 Native OpenCode configuration

All native OpenCode settings live under `config:`.

Example:

```yaml
config:
  model: openrouter/example-model

  permission:
    bash:
      "*": ask
      "git status*": allow
```

The same rule applies inside profiles:

```yaml
profiles:
  deep:
    config:
      model: openrouter/example-model
```

OCP must not provide a second shorthand such as:

```yaml
profiles:
  deep:
    model: ...
```

There is one canonical location for native OpenCode configuration.

---

## 6. Skills

OCP supports three skill-source categories.

### 6.1 Repository-managed local skill

```yaml
skills:
  - my-skill
```

The path is relative to the canonical repository's `skills/` directory. Nested paths such as `apple/swiftlint` are supported; local skills outside `skills/` are rejected. Legacy `./skills/...` declarations remain readable.

Example:

```text
skills/
└── my-skill/
    ├── SKILL.md
    ├── scripts/
    └── references/
```

OCP treats the skill directory as an opaque OpenCode skill package.

### 6.2 Git-backed skill

Normal syntax should use an explicit Git URL:

```yaml
skills:
  - https://github.com/example/debugging
```

Pinned Git ref:

```yaml
skills:
  - https://github.com/example/debugging@v1.4.0
```

The suffix after `@` is a Git ref and may represent:

- tag
- branch
- commit

Expanded syntax is supported when shorthand becomes ambiguous:

```yaml
skills:
  - git: git@myserver:team/debugging.git
    ref: main
```

OCP should prefer explicit syntax over guessing.

### 6.3 Local source boundaries

Repository-local skills must live under `skills/`. Absolute and escaping local paths are rejected instead of creating machine-specific dependencies.

### 6.4 Skill source parsing

MVP parsing rules:

- bare name or nested path → local path under `skills/`
- `./skills/...` → legacy explicit local path under `skills/`
- absolute or escaping local path → error
- explicit HTTP(S) Git URL → Git-backed skill
- expanded `git:` form → Git-backed skill

OCP should not infer Git sources from arbitrary bare strings.

---

## 7. Skill Identity and Merge Rules

Skills are additive across:

```text
root → parent profile → child profile
```

The same logical skill must not be installed multiple times.

### 7.1 Exact duplicate

If the same declaration appears more than once:

```yaml
skills:
  - https://github.com/example/foo
```

it is silently deduplicated.

### 7.2 Same source, different ref

Example:

```yaml
skills:
  - https://github.com/example/foo@v1

profiles:
  deep:
    skills:
      - https://github.com/example/foo@v2
```

The more specific declaration wins.

Resolution precedence:

```text
root < parent < child
```

`deep` receives one instance of `foo`, at `v2`.

### 7.3 Destination collision

If two different sources would render to the same logical skill destination and OCP cannot confidently determine equivalence, rendering must fail with a clear error.

OCP must not guess.

---

## 8. Skill Locking

Git-backed skills are reproducible through:

```text
ocp.lock
```

Conceptually:

```yaml
# ocp.yaml
skills:
  - https://github.com/example/debugging
```

```text
# ocp.lock
debugging → commit abc123
```

Normal synchronization always resolves the commit recorded in the lockfile.

`ocp sync` never advances dependency versions.

Only:

```bash
ocp upgrade
```

may update eligible Git-backed skills and rewrite the lockfile.

Explicitly pinned refs are skipped by normal upgrades unless the user explicitly changes the pin.

---

## 9. Plugins

OCP manages plugin declarations only.

Example:

```yaml
plugins:
  - opencode-wakatime
  - some-plugin@1.2.0
```

OCP renders them into native OpenCode configuration.

Repository-local plugin files live under `plugins/` and use paths relative to that folder (for example, `harness-bridge.ts`). Legacy `./plugins/...` declarations remain readable. Local plugin paths outside `plugins/` are rejected.

OpenCode/Bun remains responsible for:

- plugin fetching
- installation
- package/runtime dependency management

For MVP:

```bash
ocp upgrade
```

does not manage plugin versions.

### 9.1 Plugin merge rules

Plugins are additive.

Exact duplicates are silently deduplicated.

If the same package identity appears with different versions, the more specific declaration wins:

```text
root < parent < child
```

---

## 10. Agents

Agents may be represented by source files:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md

  reviewer:
    file: ./agents/reviewer.md
```

Profiles may override selected agent properties without duplicating the underlying file.

Example:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md

profiles:
  lean:
    agents:
      implementer:
        config:
          model: openrouter/model-a

  deep:
    agents:
      implementer:
        config:
          model: openrouter/model-b
```

Exact agent override syntax should map cleanly onto the OpenCode agent fields OCP renders.

A child profile overrides inherited values for the same named agent.

---

## 11. Profiles

Profiles are optional.

If `profiles:` is absent, OCP creates one implicit profile:

```text
default
```

Root configuration applies automatically to all profiles.

Example:

```yaml
skills:
  - common

plugins:
  - foo

profiles:
  lean:
    skills:
      - fast

  deep:
    skills:
      - research
```

Resolved result:

```text
lean:
  common
  fast
  foo

deep:
  common
  research
  foo
```

### 11.1 Profile inheritance

A profile may inherit from exactly one other profile:

```yaml
profiles:
  default:
    config:
      model: openrouter/model-a

  deep:
    extends: default
    config:
      model: openrouter/model-b
```

Multiple inheritance is not supported.

Resolution order:

```text
root
  ↓
parent profile
  ↓
child profile
```

### 11.2 Merge behavior

Scalars:

```text
most specific value wins
```

Maps/objects:

```text
deep merge where possible
```

Named objects such as agents:

```text
merge by name, then child overrides inherited fields
```

Lists representing dependencies such as skills/plugins:

```text
additive + deduplicated by logical identity
```

### 11.3 Removal semantics

Removing inherited items is not part of MVP.

MVP inheritance is additive.

A compact removal syntax may be added later if real usage demonstrates the need.

---

## 12. Generated Environments

OCP renders each resolved profile into a complete OpenCode-compatible environment.

Conceptually:

```text
OCP source
   ↓
resolver
   ↓
generated/
├── default/
├── lean/
└── deep/
```

Generated output may contain:

```text
opencode.json
AGENTS.md
agent/
skills/
package.json
...
```

Generated output is disposable.

OCP must be able to recreate it from:

```text
ocp.yaml
+ ocp.lock
+ repository-managed source files
+ repository-local skills and plugins
```

---

## 13. Generated-File Ownership and Drift

OCP should track which files it generated.

Each render should record a small internal manifest containing generated file paths and checksums.

Conceptually:

```text
profile: deep

generated:
  opencode.json → hash
  AGENTS.md → hash
  agent/implementer.md → hash
  skills/foo/SKILL.md → hash
```

Drift means:

> an OCP-owned generated file differs from the version OCP last rendered.

Files OCP did not generate are not considered drift.

This avoids maintaining an arbitrary runtime ignore list.

Examples:

```text
opencode.json        → OCP-owned → drift checked
agent/foo.md         → OCP-owned → drift checked
node_modules/...     → not OCP-owned → ignored
OpenCode cache       → not OCP-owned → ignored
```

Before overwriting drift:

```text
Profile "deep" contains local modifications:

  agent/implementer.md
  opencode.json

Sync will overwrite these generated changes.

Continue? [y/N]
```

Interactive mode permits cancellation.

Non-interactive mode fails unless explicitly forced.

Automatic backup/restore is not required for MVP.

---

## 14. Synchronization

### `ocp sync`

Core promise:

> Make this machine current and ready to work.

Normal flow:

```text
check canonical repository
        ↓
auto-commit local canonical changes if any
        ↓
fetch remote
        ↓
safe Git reconciliation
        ↓
push if local branch is ahead
        ↓
resolve locked Git-backed skills
        ↓
render profiles
        ↓
restore active profile
```

### 14.1 Automatic commits

OCP automatically commits canonical source changes by default.

Users may opt out and manage commits manually.

### 14.2 Automatic merging

If Git can safely merge local and remote changes, OCP proceeds automatically.

If Git reports a real conflict, OCP stops and clearly explains that user resolution is required.

OCP never silently chooses local or remote canonical state during a conflict.

### 14.3 Push behavior

OCP pushes only when the reconciled local branch contains commits not present on the configured remote branch.

No local commits to publish means no push.

Automatic push is considered normal `ocp sync` behavior.

During setup, OCP should clearly communicate that normal sync may:

- create commits;
- merge remote changes;
- push local canonical changes.

Manual/no-auto-commit workflows remain available.

### 14.4 Active profile

If the active profile still exists after synchronization, it remains active.

If it no longer exists:

```text
Active profile "deep" no longer exists.
Switched to "default".
```

OCP automatically falls back to `default`.

This fallback must be explicitly communicated.

### 14.5 Dependencies

`ocp sync` reproduces versions recorded in `ocp.lock`.

It never upgrades Git-backed skills.

---

## 15. Upgrade

### `ocp upgrade`

Upgrade intentionally advances eligible Git-backed skill versions.

For MVP:

```bash
ocp upgrade
```

upgrades all eligible unpinned Git-backed skills.

Targeted form:

```bash
ocp upgrade skill frontend-design
```

upgrades one eligible skill.

Explicitly pinned refs are not advanced automatically.

Successful upgrades:

1. update `ocp.lock`;
2. regenerate affected profiles.

Plugins are outside OCP upgrade management in MVP.

---

## 16. Profile Activation

OCP replaces OPM's profile-management role.

Users should not need OPM alongside OCP.

### 16.1 Persistent activation

```bash
ocp use deep
```

Makes `deep` active until changed.

### 16.2 One-off execution

```bash
ocp run lean
```

Runs OpenCode using `lean` for that invocation/session without changing the persistent active profile.

Both are required for MVP.

---

## 17. CLI Design

The CLI has two layers.

### 17.1 Atomic commands

Suitable for:

- scripts
- automation
- experienced users

Examples:

```text
ocp sync
ocp apply
ocp use
ocp run
ocp import
ocp upgrade
ocp status
ocp list
ocp reset
```

### 17.2 Guided interactive workflows

Suitable for:

- first use
- onboarding
- configuration discovery
- users who do not know all required arguments

Primary entry point:

```bash
ocp setup
```

The interaction model should resemble the approachable guided CLI style of Hermes, but with far fewer commands and substantially less hierarchy.

Interactive flows should orchestrate the same underlying operations as non-interactive commands.

There should not be separate implementations for interactive and scripted behavior.

### 17.3 Intentionally small root command surface

The root command surface should remain intentionally small.

MVP root commands are:

```text
ocp setup
ocp sync
ocp apply
ocp use <profile>
ocp run <profile> [args...]
ocp list
ocp status
ocp import [...]
ocp upgrade [skill <name>]
ocp reset
```

OCP should not add separate commands such as:

```text
create
delete
clone
edit
repo
```

unless future real-world usage demonstrates a clear need.

Prefer guided `ocp setup`, direct configuration editing and the existing atomic commands over expanding the CLI hierarchy.

---

## 18. Initial Setup

### `ocp setup`

First-run onboarding should support:

1. creating a new OCP configuration;
2. importing an existing setup;
3. optionally configuring one canonical Git repository;
4. testing Git availability;
5. testing repository access/authentication;
6. generating the initial environment;
7. selecting the active profile.

Git authentication itself is out of scope.

Example:

```text
✓ Git installed
✓ Repository reachable
✓ Authentication works
✓ Configuration generated
```

Failure:

```text
✗ Cannot access git@github.com:user/opencode-config.git

Configure Git/SSH authentication and retry.
```

---

## 19. Import

Basic best-effort import is part of MVP.

Supported sources:

- default OpenCode configuration
- OPM profile
- custom filesystem path

Interactive example:

```text
Import existing configuration from:

  1. OpenCode
  2. OPM
  3. Custom path
```

Import principle:

> Import what OCP understands, warn about what it does not understand, never guess.

Unsupported or ambiguous content should be:

- preserved when safe and straightforward;
- otherwise clearly reported.

OCP must not silently produce semantically incorrect configuration.

---

## 20. Apply

### `ocp apply`

Regenerates local OpenCode environments from current canonical configuration without performing Git synchronization.

Useful after:

- manually editing `ocp.yaml`;
- editing repository-managed agent/skill files;
- debugging rendering;
- working without a Git repository.

`ocp sync` normally performs the equivalent of `apply` automatically.

## 20.1 Reset

### `ocp reset`

Detaches OCP from OpenCode and restores a normal non-OCP-managed OpenCode state.

Reset must not delete:

- the canonical OCP repository;
- OCP configuration;
- skills;
- other user source data.

After reset, OCP must clearly tell the user where the remaining OCP data lives and that they may manually delete that directory for complete removal.

If reset would overwrite or replace existing user-owned OpenCode configuration, OCP must follow the destructive-operation safety policy:

1. clearly explain what will be overwritten or replaced;
2. allow interactive users to cancel;
3. fail in non-interactive mode unless an explicit force option is supplied.

---

## 21. Git Repository Behavior

Git synchronization is optional.

Without Git:

```text
ocp.yaml
   ↓
ocp apply
   ↓
OpenCode environment
```

With Git:

```text
Git repository
   ↓
ocp sync
   ↓
OpenCode environment
```

MVP supports exactly one canonical Git repository per OCP installation.

Git-backed skills may independently come from other repositories.

Private canonical repositories and private Git-backed skills work when the machine's normal Git authentication already permits access.

OCP must not manage:

- SSH keys
- access tokens
- `.env`
- API keys
- provider credentials

---

## 22. Secrets

Secret management is explicitly out of scope.

OCP delivers configuration to OpenCode.

Users remain responsible for:

- environment variables
- `.env`
- API credentials
- Git credentials
- provider authentication
- OpenCode-native secret behavior

OCP should avoid encouraging secrets to be committed into the canonical repository.

---

## 23. Machine-Specific Configuration

Each installation has an explicit machine name, captured during `ocp setup`
(defaulting to the hostname, overridable with `--machine`) and stored in local
state. The name drives machine-aware rendering from one shared source.

Profiles support two optional keys:

```yaml
profiles:
  hybrid:
    config:
      model: openai/gpt-6-sol
    machines:
      windy:
        config:
          provider:
            ollama:
              options:
                baseURL: http://127.0.0.1:11434/v1
  windy-dev:
    hosts: [windy]
```

- `hosts:` restricts profile availability. A profile without `hosts` renders
  everywhere; with `hosts` it renders only on listed machines and is hidden
  from `list`, `use`, and `run` elsewhere. Extending an unavailable profile
  skips the child, unless the child contradicts with its own `hosts` entry.
- `machines:` holds per-machine overlays merged over the shared base
  (deep merge for config, additive for skills/plugins/instructions/agents).
  Overlays never nest `hosts`, `machines`, or `extends`.
- When the machine is named `windy`, OCP automatically appends
  `hosts/windy.md` to each rendered profile's `AGENTS.md` when the file
  exists. No declaration is required.

Machine-local differences use these named-machine rules rather than external
local paths. `ocp sync` labels automatic commits with the machine name
(`sync from windy`).

---

## 24. Terminal UX

OCP should use restrained, coordinated terminal coloring from the beginning.

Color should communicate meaning, not decorate output.

Suggested semantic categories:

```text
success
warning
error
muted/context
important change
```

Normal successful operations should remain visually quiet.

Example:

```text
Syncing configuration...
✓ Repository current
✓ 4 profiles rendered
✓ Active profile: deep

Warning: 1 local skill path unavailable
```

Color-system refinement is iterative and not an MVP blocker.

---

## 25. Safety Requirements

OCP must require confirmation before unexpected destructive operations such as:

- overwriting drifted generated files;
- replacing an imported environment;
- deleting profiles/configuration;
- resetting local generated state containing unexpected edits.

Non-interactive execution must fail safely unless the user supplies an explicit destructive override such as:

```text
--force
```

Routine reproducible regeneration with no detected drift should not prompt.

Normal auto-commit, safe merge and push behavior during `ocp sync` is not considered destructive and does not require confirmation.

---

## 26. MVP Commands

Exact naming may evolve during implementation, but the MVP command surface should remain small.

```text
ocp setup
ocp sync
ocp apply

ocp use <profile>
ocp run <profile> [args...]

ocp list
ocp status

ocp import [...]
ocp upgrade [skill <name>]
ocp reset
```

This is the intended MVP root command surface.

Avoid a large or deep command hierarchy.

---

## 27. MVP Non-Goals

OCP MVP does not:

- manage secrets
- manage Git authentication
- replace npm/Bun dependency management
- manage plugin upgrades
- automatically reconcile manual generated edits back into canonical configuration
- support multiple profile inheritance
- support inherited-item removal syntax
- support formal machine-specific overlays
- provide automatic backups
- provide a full GUI
- act as a general dotfile manager
- provide native Windows support
- attempt to independently model every OpenCode schema field
- support arbitrary generic file materialization
- provide complex uninstall workflows
- automatically delete canonical OCP data during reset
- provide a large or deep command hierarchy

---

## 28. Roadmap

### 28.1 Automatic backups and restore

Before destructive regeneration, optionally snapshot affected generated state.

Retention must be bounded to avoid unbounded disk usage.

Potential controls:

```text
maximum backup count
maximum backup age
manual restore
automatic cleanup
```

### 28.2 Machine-specific overlays

Allow canonical configuration to intentionally vary by machine while remaining declarative and synced.

### 28.3 Doctor

Add:

```bash
ocp doctor
```

Potential checks:

- OpenCode installation/version
- OCP repository health
- Git authentication/access
- malformed configuration
- missing repository-local skills or plugins
- unavailable skill repositories
- lockfile inconsistencies
- generated drift
- active-profile validity
- plugin declarations
- stale/broken generated environments

`doctor` should diagnose and explain rather than mutate state unexpectedly.

### 28.4 Improved terminal design

Iteratively refine:

- semantic colors
- status layout
- warnings
- diffs
- progress presentation
- consistency across commands

Maintain a minimal aesthetic.

### 28.5 Inherited-item removal

If real usage requires it, add concise syntax for excluding root/parent items from child profiles.

### 28.6 Advanced machine/local state

Potential future support for:

- named machines
- machine overlays
- richer local-only declarations
- promoting local configuration into shared canonical configuration

### 28.7 Windows support

Add and test native Windows path handling, environment generation and activation semantics after the Linux/macOS model is stable.

---

## 29. Success Criteria

The MVP succeeds if a user can:

1. take an existing OpenCode/OPM setup and obtain a usable starter OCP configuration;
2. represent a simple setup with very little YAML;
3. create related profiles without duplicating shared agents, skills, plugins and instructions;
4. switch profiles without OPM;
5. clone/setup OCP on another authenticated Linux/macOS machine;
6. run:

```bash
ocp sync
```

and receive the current reproducible OpenCode environment;
7. safely detect generated-file drift;
8. use Git-backed skills reproducibly across machines;
9. understand every warning, automatic merge, fallback and meaningful action OCP performs.

The key experiential test is:

> **SSH into a machine, run `ocp sync`, then start working.**

If that feels reliable and boring, OCP is doing its job.
