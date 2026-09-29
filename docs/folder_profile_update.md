Update OCP’s source-configuration architecture so the canonical user-owned OCP directory is easy to navigate, scales cleanly to several profiles, and maps predictably into OpenCode’s native configuration structure.

## Goal

The OCP source directory should separate:

- global/shared configuration
- profile-specific configuration
- reusable agent definitions
- repository-managed skills
- repository-managed plugins
- dependency lock state

The structure should remain simple enough to understand at a glance and should optimize for the common case of maintaining several related OpenCode profiles.

The key model is:

```text
OCP source
    ↓
resolve global + selected profile
    ↓
render OpenCode-native environment
```

OCP’s source structure is an authoring structure.

OpenCode’s structure is the generated runtime target.

---

## Canonical OCP Source Layout

Use this as the primary source layout:

```text
~/.config/ocp/
├── ocp.yaml
├── ocp.lock
├── profiles/
│   ├── default.yaml
│   ├── lean.yaml
│   ├── deep.yaml
│   ├── openai.yaml
│   └── openrouter.yaml
├── agents/
│   ├── implementer.md
│   ├── reviewer.md
│   ├── planner.md
│   └── claude-reviewer.md
├── skills/
│   ├── my-local-skill/
│   │   ├── SKILL.md
│   │   └── ...
│   └── another-skill/
│       └── SKILL.md
└── plugins/
    └── my-local-plugin.ts
```

Each top-level item has one clear responsibility.

### `ocp.yaml`

Contains global/shared configuration that applies to every profile.

Typical contents:

- shared OpenCode config
- shared skills
- shared plugins
- shared agents
- shared instructions

Example:

```yaml
version: 1

config:
  permission:
    bash:
      "*": ask
      "git status*": allow
      "git diff*": allow

skills:
  - my-local-skill
  - https://github.com/example/shared-skill

plugins:
  - opencode-wakatime

agents:
  implementer:
    file: ./agents/implementer.md

  reviewer:
    file: ./agents/reviewer.md
```

Root-level configuration is inherited by every profile.

---

## Profile Files

Profiles live in:

```text
~/.config/ocp/profiles/
```

Each `*.yaml` or `*.yml` file represents one profile.

The filename without its YAML extension is the profile name. Defining both
`deep.yaml` and `deep.yml` is an error.

Examples:

```text
profiles/default.yaml
→ profile: default

profiles/deep.yaml
→ profile: deep

profiles/openrouter.yaml
→ profile: openrouter
```

Profiles are discovered automatically from this directory.

This removes the need to maintain a separate profile index in `ocp.yaml`.

### Example profile

```yaml
extends: default

config:
  model: openrouter/provider/model

agents:
  implementer:
    model: openrouter/provider/strong-coding-model

  reviewer:
    model: anthropic/provider/reviewer-model

skills:
  - https://github.com/example/deep-research-skill
```

A profile contains only the configuration that differs from or adds to the shared root configuration.

---

## Single-Profile Behavior

OCP should remain useful for users who do not need multiple profiles.

If there is no `profiles/` directory, or there are no profile files, `ocp.yaml` represents one implicit profile named:

```text
default
```

Example minimal setup:

```text
~/.config/ocp/
└── ocp.yaml
```

```yaml
version: 1

config:
  model: openrouter/provider/model
```

This should render and behave as a normal single-profile OCP installation.

The multi-profile directory structure should therefore be an additive scaling mechanism rather than a requirement for basic use.

---

## Profile Inheritance

A profile may inherit from one other profile:

```yaml
extends: default
```

Resolution order remains:

```text
global ocp.yaml
    ↓
parent profile
    ↓
selected profile
```

Root configuration always applies first.

Parent profile configuration applies next.

Child profile configuration is the most specific layer.

This maintains the existing additive configuration model.

---

## Agents

Use one flat reusable source directory:

```text
~/.config/ocp/agents/
```

Example:

```text
agents/
├── implementer.md
├── reviewer.md
├── planner.md
├── lean-reviewer.md
└── deep-planner.md
```

The directory is a pool of source files.

Agent identity comes from the YAML mapping rather than from the source filename.

