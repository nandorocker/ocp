# OCP Web UI — MVP Product Requirements Document

## 1. Summary

The OCP Web UI is a lightweight local web interface for visually managing OCP profiles, agents, skills, plugins, and related configuration.

It operates directly on the same canonical OCP files used by the CLI.

Core flow:

```text
OCP source files
      ↓
Web UI
      ↓
existing OCP resolver / renderer
      ↓
OpenCode environment
```

The intended entry point is:

```bash
ocp ui
```

This starts a local web server and opens the OCP interface in the user’s browser.

---

## 2. Goal

The Web UI should make common OCP configuration tasks easier to understand and faster to perform visually.

Primary use cases:

- browse all profiles
- create and edit profiles
- understand profile inheritance
- assign agents to profiles
- reuse shared agents across profiles
- assign different models to the same reusable agent per profile
- manage shared and profile-specific skills
- manage shared and profile-specific plugins
- create and edit agent Markdown files
- edit known agent frontmatter fields through forms
- save and apply configuration
- run OCP sync from the interface

The main usability goal is:

> Managing several related profiles should be easier in the browser than manually navigating multiple YAML files.

---

## 3. Architecture

Canonical OCP files remain the source of truth.

Typical source layout:

```text
~/.config/ocp/
├── ocp.yaml
├── ocp.lock
├── profiles/
│   ├── default.yaml
│   ├── lean.yaml
│   ├── deep.yaml
│   └── openrouter.yaml
├── agents/
│   ├── implementer.md
│   ├── reviewer.md
│   └── planner.md
├── skills/
└── plugins/
```

The UI reads and writes these files directly through OCP’s existing configuration layer.

Changes made manually outside the browser should remain fully compatible with the UI.

The UI server should reuse the same internal OCP logic used by CLI commands for:

- configuration loading
- validation
- profile resolution
- rendering
- apply
- sync
- status

---

## 4. Local Web Server

`ocp ui` starts the local interface.

Expected behavior:

1. locate the canonical OCP configuration
2. load the current OCP state
3. start a local HTTP server
4. select an available local port
5. open the default browser when supported
6. print the URL in the terminal

Example:

```text
OCP UI running at:
http://127.0.0.1:4321
```

The default server binding should use the local loopback interface.

A convenience option may support launching without opening the browser:

```bash
ocp ui --no-open
```

---

## 5. Main Layout

The interface uses:

- a compact top bar
- a persistent left sidebar
- one main content pane

Conceptually:

```text
┌─────────────────────────────────────────────────────────────┐
│ OCP                                          Sync   Status   │
├──────────────┬──────────────────────────────────────────────┤
│ Profiles     │                                              │
│              │                                              │
│ • default    │              Main workspace                  │
│ • lean       │                                              │
│ • deep       │                                              │
│ • openai     │                                              │
│ • openrouter │                                              │
│              │                                              │
│ + New        │                                              │
│──────────────│                                              │
│ Agents       │                                              │
│ Skills       │                                              │
│ Plugins      │                                              │
│ Settings     │                                              │
└──────────────┴──────────────────────────────────────────────┘
```

---

## 6. Top Bar

The top bar contains global OCP actions and state.

Recommended MVP content:

```text
OCP                              Sync     Status
```

Useful status states may include:

- Synced
- Unsaved changes
- Drift detected
- Git conflict
- Apply required

The top bar should remain visually light and focused on operations affecting the whole OCP setup.

---

## 7. Sidebar

The sidebar provides navigation between the main OCP areas.

Primary sections:

```text
Profiles
Agents
Skills
Plugins
Settings
```

Profiles are listed directly beneath the Profiles heading.

Example:

```text
Profiles

default
lean
deep
openai
openrouter

+ New profile

────────────

Agents
Skills
Plugins
Settings
```

Selecting a profile opens that profile in the main workspace.

The selected item should be clearly highlighted.

---

## 8. Default Screen

The application opens directly to the Profiles workspace.

The selected profile should preferably be:

1. the currently active OCP profile, when available
2. otherwise `default`
3. otherwise the first available profile

The page should immediately show useful configuration rather than an intermediary overview screen.

---

## 9. Profiles Workspace

The Profiles workspace is the primary interface for composing OCP profiles.

Selecting a profile displays:

- profile name
- active state
- parent profile
- profile-level OpenCode configuration
- agents
- skills
- plugins

Example:

