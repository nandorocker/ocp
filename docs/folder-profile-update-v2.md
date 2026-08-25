# OCP — Source Layout and Profile File Structure PRD

## 1. Summary

OCP organizes user-owned configuration around a small, predictable source tree under:

```text
~/.config/ocp/
```

The design is:

* `ocp.yaml` contains shared/global configuration
* each profile lives in its own YAML file under `profiles/`
* profiles are auto-discovered from filenames
* agents live in one flat reusable `agents/` directory
* agent files define reusable agent archetypes
* profiles normally assign models to those archetypes
* skills and plugins keep their own source directories
* OCP renders the resolved configuration into OpenCode's native structure

The goal is to keep multi-profile setups readable while maximizing reuse and keeping the schema small.

---

## 2. Design Principle

OCP source layout is an **authoring structure**.

OpenCode's directory layout is the **rendered runtime structure**.

Conceptually:

```text
OCP source
    ↓
shared configuration
+ selected profile
+ reusable agent archetypes
    ↓
resolve
    ↓
OpenCode-compatible environment
```

OCP's source tree does not need to mirror OpenCode exactly.

---

## 3. Canonical OCP Source Layout

Default layout:

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
│   ├── researcher.md
│   └── explorer.md
├── skills/
│   ├── my-skill/
│   │   └── SKILL.md
│   └── another-skill/
│       └── SKILL.md
└── plugins/
    └── my-plugin.ts
```

Not every directory is required.

A minimal installation may contain only:

```text
~/.config/ocp/
└── ocp.yaml
```

---

## 4. `ocp.yaml`

`ocp.yaml` contains configuration shared across all profiles.

Example:

```yaml
version: 1

config:
  permission:
    bash:
      "*": ask
      "git status*": allow

skills:
  - ./skills/common-tools

plugins:
  - opencode-wakatime

agents:
  implementer:
    file: ./agents/implementer.md

  reviewer:
    file: ./agents/reviewer.md

  planner:
    file: ./agents/planner.md