Example:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md
```

Here:

```text
implementer
```

is the logical OpenCode agent name.

```text
./agents/implementer.md
```

is its reusable source file.

The same source file may be used by several profiles.

---

## Shared Agents with Profile-Specific Models

A major use case is sharing agent behavior while varying the model by profile.

Example global definition:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md

  reviewer:
    file: ./agents/reviewer.md
```

Then:

```yaml
# profiles/lean.yaml

agents:
  implementer:
    config:
      model: openrouter/provider/fast-model

  reviewer:
    config:
      model: openrouter/provider/cheap-reviewer
```

and:

```yaml
# profiles/deep.yaml

agents:
  implementer:
    config:
      model: openrouter/provider/frontier-coding-model

  reviewer:
    config:
      model: anthropic/provider/frontier-reviewer
```

Both profiles reuse:

```text
agents/implementer.md
agents/reviewer.md
```

while supplying different models.

This should be a normal and well-supported OCP pattern.

### Agent source best practice

Treat the Markdown agent file as the reusable definition of:

- instructions
- role
- behavioral guidance
- stable permissions/settings appropriate to the agent itself

Use profile configuration for profile-dependent properties such as model selection when appropriate.

A fixed model may still be represented in an agent definition when that is intentionally part of the agent’s behavior.

---

## Profile-Specific Agent Variants

When a profile genuinely needs different agent behavior, it can reference another source file from the same flat directory.

Example:

```text
agents/
├── implementer.md
├── reviewer.md
└── deep-reviewer.md
```

```yaml
# profiles/deep.yaml

agents:
  reviewer:
    file: ./agents/deep-reviewer.md
    config:
      model: anthropic/provider/model
```

Filename conventions are for human organization.

The YAML mapping remains authoritative.

A useful naming convention for profile-specific source variants is:

```text
<scope>-<agent>.md
```

Examples:

```text
deep-reviewer.md
lean-implementer.md
claude-planner.md
```

Shared agents can retain simple names:

```text
implementer.md
reviewer.md
planner.md
```

---

## Skills

Repository-managed local skills remain under:

```text
~/.config/ocp/skills/
```

Example:

```text
skills/
└── browser-testing/
    ├── SKILL.md
    ├── scripts/
    └── references/
```

They can be referenced from global config or profile config:

```yaml
skills:
  - browser-testing
```

Git-backed skills continue to be declared in YAML:

```yaml
skills:
  - https://github.com/example/skill
```

Profile files may add additional skills:

```yaml
skills:
  - https://github.com/example/deep-research
```

Existing inheritance, deduplication and lockfile behavior remains applicable.

---

## Plugins

Repository-managed local plugin files remain under:

```text
~/.config/ocp/plugins/
```

Example:

```text
plugins/
└── local-hook.ts
```

Package plugin declarations continue to live in YAML:

```yaml
plugins:
  - opencode-wakatime
```

Profile files may add or override plugin declarations according to the existing resolver rules.

---

## Relationship to OpenCode

OCP should continue rendering the resolved profile into the OpenCode-native structure expected by OpenCode.

Conceptually:

```text
~/.config/ocp/
├── ocp.yaml
├── profiles/
├── agents/
├── skills/
└── plugins/
        ↓
     resolve
        ↓
generated OCP profile
        ↓
OpenCode-compatible structure
```

The generated OpenCode environment may contain:

```text
opencode.json
AGENTS.md
agents/
skills/
plugins/
package.json
...
```

The OCP `profiles/` directory is an authoring concept and participates in resolution.

The resulting selected profile is materialized as a normal OpenCode environment.

Agents, skills and plugins should continue to be rendered using OpenCode’s corresponding native structures wherever applicable.

---

## Profile Discovery

At load time, OCP should inspect:

```text
<canonical-config>/profiles/*.yaml
```

Each regular YAML file becomes one profile.

For example:

```text
profiles/
├── default.yaml
├── lean.yaml
└── deep.yaml
```

produces:

```text
default
lean
deep
```

This discovery should feed existing commands such as:

```text
ocp list
ocp use <profile>
ocp run <profile>
ocp status
ocp sync
ocp apply
```

Profile discovery should be deterministic and produce a stable ordering for presentation, such as alphabetical ordering.

---

## Import Behavior

When importing multiple OPM profiles, generate one profile YAML file per imported profile.

Example input:

```text
OPM:
  default
  lean
  deep
```

should produce conceptually:

```text
~/.config/ocp/
├── ocp.yaml
└── profiles/
    ├── default.yaml
    ├── lean.yaml
    └── deep.yaml
```

Shared content discovered during import may be promoted into `ocp.yaml` or reusable source files where the existing importer can confidently identify it.

Profile-specific content should remain in the corresponding profile YAML.

The existing best-effort import principle remains:

> Import what is understood, communicate ambiguity clearly, and preserve a usable result.

---

## Setup Behavior

`ocp setup` should create the canonical structure appropriate to the selected workflow.

For a simple new setup, the smallest useful result may remain:

```text
~/.config/ocp/
└── ocp.yaml
```

When multiple profiles are created or imported, add:

```text
profiles/
```

and populate it with one YAML file per profile.

Agent, skill and plugin directories should be created when they are needed by the resulting configuration.

This keeps first-run output proportional to the actual configuration.

---

## Existing Configuration Migration

Add migration support for the current single-file multi-profile format.

If an existing `ocp.yaml` contains inline profiles such as:

```yaml
profiles:
  default:
    ...

  lean:
    ...

  deep:
    ...
```

OCP should be able to convert these into:

```text
profiles/default.yaml
profiles/lean.yaml
profiles/deep.yaml
```

while preserving root/shared configuration in:

```text
ocp.yaml
```

The migration should preserve behavior and provide the user with a concise summary of the changes.

Follow the existing destructive-operation safety policy whenever migration would replace or overwrite user-owned source files.

This migration may be implemented through setup/import/migration logic in whichever way fits the current CLI architecture most naturally.

---

## Validation

OCP should validate the source structure when loading or rendering.

Useful validation includes:

- profile YAML parses successfully
- `extends` refers to an available profile
- profile inheritance does not form a cycle
- referenced agent files exist
- referenced repository-local skills exist
- source files required for rendering are readable
- resolved agent names are valid for the generated OpenCode profile
- dependency collisions continue to use the existing resolver rules

Errors should identify the relevant profile and source file.

Example:

```text
Profile "deep" references a missing agent file:

  ./agents/deep-reviewer.md
```

Warnings and errors should follow OCP’s existing concise terminal UX conventions.

---

## Rendering and Drift

Profile splitting changes source organization, while the generated-output ownership model remains the same.

OCP continues to:

- resolve the selected profile
- render complete OpenCode-compatible environments
- track generated files/checksums
- detect drift in OCP-owned generated files
- apply the existing destructive-overwrite safety behavior

Profile YAML files and reusable agent/skill/plugin files remain canonical user-owned source.

Generated profile directories remain disposable output.

---

## Git and Sync

The entire canonical source tree remains the unit synchronized through Git:

```text
~/.config/ocp/
├── ocp.yaml
├── ocp.lock
├── profiles/
├── agents/
├── skills/
└── plugins/
```

This structure should work identically whether the canonical directory is:

- local-only
- a Git repository used across multiple machines

`ocp sync` should continue synchronizing the canonical repository and then rendering the current local environment.

Splitting profiles into separate YAML files should naturally improve Git diffs and reduce unrelated edits to one large configuration file.

---

## Design Guidelines

Prefer convention when it removes repetitive bookkeeping and remains obvious from the filesystem.

Keep configuration explicit where the relationship between two objects carries semantic meaning.

Keep source-file paths and logical OpenCode identities separate.

Favor reusable agent definitions with profile-specific configuration layered on top.

Create directories and files proportionally to what the user actually needs.

Keep the single-profile experience extremely small.

Keep the multi-profile experience easy to navigate by giving each profile one clearly named YAML file.

Preserve the existing OCP resolver, sync, locking, rendering and safety concepts wherever this source-layout change does not require different behavior.

## Target Mental Model

The resulting system should be explainable as:

```text
ocp.yaml
    shared settings

profiles/
    one YAML file per profile

agents/
    reusable agent source files

skills/
    reusable local skills

plugins/
    reusable local plugins

ocp.lock
    locked Git skill versions
```

Then:

```text
shared config + selected profile
            ↓
         resolve
            ↓
     OpenCode environment
```

The main usability goal is that a user with five or more profiles can open `~/.config/ocp`, immediately find the profile they want, edit one small YAML file, and continue working without navigating a large monolithic configuration.