```text
deep

Active
Extends: default

Model
[ openrouter/provider/model ]

Agents
...

Skills
...

Plugins
...
```

---

## 10. Profile Header

The profile header displays the profile identity and inheritance information.

Example:

```text
deep

Active
Extends: default
```

Useful profile actions may include:

- Duplicate
- Delete

The active profile should have a compact visual indicator.

---

## 11. Profile Creation

The sidebar provides:

```text
+ New profile
```

The creation flow should remain small.

Example:

```text
New profile

Name
[ research ]

Extends
[ default ▾ ]

Create
```

Creating the profile produces:

```text
profiles/research.yaml
```

The new profile then opens in the workspace.

---

## 12. Profile Duplication

Profiles may be duplicated as a convenience for experimentation.

Example:

```text
Duplicate deep

New profile name
[ deep-experimental ]

Create
```

The new profile should copy the explicit source configuration of the original profile while retaining normal inheritance behavior.

---

## 13. Profile-Level Configuration

Common native OpenCode fields should receive simple controls where useful.

Example:

```text
Model

[ openrouter/provider/model ]
```

This writes to:

```yaml
config:
  model: openrouter/provider/model
```

Additional OpenCode configuration may be exposed progressively when a clear UI representation exists.

Underlying YAML structure should remain compatible with OCP’s native configuration model.

---

## 14. Profile Agents

The profile workspace displays the agents available to the selected profile.

The UI should communicate:

- agent name
- source file
- inclusion state
- inherited/profile-specific state
- effective model

Example:

```text
Agents

[x] implementer
    implementer.md
    openrouter/model-a
    inherited

[x] reviewer
    reviewer.md
    anthropic/model-b
    overridden

[ ] researcher
    researcher.md
```

---

## 15. Shared Agents

Reusable agent source files live in:

```text
agents/
```

Example:

```text
agents/
├── implementer.md
├── reviewer.md
└── planner.md
```

A shared agent may be referenced globally:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md
```

Several profiles may reuse the same file.

---

## 16. Profile-Specific Agent Models

A primary OCP workflow is assigning different models to the same reusable agent.

Example shared definition:

```yaml
agents:
  implementer:
    file: ./agents/implementer.md
```

Profile override:

```yaml
agents:
  implementer:
    config:
      model: openrouter/provider/model-a
```

Another profile:

```yaml
agents:
  implementer:
    config:
      model: anthropic/provider/model-b
```

The UI should make this relationship obvious.

Example:

```text
implementer

Source
implementer.md

Model
[ openrouter/provider/model-a ]

Source: inherited
Model: this profile
```

---

## 17. Agent Source Override

A profile may use a different source file for an existing logical agent.

Example:

```text
reviewer

Source
[ deep-reviewer.md ▾ ]

Model
[ anthropic/provider/model ]
```

This supports profile-specific agent behavior while preserving shared agents for normal reuse.

---

## 18. Inheritance States

Inherited configuration should be visually distinguishable from explicit profile configuration.

Use concise labels such as:

```text
inherited
this profile
overridden
```

Definitions:

### inherited

The resolved value originates from shared root configuration or a parent profile.

### this profile

The item is explicitly introduced by the selected profile.

### overridden

The item is inherited while one or more properties are explicitly changed by the selected profile.

Inherited content should use lower visual emphasis than explicit profile changes.

---

## 19. Profile Skills

The profile workspace lists effective skills.

Each item should show:

- name/source
- inclusion state
- origin
- source type when useful

Example:

```text
Skills

[x] frontend-design
    inherited · Git

[x] browser-testing
    inherited · local

[x] deep-research
    this profile · Git
```

---

## 20. Add Skill

The UI should support adding skills to the selected profile.

Git-backed example:

```text
Add skill