```

Root configuration applies to every profile.

`ocp.yaml` should not contain inline profile definitions in MVP.

---

## 5. Profile Discovery

Profiles live under:

```text
profiles/
```

Each `*.yaml` or `*.yml` file defines exactly one profile.

The filename becomes the profile name.

Example:

```text
profiles/deep.yaml
```

defines profile:

```text
deep
```

No profile registry is required in `ocp.yaml`.

OCP automatically discovers:

```text
profiles/*.yaml
profiles/*.yml
```

If both extensions produce the same profile name, loading fails with a duplicate-profile error. Invalid profile filenames and invalid profile configuration files also fail loading.

---

## 6. Implicit Default Profile

If `profiles/` does not exist or contains no profile files, OCP treats the root configuration as one implicit profile named:

```text
default
```

Therefore:

```text
~/.config/ocp/
└── ocp.yaml
```

is a complete valid OCP setup.

---

## 7. Profile Files

Profile files contain profile-specific additions and overrides.

A primary responsibility of profiles is assigning models to shared agent archetypes.

Example:

```yaml
# profiles/deep.yaml

config:
  model: openrouter/frontier-model

agents:
  planner:
    model: anthropic/strong-reasoning-model

  implementer:
    model: openai/strong-coding-model

  reviewer:
    model: anthropic/strong-review-model

skills:
  - https://github.com/example/deep-research
```

A lean profile may use the exact same agents with cheaper models:

```yaml
# profiles/lean.yaml

config:
  model: openrouter/fast-model

agents:
  planner:
    model: openrouter/cheap-reasoning-model

  implementer:
    model: openrouter/cheap-coding-model

  reviewer:
    model: openrouter/cheap-review-model
```

The underlying Markdown files are reused.

---

## 8. Agent Archetypes

Files under:

```text
agents/
```

represent reusable **agent archetypes**.

For example:

```text
agents/
├── planner.md
├── implementer.md
├── reviewer.md
├── researcher.md
└── explorer.md
```

Each file primarily defines:

* role
* prompt/instructions
* behavior
* permissions
* tool usage
* other agent-specific OpenCode settings

The same archetype should normally be reused across profiles.

Example:

```yaml
# ocp.yaml

agents:
  implementer:
    file: ./agents/implementer.md
```

Every profile may then assign a different model to `implementer`.

This is a core OCP use case.

---

## 9. Agent Identity

The YAML key defines the logical OpenCode agent identity.

Example:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md
```

Logical identity:

```text
implementer
```

Source:

```text
./agents/implementer.md
```

The filename itself is not semantic.

OCP must not discover logical agents based on filenames.

---

## 10. Model Assignment

### 10.1 Normal behavior

Agent Markdown files should normally be model-agnostic.

Profiles assign models to shared agents:

```yaml
agents:
  implementer:
    model: openrouter/model-x

  reviewer:
    model: anthropic/model-y
```

This allows a small set of stable agent archetypes to be combined with different model strategies across profiles.

Example:

```text
implementer.md
    ↓
lean       → Model A
default    → Model B
deep       → Model C
openai     → Model D
```

No agent duplication is required.

### 10.2 Hard-coded agent model

OpenCode agent Markdown may itself contain a model.

OCP should allow this.

A model defined directly in the agent source file is treated as an intentional hard constraint.

It overrides profile-level model assignment for that agent.

Conceptual precedence:

```text
profile model assignment
        ↓
agent Markdown model, if present
        ↓
final rendered model
```

If the agent source contains no model, the profile assignment is used.

### 10.3 Best practice

Recommended practice:

> Keep shared agent archetypes model-agnostic and assign models in profiles.

Hard-code a model in an agent Markdown file only when that agent is intentionally tied to a specific model.

OCP should not prevent this, but the behavior must be predictable.

---

## 11. Model Resolution Example

Shared archetype:

```text
agents/implementer.md
```

contains no model.

Profile:

```yaml
# profiles/deep.yaml

agents:
  implementer:
    model: openai/coding-model
```

Resolved OpenCode agent:

```text
implementer
model = openai/coding-model
```

If `implementer.md` itself specifies:

```yaml
model: anthropic/fixed-model
```

then the resolved agent uses:

```text
anthropic/fixed-model
```

regardless of the profile assignment.

OCP may surface this clearly in status/debug output if useful:

```text
implementer: anthropic/fixed-model
  model fixed by agent source
```

Do not add interactive conflict resolution for this in MVP.

---

## 12. Agent Reuse

Profiles should not duplicate agent files merely to change models.

Preferred:

```text
agents/
└── implementer.md
```

with:

```yaml
# profiles/lean.yaml
agents:
  implementer:
    model: model-a
```

and:

```yaml
# profiles/deep.yaml
agents:
  implementer:
    model: model-b
```

Avoid:

```text
agents/
├── lean-implementer.md
└── deep-implementer.md
```

when the only difference is the model.

Separate files remain appropriate when the agent's actual behavior or instructions differ.

---

## 13. Agent Best Practices

Recommended conventions:

* maintain a small set of reusable archetypes
* keep prompts and behavior in Markdown files
* keep model selection in profile YAML
* hard-code a model in Markdown only intentionally
* reuse the same agent source across profiles whenever behavior is shared
* create a new agent file only when behavior materially differs
* keep `agents/` flat for MVP
* filenames remain organizational only
* YAML remains authoritative

---

## 14. Profile Inheritance

A profile may inherit from one other profile:

```yaml
extends: default
```

Resolution order:

```text
root ocp.yaml
    ↓
parent profile
    ↓
current profile
```

Multiple inheritance is not supported.

Circular inheritance must fail clearly.

Profile model assignments follow normal profile inheritance.

Example:

```yaml
# profiles/default.yaml

agents:
  implementer:
    model: model-a
```

```yaml
# profiles/deep.yaml

extends: default

agents:
  implementer:
    model: model-b
```

`deep` uses `model-b`, unless the underlying agent Markdown fixes its own model.

---

## 15. Merge Behavior

Scalars:

```text
most specific profile value wins
```

Maps:

```text
deep merge
```

Named agents:

```text
merge by logical agent name
```

Skills/plugins:

```text
additive + deduplicated by logical identity
```

Agent source-level model declarations remain authoritative over profile model assignments.

---

## 16. Skills

Repository-managed local skills live under:

```text
skills/
```

Example:

```text
skills/
└── browser-debugging/
    ├── SKILL.md
    └── scripts/
```

Reference:

```yaml
skills:
  - ./skills/browser-debugging
```

Existing Git-backed and experimental external-path behavior remains unchanged.

---

## 17. Plugins

Local plugin source may live under:

```text
plugins/
```

Package plugins remain ordinary YAML declarations and are handled by OpenCode/Bun.

No additional plugin organization system is required.

---

## 18. Lockfile

Git-backed skill resolution remains stored in:

```text
ocp.lock
```

at the canonical source root.

Profile splitting does not change lockfile behavior.

---

## 19. Relationship to OpenCode

OCP source:

```text
~/.config/ocp/
├── ocp.yaml
├── profiles/
├── agents/
├── skills/
└── plugins/
```

OpenCode receives a resolved native environment:

```text
opencode.json
AGENTS.md
agents/
skills/
plugins/
...
```

The OCP-only:

```text
profiles/
```

directory is never rendered into OpenCode.

Conceptually:

```text
shared OCP config
+ deep.yaml
+ reusable agent archetypes
        ↓
resolve models and overrides
        ↓
render
        ↓
OpenCode-native configuration
```

---

## 20. Rendering

When rendering `deep`, OCP should:

1. load `ocp.yaml`
2. discover `profiles/deep.yaml`
3. resolve any parent profile
4. merge root, parent and profile configuration
5. resolve referenced agents
6. assign profile-level agent models
7. preserve any agent-source model that explicitly overrides profile assignment
8. resolve skills and plugins
9. render the resulting OpenCode environment

Profile source layout itself must not leak into generated OpenCode paths.

---

## 21. Validation

OCP should validate:

* referenced agent files exist
* profile inheritance targets exist
* profile inheritance is not circular
* local skill paths exist where required
* profile filenames produce unique profile names
* incompatible generated destinations do not collide

Missing model assignments are not automatically errors if OpenCode can validly resolve the agent without one.

OCP should avoid inventing additional model requirements beyond OpenCode's own requirements.

---

## 22. Import Behavior

OpenCode/OPM imports should generate the new layout.

Multiple imported profiles become:

```text
profiles/
├── default.yaml
├── deep.yaml
└── openrouter.yaml
```

Agent definitions should be deduplicated when this is safe.

If multiple imported profiles contain the same agent behavior but different models:

* create one shared agent archetype
* remove model differences from the shared source when safely possible
* place each model assignment in the relevant profile YAML

Example result:

```text
agents/
└── implementer.md
```

```yaml
# profiles/default.yaml
agents:
  implementer:
    model: model-a
```

```yaml
# profiles/deep.yaml
agents:
  implementer:
    model: model-b
```

If OCP cannot safely determine equivalence, preserve separate agent files rather than guessing.

Import remains best-effort.

---

## 23. Setup Behavior

`ocp setup` creates only directories needed by the resulting configuration.

Single-profile:

```text
~/.config/ocp/
└── ocp.yaml
```

Multi-profile:

```text
~/.config/ocp/
├── ocp.yaml
├── profiles/
└── agents/
```

Empty directories should not be created unnecessarily.

---

## 24. UX Mental Model

The structure should be explainable as:

```text
ocp.yaml
  things shared everywhere

profiles/
  model/config combinations

agents/
  reusable agent archetypes

skills/
  local skills

plugins/
  local plugins
```

The especially important relationship is:

```text
Agent = what it does
Profile = which models/settings it uses
```

Profiles should generally select models.

Agents should generally remain reusable.

---

## 25. MVP Scope

Required:

* shared root `ocp.yaml`
* automatic `profiles/*.yaml` discovery
* filename → profile name
* implicit default profile
* one-parent inheritance
* flat `agents/`
* reusable agent archetypes
* profile-level model assignment
* model-agnostic agents as the recommended pattern
* support for explicit model declarations inside agent Markdown
* agent-source model overriding profile assignment
* shared agent reuse across profiles
* existing skills/plugins behavior
* OpenCode-native rendering
* import into the new layout

Not required:

* nested profile directories
* profile registry in `ocp.yaml`
* inline profiles
* automatic agent discovery
* agent namespaces
* multiple inheritance
* complicated model conflict resolution
* GUI management

---

## 26. Migration From Current Layout

If existing OCP configuration contains inline profiles:

* detect them
* convert each to `profiles/<name>.yaml`
* retain shared configuration in `ocp.yaml`
* preserve behavior

Migration is explicit through `ocp setup --migrate-profiles`; ordinary `apply` and `sync` do not rewrite the canonical source.

Where profiles duplicate otherwise identical agents solely because their models differ, migration may consolidate them into one shared archetype when equivalence is clear.

Do not perform aggressive prompt/agent deduplication if equivalence is uncertain.

Do not silently rewrite user configuration outside an explicit migration/setup/import operation.

---

## 27. Success Criteria

This change succeeds if:

1. five profiles no longer produce one enormous `ocp.yaml`
2. adding a profile means adding one YAML file
3. shared configuration remains centralized
4. a small set of shared agent archetypes can serve every profile
5. `implementer.md` can be reused across all profiles
6. each profile can assign a different model to `implementer`
7. changing an agent's prompt changes its behavior everywhere it is reused
8. changing a profile model does not require editing the agent Markdown
9. an intentionally hard-coded agent model remains authoritative
10. rendered output remains OpenCode-native
11. single-profile users retain a minimal setup

The intended mental model is:

> **Agents define roles. Profiles define model combinations. OCP combines them into OpenCode configurations.**