Repository
[ https://github.com/example/skill ]

Ref
[ optional ]

Add
```

Repository-local example:

```text
Path
[ ./skills/my-skill ]

Add
```

Declarations should use OCP’s existing skill syntax and resolution logic.

---

## 21. Profile Plugins

The profile workspace lists effective plugins.

Example:

```text
Plugins

[x] opencode-wakatime
    inherited

[x] example-plugin
    this profile
```

Plugin editing manages OCP/OpenCode plugin declarations.

OpenCode continues to handle plugin installation and runtime dependency management.

---

## 22. Agents Library

The Agents section provides a reusable agent-file library.

Example:

```text
Agents

implementer.md
reviewer.md
planner.md
deep-reviewer.md

+ New agent
```

Each row may display usage information:

```text
implementer.md
Used by: default, lean, deep
```

Selecting an agent opens the Agent Editor.

---

## 23. Agent Editor

The Agent Editor works directly with the Markdown file.

It contains:

1. structured frontmatter controls
2. Markdown body editor

Example:

```text
Implementer

File
implementer.md

Frontmatter

Description
[ Implements approved code changes ]

Mode
[ subagent ▾ ]

Model
[ optional ]

Temperature
[ optional ]

Permissions
[ ... ]

Prompt

┌────────────────────────────────────────────────────┐
│ You are the implementation agent.                  │
│                                                    │
│ Follow the approved plan...                        │
│                                                    │
└────────────────────────────────────────────────────┘

Save
```

---

## 24. Frontmatter Editing

Known OpenCode agent frontmatter fields should use appropriate form controls where practical.

Likely fields include:

- description
- mode
- model
- temperature
- permissions
- supported agent metadata

Existing supported frontmatter should be preserved when files are edited and saved.

The form should focus first on fields that are common and straightforward to represent.

---

## 25. Markdown Editor

The agent Markdown body uses a lightweight browser text editor.

MVP requirements:

- multiline editing
- faithful Markdown preservation
- monospace presentation
- normal keyboard editing
- scrolling
- Save support

Syntax highlighting may be added when straightforward.

---

## 26. Create Agent

The Agents page provides:

```text
+ New agent
```

Suggested flow:

```text
New agent

Filename
[ security-reviewer.md ]

Description
[ Reviews code for security issues ]

Mode
[ subagent ▾ ]

Create
```

The resulting file:

```text
agents/security-reviewer.md
```

opens immediately in the Agent Editor.

---

## 27. Skills Library

The Skills section provides an inventory of skill sources known to OCP.

Useful grouping:

```text
Local skills
Git skills
External paths
```

Example:

```text
Local

browser-testing
frontend-design

Git

deep-research
https://github.com/example/deep-research

External

/home/user/custom-skill
```

The page should prioritize discoverability and profile usage.

Where useful, display:

```text
Used by: default, deep
```

---

## 28. Plugins Library

The Plugins section lists plugin declarations known to OCP.

Example:

```text
Plugins

opencode-wakatime
example-plugin@1.2.0

+ Add plugin
```

Usage context may be displayed:

```text
opencode-wakatime
Shared

example-plugin
Profiles: deep, openrouter
```

---

## 29. Settings

Settings focuses on global OCP information.

Example:

```text
OCP configuration
~/.config/ocp

Git repository
git@github.com:user/ocp-config.git

Active profile
deep

OpenCode
Detected

Generated data
~/.local/share/ocp/...
```

Useful actions may include:

- Sync
- Apply
- reveal/open config directory where supported
- view Git state

---

## 30. Save

Save writes changes to canonical OCP source files.

Examples:

```text
profiles/deep.yaml
agents/implementer.md
ocp.yaml
```

The browser should clearly communicate successful saves.

Example:

```text
Saved
```

---

## 31. Apply

Apply runs the equivalent of:

```bash
ocp apply
```

This regenerates OpenCode environments from the current canonical source.

Suggested workflow:

```text
Edit
↓
Save
↓
Apply
```

A convenience action may combine them:

```text
Save & Apply
```

---

## 32. Sync

Sync runs the existing OCP synchronization workflow.

Equivalent CLI action:

```bash
ocp sync
```

The UI should present the result clearly.

Example:

```text
Sync complete

Repository current
5 profiles rendered
Active profile: deep
```

Git conflicts and other sync errors should surface using the same underlying OCP error information used by the CLI.

---

## 33. Unsaved Changes

Browser-local edits should be tracked until saved.

When navigation would discard unsaved edits, show a concise confirmation.

Example:

```text
You have unsaved changes.

Discard changes?
```

---

## 34. External File Changes

OCP files may also be edited outside the browser.

MVP handling should support:

- reading current source data when a page loads
- refreshing data on browser refresh
- refreshing configuration when navigating between major areas where appropriate
- warning when an externally changed file conflicts with unsaved browser edits

This keeps browser behavior compatible with normal editor-based workflows.

---

## 35. Validation

The UI should use OCP’s existing parser and validation logic.

Examples:

```text
Profile "deep" references a missing agent:

./agents/deep-reviewer.md
```

```text
Invalid YAML:

profiles/openrouter.yaml
```

```text
Profile inheritance cycle:

deep → default → deep
```

Errors should identify:

- affected profile
- affected file
- useful resolution context

---

## 36. Destructive Actions

Destructive actions should follow OCP’s existing safety model.

Examples include:

- deleting profiles
- overwriting drifted generated configuration
- replacing source files

The UI should present the specific effect before confirmation.

Example:

```text
Delete profile "deep"?

This removes:
profiles/deep.yaml

Shared agents and skills remain available.
```

---

## 37. Implementation Architecture

Conceptual architecture:

```text
Browser
   ↓
local OCP HTTP server
   ↓
existing OCP application services
   ↓
configuration / resolver / Git / renderer
```

The web layer should primarily provide:

- HTTP routing
- serialization
- browser-facing validation results
- filesystem-backed editing operations
- invocation of existing OCP operations

Existing Go packages should be reused wherever they already expose the required behavior.

Implementation simplicity is the primary engineering goal for the MVP.

---

## 38. MVP Feature Set

The initial Web UI should include:

- `ocp ui`
- local browser server
- persistent sidebar
- Profiles workspace
- profile discovery
- profile selection
- profile creation
- profile duplication
- basic profile deletion
- inheritance display
- profile-level model editing
- profile agent composition
- per-profile agent model overrides
- agent source selection
- Agents library
- Agent Editor
- agent creation
- frontmatter forms for common fields
- Markdown body editing
- Skills inventory
- basic skill add/remove
- Plugins inventory
- basic plugin add/remove
- Settings
- Save
- Apply
- Save & Apply
- Sync
- validation/error display
- unsaved-change protection

---

## 39. Lightweight Enhancements

Straightforward enhancements may be included during MVP implementation when they fit naturally:

- model field autocomplete
- syntax highlighting
- richer permission editing
- profile-usage labels for agents/skills/plugins
- external file-change notifications
- simple diff preview
- bulk agent assignment

Core profile composition and editing should remain the priority.

---

## 40. Roadmap

### Profile Comparison

Side-by-side profile comparison.

```text
             lean                deep
──────────────────────────────────────
implementer model-a             model-b
reviewer    model-c             model-d
```

### Matrix View

Visualize agents/models across profiles.

```text
             default   lean   deep
implementer  GPT-X     Mini   Claude-X
reviewer     GPT-Y     Mini   Claude-Y
```

### Enhanced Markdown Editor

Potential improvements:

- Markdown preview
- syntax highlighting
- search/replace
- richer navigation

### Model Browser

Display model metadata and provide easier model discovery.

### Doctor UI

Visual interface for the future:

```bash
ocp doctor
```

### Backup and Restore

Manage future OCP snapshots and restoration.

### Machine-Specific Configuration

Visual support for future machine overlays.

### Remote UI Access

Optional secure access to the interface from another machine.

---

## 41. Design Direction

The visual design should be:

- compact
- calm
- functional
- easy to scan
- restrained in its use of color
- consistent with OCP’s terminal UX philosophy

Color should primarily communicate:

- selection
- inheritance
- warning/error state
- active status
- successful operations

The UI should prioritize information density without becoming visually crowded.

---

## 42. Target User Flow

Typical workflow:

```bash
ocp ui
```

The browser opens directly to the active profile.

Example:

```text
deep

Extends: default

Agents

implementer
inherited
model: openrouter/model-a

reviewer
overridden
model: anthropic/model-b

Skills

frontend-design
inherited

deep-research
this profile

Plugins

opencode-wakatime
inherited
```

The user changes:

```text
implementer
openrouter/model-a
```

to:

```text
implementer
openrouter/model-b
```

Then chooses:

```text
Save & Apply
```

OCP updates:

```text
profiles/deep.yaml
```

and regenerates the local OpenCode environment.

---

## 43. Success Criteria

The MVP succeeds when a user with several OCP profiles can:

1. run `ocp ui`
2. immediately see available profiles
3. open a profile and understand its inheritance
4. see the agents, skills, and plugins used by that profile
5. reuse one agent source across several profiles
6. assign a different model to that agent in each profile
7. create a profile without writing YAML
8. create an agent Markdown file in the browser
9. edit common frontmatter through forms
10. edit the Markdown body directly
11. save canonical OCP source files
12. apply the resulting configuration
13. synchronize the OCP repository
14. understand validation and sync errors when they occur

The key experiential goal is:

> **A user with five profiles should be able to understand and modify their OCP setup visually within seconds.**