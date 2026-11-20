// Package config loads and resolves OCP configuration documents.
package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const FileName = "ocp.yaml"

// Document is the parsed OCP configuration.
type Document struct {
	Version          int                       `yaml:"version"`
	Harnesses        []string                  `yaml:"harnesses,omitempty"`
	Hosts            []string                  `yaml:"hosts,omitempty"`
	Machines         map[string]MachineOverlay `yaml:"machines,omitempty"`
	Config           map[string]any            `yaml:"config,omitempty"`
	Instructions     []string                  `yaml:"instructions,omitempty"`
	Skills           []Skill                   `yaml:"skills,omitempty"`
	Plugins          []string                  `yaml:"plugins,omitempty"`
	Agents           map[string]Agent          `yaml:"agents,omitempty"`
	Profiles         map[string]ProfileSpec    `yaml:"profiles,omitempty"`
	Claude           map[string]any            `yaml:"claude,omitempty"`
	Codex            map[string]any            `yaml:"codex,omitempty"`
	Disabled         []string                  `yaml:"disabled,omitempty"`
	profilesDeclared bool
	SourceDir        string `yaml:"-"` // runtime only, never serialized
}

// ProfileSpec is an unresolved profile declaration.
type ProfileSpec struct {
	Extends      string                    `yaml:",omitempty"`
	Hosts        []string                  `yaml:"hosts,omitempty"`
	Machines     map[string]MachineOverlay `yaml:"machines,omitempty"`
	Config       map[string]any            `yaml:"config,omitempty"`
	Instructions []string                  `yaml:"instructions,omitempty"`
	Skills       []Skill                   `yaml:"skills,omitempty"`
	Plugins      []string                  `yaml:"plugins,omitempty"`
	Agents       map[string]Agent          `yaml:"agents,omitempty"`
	Claude       map[string]any            `yaml:"claude,omitempty"`
	Codex        map[string]any            `yaml:"codex,omitempty"`
	Disabled     []string                  `yaml:"disabled,omitempty"`
	// FilesDirs lists source-relative directories whose contents are copied
	// into the rendered profile root. Runtime only; discovered from
	// profiles/<name>/files/ and never read from YAML.
	FilesDirs []string `yaml:"-"`
}

// MachineOverlay holds per-machine overrides applied on top of a profile's
// shared base. Keys are machine names; values never nest further.
type MachineOverlay struct {
	Config       map[string]any   `yaml:"config,omitempty"`
	Instructions []string         `yaml:"instructions,omitempty"`
	Skills       []Skill          `yaml:"skills,omitempty"`
	Plugins      []string         `yaml:"plugins,omitempty"`
	Agents       map[string]Agent `yaml:"agents,omitempty"`
	Claude       map[string]any   `yaml:"claude,omitempty"`
	Codex        map[string]any   `yaml:"codex,omitempty"`
	Disabled     []string         `yaml:"disabled,omitempty"`
}

// Skill identifies a local or Git-backed skill source. Ref is optional. Path
// optionally selects a subdirectory of a Git repository; it is always a clean,
// relative, slash-separated path and is empty for local skills.
type Skill struct {
	Source string
	Ref    string
	Path   string
}

func (s Skill) MarshalYAML() (any, error) {
	if s.Ref == "" && s.Path == "" {
		return s.Source, nil
	}
	return struct {
		Git  string `yaml:"git"`
		Path string `yaml:"path,omitempty"`
		Ref  string `yaml:"ref,omitempty"`
	}{Git: s.Source, Path: s.Path, Ref: s.Ref}, nil
}

// IsGit reports whether the skill is fetched from a Git repository.
func (s Skill) IsGit() bool { return isGitURL(s.Source) }

func isGitURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://")
}

// Agent is an agent source file and its native OpenCode configuration.
type Agent struct {
	File   string
	Config map[string]any
}

// Profile is a fully resolved profile ready for rendering.
type Profile struct {
	Name         string
	Config       map[string]any
	Instructions []string
	Skills       []Skill
	Plugins      []string
	Agents       map[string]Agent
	Hosts        []string
	Machines     map[string]MachineOverlay
	// FilesDirs are source-relative directories of extra files, ordered from
	// the most distant ancestor to the profile itself; later directories win
	// per path.
	FilesDirs []string
	// Claude and Codex are native settings blocks for those harnesses,
	// merged like Config.
	Claude map[string]any
	Codex  map[string]any
	// Disabled names skills and plugins that are declared but switched off.
	// They are already removed from Skills, Plugins and config plugin lists.
	Disabled []string
	// UnmatchedDisabled lists Disabled names that matched nothing, which
	// renders as a warning rather than an error.
	UnmatchedDisabled []string
	// DisabledSkills are the skills Disabled removed. They are not rendered
	// but stay locked, validated and upgradeable.
	DisabledSkills []Skill
}

// Load parses and validates an OCP document from path.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("OCP configuration must be a mapping")
	}
	doc, err := parseDocument(node.Content[0])
	if err != nil {
		return nil, err
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("version must equal 1")
	}
	return doc, nil
}

// Resolve returns all fully composed profiles sorted by name.
func Resolve(doc *Document) ([]Profile, error) {
	return ResolveForMachine(doc, "")
}

// ResolveForMachine returns composed profiles for a machine, dropping
// profiles whose hosts list excludes it and merging per-machine overlays.
func ResolveForMachine(doc *Document, machine string) ([]Profile, error) {
	if doc == nil {
		return nil, fmt.Errorf("document is nil")
	}
	if !doc.profilesDeclared {
		return resolveProfiles(doc, implicitProfiles, machine)
	}
	return resolveProfiles(doc, doc.Profiles, machine)
}

// implicitProfiles is the single empty "default" profile used when a document
// declares no profiles; it inherits everything from the root document.
var implicitProfiles = map[string]ProfileSpec{"default": {}}

type composition struct {
	config       map[string]any
	instructions []string
	skills       []Skill
	plugins      []string
	agents       map[string]Agent
	hosts        []string
	machines     map[string]MachineOverlay
	filesDirs    []string
	claude       map[string]any
	codex        map[string]any
	disabled     []string
}

func compositionFromDocument(d *Document) composition {
	return composition{config: d.Config, instructions: d.Instructions, skills: d.Skills, plugins: d.Plugins, agents: d.Agents, hosts: d.Hosts, machines: d.Machines, claude: d.Claude, codex: d.Codex, disabled: d.Disabled}
}
func compositionFromProfile(p ProfileSpec) composition {
	return composition{p.Config, p.Instructions, p.Skills, p.Plugins, p.Agents, p.Hosts, p.Machines, p.FilesDirs, p.Claude, p.Codex, p.Disabled}
}
func compositionFromResolved(p Profile) composition {
	return composition{p.Config, p.Instructions, p.Skills, p.Plugins, p.Agents, p.Hosts, p.Machines, p.FilesDirs, p.Claude, p.Codex, p.Disabled}
}
func compositionFromOverlay(o MachineOverlay) composition {
	return composition{o.Config, o.Instructions, o.Skills, o.Plugins, o.Agents, nil, nil, nil, o.Claude, o.Codex, o.Disabled}
}
func resolved(name string, c composition) Profile {
	return Profile{name, cloneMap(c.config), append([]string(nil), c.instructions...), append([]Skill(nil), c.skills...), append([]string(nil), c.plugins...), cloneAgents(c.agents), append([]string(nil), c.hosts...), cloneMachines(c.machines), append([]string(nil), c.filesDirs...), cloneMap(c.claude), cloneMap(c.codex), append([]string(nil), c.disabled...), nil, nil}
}

func mergeComposition(base, child composition) composition {
	hosts := base.hosts
	if len(child.hosts) != 0 {
		hosts = child.hosts
	}
	return composition{
		config:       mergeMap(base.config, child.config),
		instructions: appendUnique(base.instructions, child.instructions),
		skills:       mergeSkills(base.skills, child.skills),
		plugins:      mergePlugins(base.plugins, child.plugins),
		agents:       mergeAgents(base.agents, child.agents),
		hosts:        append([]string(nil), hosts...),
		machines:     mergeMachines(base.machines, child.machines),
		filesDirs:    appendUnique(base.filesDirs, child.filesDirs),
		claude:       mergeMap(base.claude, child.claude),
		codex:        mergeMap(base.codex, child.codex),
		disabled:     appendUnique(base.disabled, child.disabled),
	}
}

// mergeMachines combines per-machine overlays. Child entries win per machine,
// with each machine's overlay itself merged compositionally.
func mergeMachines(base, child map[string]MachineOverlay) map[string]MachineOverlay {
	if len(base) == 0 && len(child) == 0 {
		return nil
	}
	out := make(map[string]MachineOverlay, len(base)+len(child))
	for name, overlay := range base {
		out[name] = cloneMachineOverlay(overlay)
	}
	for name, overlay := range child {
		if existing, ok := out[name]; ok {
			merged := mergeComposition(compositionFromOverlay(existing), compositionFromOverlay(overlay))
			out[name] = MachineOverlay{
				Config:       merged.config,
				Instructions: merged.instructions,
				Skills:       merged.skills,
				Plugins:      merged.plugins,
				Agents:       merged.agents,
				Claude:       merged.claude,
				Codex:        merged.codex,
				Disabled:     merged.disabled,
			}
			continue
		}
		out[name] = cloneMachineOverlay(overlay)
	}
	return out
}

func cloneMachineOverlay(o MachineOverlay) MachineOverlay {
	return MachineOverlay{
		Config:       cloneMap(o.Config),
		Instructions: append([]string(nil), o.Instructions...),
		Skills:       append([]Skill(nil), o.Skills...),
		Plugins:      append([]string(nil), o.Plugins...),
		Agents:       cloneAgents(o.Agents),
		Claude:       cloneMap(o.Claude),
		Codex:        cloneMap(o.Codex),
		Disabled:     append([]string(nil), o.Disabled...),
	}
}

func cloneMachines(in map[string]MachineOverlay) map[string]MachineOverlay {
	if in == nil {
		return nil
	}
	out := make(map[string]MachineOverlay, len(in))
	for name, overlay := range in {
		out[name] = cloneMachineOverlay(overlay)
	}
	return out
}

func mergePlugins(base, child []string) []string {
	out := append([]string(nil), base...)
	byIdentity := make(map[string]int, len(out))
	for index, plugin := range out {
		byIdentity[pluginIdentity(plugin)] = index
	}
	for _, plugin := range child {
		identity := pluginIdentity(plugin)
		if index, exists := byIdentity[identity]; exists {
			out[index] = plugin
			continue
		}
		byIdentity[identity] = len(out)
		out = append(out, plugin)
	}
	return out
}

func pluginIdentity(plugin string) string {
	if strings.HasPrefix(plugin, "./plugins/") {
		return strings.TrimPrefix(plugin, "./")
	}
	if strings.HasPrefix(plugin, "plugins/") {
		return plugin
	}
	if strings.HasSuffix(plugin, ".ts") || strings.HasSuffix(plugin, ".js") || strings.HasSuffix(plugin, ".mjs") || strings.HasSuffix(plugin, ".cjs") || strings.HasSuffix(plugin, ".mts") || strings.HasSuffix(plugin, ".cts") {
		return "plugins/" + plugin
	}
	if strings.HasPrefix(plugin, "@") {
		if slash := strings.IndexByte(plugin, '/'); slash >= 0 {
			if version := strings.LastIndexByte(plugin, '@'); version > slash {
				return plugin[:version]
			}
		}
		return plugin
	}
	if version := strings.LastIndexByte(plugin, '@'); version > 0 {
		return plugin[:version]
	}
	return plugin
}

func appendUnique(base, child []string) []string {
	out := append([]string(nil), base...)
	seen := make(map[string]bool, len(out))
	for _, value := range out {
		seen[value] = true
	}
	for _, value := range child {
		if !seen[value] {
			out, seen[value] = append(out, value), true
		}
	}
	return out
}

func mergeSkills(base, child []Skill) []Skill {
	out := append([]Skill(nil), base...)
	byDeclaration, bySource := make(map[string]int), make(map[string]int)
	for i, skill := range out {
		byDeclaration[skill.declaration()] = i
		bySource[skill.identity()] = i
	}
	for _, skill := range child {
		if _, ok := byDeclaration[skill.declaration()]; ok {
			continue
		}
		if i, ok := bySource[skill.identity()]; ok {
			delete(byDeclaration, out[i].declaration())
			out[i] = skill
			byDeclaration[skill.declaration()] = i
			continue
		}
		byDeclaration[skill.declaration()] = len(out)
		bySource[skill.identity()] = len(out)
		out = append(out, skill)
	}
	return out
}

func (s Skill) declaration() string { return s.identity() + "\x00" + s.Ref }

// identity names the skill independent of its ref, so a later declaration of
// the same skill at another ref replaces the earlier one. Skills from the same
// repository but different subpaths are distinct.
func (s Skill) identity() string {
	id := skillSourceIdentity(s.Source)
	if s.Path != "" {
		id += "//" + s.Path
	}
	return id
}

func skillSourceIdentity(source string) string {
	if strings.HasPrefix(source, "./skills/") {
		return strings.TrimPrefix(source, "./")
	}
	if strings.HasPrefix(source, "skills/") || strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "ssh://") || strings.HasPrefix(source, "git@") || filepath.IsAbs(source) {
		return source
	}
	return "skills/" + source
}

func mergeAgents(base, child map[string]Agent) map[string]Agent {
	out := cloneAgents(base)
	if out == nil {
		out = make(map[string]Agent, len(child))
	}
	for name, agent := range child {
		if inherited, ok := out[name]; ok {
			if agent.File != "" {
				inherited.File = agent.File
			}
			inherited.Config = mergeMap(inherited.Config, agent.Config)
			out[name] = inherited
		} else {
			out[name] = Agent{agent.File, cloneMap(agent.Config)}
		}
	}
	return out
}

func cloneAgents(in map[string]Agent) map[string]Agent {
	if in == nil {
		return nil
	}
	out := make(map[string]Agent, len(in))
	for name, agent := range in {
		out[name] = Agent{agent.File, cloneMap(agent.Config)}
	}
	return out
}

func mergeMap(base, child map[string]any) map[string]any {
	if base == nil && child == nil {
		return nil
	}
	out := cloneMap(base)
	if out == nil {
		out = make(map[string]any)
	}
	for key, childValue := range child {
		if baseValue, ok := out[key].(map[string]any); ok {
			if childMap, ok := childValue.(map[string]any); ok {
				out[key] = mergeMap(baseValue, childMap)
				continue
			}
		}
		out[key] = cloneValue(childValue)
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}
func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneMap(value)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = cloneValue(value[i])
		}
		return out
	default:
		return value
	}
}

func parseDocument(node *yaml.Node) (*Document, error) {
	values, err := mapping(node, "configuration", "version", "harnesses", "hosts", "machines", "config", "claude", "codex", "instructions", "skills", "plugins", "agents", "disabled", "profiles")
	if err != nil {
		return nil, err
	}
	doc := &Document{}
	if doc.Harnesses, err = harnesses(values["harnesses"]); err != nil {
		return nil, err
	}
	if value := values["version"]; value != nil {
		if err := value.Decode(&doc.Version); err != nil {
			return nil, fmt.Errorf("version must be an integer")
		}
	}
	if doc.Hosts, err = parseHosts(values["hosts"], "configuration"); err != nil {
		return nil, err
	}
	if doc.Machines, err = parseMachines(values["machines"], "configuration"); err != nil {
		return nil, err
	}
	if doc.Config, err = nativeMap(values["config"], "config"); err != nil {
		return nil, err
	}
	if doc.Claude, doc.Codex, err = harnessBlocks(values, ""); err != nil {
		return nil, err
	}
	if doc.Disabled, err = stringsList(values["disabled"], "disabled"); err != nil {
		return nil, err
	}
	if doc.Instructions, err = stringsList(values["instructions"], "instructions"); err != nil {
		return nil, err
	}
	if doc.Skills, err = skills(values["skills"]); err != nil {
		return nil, err
	}
	if doc.Plugins, err = stringsList(values["plugins"], "plugins"); err != nil {
		return nil, err
	}
	if doc.Agents, err = agents(values["agents"]); err != nil {
		return nil, err
	}
	doc.profilesDeclared = values["profiles"] != nil
	if doc.Profiles, err = profiles(values["profiles"]); err != nil {
		return nil, err
	}
	return doc, nil
}

func profiles(node *yaml.Node) (map[string]ProfileSpec, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("profiles must be a mapping")
	}
	out := make(map[string]ProfileSpec, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if err := validName("profile", name); err != nil {
			return nil, err
		}
		spec, err := parseProfileSpec(node.Content[i+1], "profile "+name)
		if err != nil {
			return nil, err
		}
		out[name] = spec
	}
	return out, nil
}

// parseProfileSpec parses a profile mapping, including optional host
// availability and per-machine overlays.
func parseProfileSpec(node *yaml.Node, label string) (ProfileSpec, error) {
	var p ProfileSpec
	values, err := mapping(node, label, "extends", "hosts", "machines", "config", "claude", "codex", "instructions", "skills", "plugins", "agents", "disabled")
	if err != nil {
		return p, err
	}
	if v := values["extends"]; v != nil {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return p, fmt.Errorf("%s extends must be a string", label)
		}
		if err := v.Decode(&p.Extends); err != nil {
			return p, fmt.Errorf("%s extends must be a string", label)
		}
		if p.Extends == "" {
			return p, fmt.Errorf("%s extends must not be empty", label)
		}
	}
	if p.Hosts, err = parseHosts(values["hosts"], label); err != nil {
		return p, err
	}
	if p.Machines, err = parseMachines(values["machines"], label); err != nil {
		return p, err
	}
	if p.Config, err = nativeMap(values["config"], label+" config"); err != nil {
		return p, err
	}
	if p.Claude, p.Codex, err = harnessBlocks(values, label+" "); err != nil {
		return p, err
	}
	if p.Disabled, err = stringsList(values["disabled"], label+" disabled"); err != nil {
		return p, err
	}
	if p.Instructions, err = stringsList(values["instructions"], label+" instructions"); err != nil {
		return p, err
	}
	if p.Skills, err = skills(values["skills"]); err != nil {
		return p, err
	}
	if p.Plugins, err = stringsList(values["plugins"], label+" plugins"); err != nil {
		return p, err
	}
	if p.Agents, err = agents(values["agents"]); err != nil {
		return p, err
	}
	return p, nil
}

// parseHosts parses and validates a hosts availability list.
func parseHosts(node *yaml.Node, label string) ([]string, error) {
	hosts, err := stringsList(node, label+" hosts")
	if err != nil {
		return nil, err
	}
	for _, host := range hosts {
		if err := validName("hosts entry", host); err != nil {
			return nil, err
		}
	}
	return hosts, nil
}

// parseMachines parses the per-machine overlay map. Overlays accept the same
// composition keys as profiles but never nest hosts, machines, or extends.
func parseMachines(node *yaml.Node, label string) (map[string]MachineOverlay, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s machines must be a mapping", label)
	}
	out := make(map[string]MachineOverlay, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if err := validName("machine", name); err != nil {
			return nil, err
		}
		overlayLabel := label + " machines " + name
		values, err := mapping(node.Content[i+1], overlayLabel, "config", "claude", "codex", "instructions", "skills", "plugins", "agents", "disabled")
		if err != nil {
			return nil, err
		}
		var overlay MachineOverlay
		if overlay.Config, err = nativeMap(values["config"], overlayLabel+" config"); err != nil {
			return nil, err
		}
		if overlay.Claude, overlay.Codex, err = harnessBlocks(values, overlayLabel+" "); err != nil {
			return nil, err
		}
		if overlay.Disabled, err = stringsList(values["disabled"], overlayLabel+" disabled"); err != nil {
			return nil, err
		}
		if overlay.Instructions, err = stringsList(values["instructions"], overlayLabel+" instructions"); err != nil {
			return nil, err
		}
		if overlay.Skills, err = skills(values["skills"]); err != nil {
			return nil, err
		}
		if overlay.Plugins, err = stringsList(values["plugins"], overlayLabel+" plugins"); err != nil {
			return nil, err
		}
		if overlay.Agents, err = agents(values["agents"]); err != nil {
			return nil, err
		}
		out[name] = overlay
	}
	return out, nil
}

// ResolveFromDir resolves profiles from YAML files or profile directories when
// the profiles directory contains them. Otherwise it falls back to inline
// profiles already parsed from ocp.yaml.
func (doc *Document) ResolveFromDir(sourceDir string) ([]Profile, error) {
	return doc.ResolveFromDirForMachine(sourceDir, "")
}

// ResolveFromDirForMachine resolves profiles for a machine, applying host
// availability filtering and per-machine overlays.
func (doc *Document) ResolveFromDirForMachine(sourceDir, machine string) ([]Profile, error) {
	specs, err := doc.profileSpecs(sourceDir)
	if err != nil {
		return nil, err
	}
	return resolveProfiles(doc, specs, machine)
}

// profileSpecs returns the unresolved profile declarations for a source:
// profile files or directories when the profiles directory has them,
// otherwise the inline profiles from ocp.yaml (or the implicit default).
func (doc *Document) profileSpecs(sourceDir string) (map[string]ProfileSpec, error) {
	doc.SourceDir = sourceDir

	profilesDir := filepath.Join(sourceDir, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err == nil {
		var hasFileProfiles bool
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml") {
				hasFileProfiles = true
				break
			}
		}
		if hasFileProfiles {
			if doc.profilesDeclared {
				return nil, errors.New("inline profiles and profiles directory cannot be used together")
			}
			return loadFileProfiles(profilesDir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read profiles directory: %w", err)
	}

	if !doc.profilesDeclared {
		return implicitProfiles, nil
	}
	return doc.Profiles, nil
}

func loadFileProfiles(profilesDir string) (map[string]ProfileSpec, error) {
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return nil, err
	}

	result := make(map[string]ProfileSpec)
	profileFiles := make(map[string]string)
	for _, e := range entries {
		entryName := e.Name()
		base, filePath, guidePath, filesRel := "", "", "", ""
		displayName := entryName
		if e.Type()&os.ModeSymlink != 0 {
			if strings.HasSuffix(entryName, ".yaml") || strings.HasSuffix(entryName, ".yml") {
				return nil, fmt.Errorf("profile file %q must be a regular file", entryName)
			}
			return nil, fmt.Errorf("profile entry %q must not be a symlink", entryName)
		}
		if e.IsDir() {
			base = entryName
			if err := validName("profile directory", base); err != nil {
				return nil, err
			}
			filePath = filepath.Join(profilesDir, entryName, "profile.yaml")
			displayName = filePath
			guidePath = filepath.Join("profiles", entryName, "guide.md")
			filesRel = filepath.Join("profiles", entryName, "files")
		} else {
			ext := filepath.Ext(entryName)
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			base = strings.TrimSuffix(entryName, ext)
			if err := validName("profile file", base); err != nil {
				return nil, err
			}
			filePath = filepath.Join(profilesDir, entryName)
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return nil, fmt.Errorf("inspect profile file %q: %w", displayName, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("profile file %q must be a regular file", displayName)
		}
		if previous, exists := profileFiles[base]; exists {
			return nil, fmt.Errorf("duplicate profile %q from %q and %q", base, previous, entryName)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read profile file %q: %w", displayName, err)
		}

		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			return nil, fmt.Errorf("parse profile file %q: %w", displayName, err)
		}
		if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("profile file %q must be a YAML mapping", displayName)
		}

		spec, err := parseProfileSpec(node.Content[0], "profile file "+base)
		if err != nil {
			return nil, err
		}
		if guidePath != "" {
			if _, err := os.Lstat(filepath.Join(filepath.Dir(filePath), "guide.md")); err == nil {
				spec.Instructions = append([]string{guidePath}, spec.Instructions...)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect profile guide %q: %w", guidePath, err)
			}
		}
		if filesRel != "" {
			if _, err := os.Lstat(filepath.Join(profilesDir, entryName, "files")); err == nil {
				spec.FilesDirs = []string{filesRel}
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect profile files %q: %w", filesRel, err)
			}
		}
		result[base] = spec
		profileFiles[base] = entryName
	}
	return result, nil
}

func resolveProfiles(rootDoc *Document, fileProfiles map[string]ProfileSpec, machine string) ([]Profile, error) {
	states := make(map[string]uint8, len(fileProfiles))
	resolvedProfiles := make(map[string]Profile, len(fileProfiles))
	// available tracks profiles visible on this machine after host filtering.
	available := make(map[string]bool, len(fileProfiles))
	var visit func(string) (Profile, error)

	visit = func(name string) (Profile, error) {
		switch states[name] {
		case 1:
			return Profile{}, fmt.Errorf("profile inheritance cycle at %q", name)
		case 2:
			return resolvedProfiles[name], nil
		}
		spec, exists := fileProfiles[name]
		if !exists {
			return Profile{}, fmt.Errorf("unknown profile %q", name)
		}
		states[name] = 1

		base := compositionFromDocument(rootDoc)
		profileSpec := cloneProfileSpec(spec)

		ext := profileSpec.Extends
		if ext != "" {
			if _, ok := fileProfiles[ext]; !ok {
				return Profile{}, fmt.Errorf("profile %q extends unknown profile %q", name, ext)
			}
			parent, err := visit(ext)
			if err != nil {
				return Profile{}, err
			}
			base = compositionFromResolved(parent)
			if !available[ext] && machine != "" {
				if containsHost(profileSpec.Hosts, machine) {
					return Profile{}, fmt.Errorf("profile %q extends %q which is unavailable on machine %q", name, ext, machine)
				}
				available[name] = false
				profile := resolved(name, mergeComposition(base, compositionFromProfile(profileSpec)))
				states[name] = 2
				resolvedProfiles[name] = profile
				return profile, nil
			}
		}

		merged := mergeComposition(base, compositionFromProfile(profileSpec))
		if machine != "" {
			if len(merged.hosts) != 0 && !containsHost(merged.hosts, machine) {
				available[name] = false
				profile := resolved(name, merged)
				states[name] = 2
				resolvedProfiles[name] = profile
				return profile, nil
			}
			if overlay, ok := merged.machines[machine]; ok {
				merged = mergeComposition(merged, compositionFromOverlay(overlay))
			}
		}
		available[name] = true

		profile := resolved(name, merged)
		states[name] = 2
		resolvedProfiles[name] = profile
		return profile, nil
	}

	names := make([]string, 0, len(fileProfiles))
	for name := range fileProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	profiles := make([]Profile, 0, len(names))
	for _, name := range names {
		profile, err := visit(name)
		if err != nil {
			return nil, err
		}
		if machine != "" && !available[name] {
			continue
		}
		// Children inherit their parent's unfiltered lists, so disabled
		// entries apply once, here, and a name disabled by an ancestor
		// still matches in every descendant.
		profile.Config = cloneMap(profile.Config)
		applyDisabled(&profile)
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func containsHost(hosts []string, machine string) bool {
	for _, host := range hosts {
		if host == machine {
			return true
		}
	}
	return false
}

func cloneProfileSpec(ps ProfileSpec) ProfileSpec {
	return ProfileSpec{
		Extends:      ps.Extends,
		Hosts:        append([]string(nil), ps.Hosts...),
		Machines:     cloneMachines(ps.Machines),
		Config:       cloneMap(ps.Config),
		Instructions: append([]string(nil), ps.Instructions...),
		Skills:       append([]Skill(nil), ps.Skills...),
		Plugins:      append([]string(nil), ps.Plugins...),
		Agents:       cloneAgents(ps.Agents),
		FilesDirs:    append([]string(nil), ps.FilesDirs...),
		Claude:       cloneMap(ps.Claude),
		Codex:        cloneMap(ps.Codex),
		Disabled:     append([]string(nil), ps.Disabled...),
	}
}

func agents(node *yaml.Node) (map[string]Agent, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("agents must be a mapping")
	}
	out := make(map[string]Agent, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if err := validName("agent", name); err != nil {
			return nil, err
		}
		values, err := mapping(node.Content[i+1], "agent "+name, "file", "config", "model")
		if err != nil {
			return nil, err
		}
		var agent Agent
		if v := values["file"]; v != nil {
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
				return nil, fmt.Errorf("agent %q file must be a string", name)
			}
			if err := v.Decode(&agent.File); err != nil {
				return nil, fmt.Errorf("agent %q file must be a string", name)
			}
		}
		if agent.Config, err = nativeMap(values["config"], "agent "+name+" config"); err != nil {
			return nil, err
		}
		if v := values["model"]; v != nil {
			model, err := modelValue(v, "agent "+name+" model")
			if err != nil {
				return nil, err
			}
			if configured, exists := agent.Config["model"]; exists {
				if !reflect.DeepEqual(configured, model) {
					return nil, fmt.Errorf("agent %q model conflicts with config.model", name)
				}
			}
			if agent.Config == nil {
				agent.Config = map[string]any{}
			}
			agent.Config["model"] = model
		}
		out[name] = agent
	}
	return out, nil
}

func skills(node *yaml.Node) ([]Skill, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("skills must be a list")
	}
	out := make([]Skill, 0, len(node.Content))
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			if item.Tag != "!!str" {
				return nil, fmt.Errorf("skill must be a string or mapping")
			}
			skill, err := parseSkillScalar(item.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, skill)
		case yaml.MappingNode:
			values, err := mapping(item, "skill", "git", "path", "ref")
			if err != nil {
				return nil, err
			}
			git := values["git"]
			if git == nil || git.Tag != "!!str" || git.Value == "" {
				return nil, fmt.Errorf("skill git must be a non-empty string")
			}
			source, err := expandGitHub(git.Value)
			if err != nil {
				return nil, err
			}
			if _, _, hasSub := SplitSubpath(source); hasSub {
				return nil, fmt.Errorf("skill git %q must not contain //; use the path key for a subpath", git.Value)
			}
			skill := Skill{Source: source}
			if v := values["ref"]; v != nil {
				if v.Tag != "!!str" {
					return nil, fmt.Errorf("skill ref must be a string")
				}
				skill.Ref = v.Value
			}
			if v := values["path"]; v != nil {
				if v.Tag != "!!str" {
					return nil, fmt.Errorf("skill path must be a string")
				}
				if skill.Path, err = CleanSkillPath(v.Value); err != nil {
					return nil, err
				}
			}
			if skill.Path != "" && !skill.IsGit() {
				return nil, fmt.Errorf("skill path requires a Git source")
			}
			out = append(out, skill)
		default:
			return nil, fmt.Errorf("skill must be a string or mapping")
		}
	}
	return out, nil
}

// parseSkillScalar parses a skill list entry. A Git entry is
// `<url>[//<subpath>][@<ref>]`, where the `//` separator is the first one after
// the URL scheme. `github:owner/repo` expands to the full GitHub HTTPS URL.
// Anything else is a local skill name or path, left for render-time validation.
func parseSkillScalar(value string) (Skill, error) {
	value, err := expandGitHub(value)
	if err != nil {
		return Skill{}, err
	}
	if !isGitURL(value) {
		if value == "" {
			return Skill{}, fmt.Errorf("skill source must not be empty")
		}
		return Skill{Source: value}, nil
	}
	source, ref := splitRef(value)
	if strings.Contains(ref, "//") {
		return Skill{}, fmt.Errorf("skill %q: put the //subpath before the @ref", value)
	}
	source, sub, hasSub := SplitSubpath(source)
	if source == "" {
		return Skill{}, fmt.Errorf("skill source must not be empty")
	}
	if hasSub && strings.Contains(sub, "@") && !strings.HasPrefix(source, "http") {
		return Skill{}, fmt.Errorf("skill %q: refs on ssh and git@ URLs need the expanded form (git:, path:, ref:)", value)
	}
	skill := Skill{Source: source, Ref: ref}
	if hasSub {
		if skill.Path, err = CleanSkillPath(sub); err != nil {
			return Skill{}, err
		}
	}
	return skill, nil
}

// expandGitHub rewrites the `github:owner/repo` shorthand, keeping any
// subpath and ref suffix, to the full HTTPS URL. Other values are unchanged.
func expandGitHub(value string) (string, error) {
	rest, ok := strings.CutPrefix(value, "github:")
	if !ok {
		return value, nil
	}
	repo, _, _ := strings.Cut(rest, "//")
	if at := strings.LastIndex(repo, "@"); at >= 0 {
		repo = repo[:at]
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("skill %q: github shorthand must be github:owner/repo", value)
	}
	return "https://github.com/" + rest, nil
}

// SplitSubpath splits `<url>//<subpath>`, searching for the separator after the
// scheme so the `//` of `https://` is never mistaken for it.
func SplitSubpath(source string) (string, string, bool) {
	start := 0
	if i := strings.Index(source, "://"); i >= 0 {
		start = i + 3
	}
	j := strings.Index(source[start:], "//")
	if j < 0 {
		return source, "", false
	}
	return source[:start+j], source[start+j+2:], true
}

// CleanSkillPath validates a repository subpath and returns it in canonical
// form. It must stay inside the repository.
func CleanSkillPath(p string) (string, error) {
	if p == "" || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("skill path %q must be a relative path inside the repository", p)
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("skill path %q must be a relative path inside the repository", p)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" {
			return "", fmt.Errorf("skill path %q must not contain .git", p)
		}
	}
	return clean, nil
}

// splitRef splits a trailing @ref from an http(s) source. The ref can only be
// in the path: an "@" in the authority is userinfo (user:token@host), which
// belongs to the URL.
func splitRef(source string) (string, string) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		pathStart := strings.Index(source, "://") + 3
		slash := strings.Index(source[pathStart:], "/")
		if slash < 0 {
			return source, ""
		}
		pathStart += slash
		if at := strings.LastIndex(source[pathStart:], "@"); at > 0 {
			return source[:pathStart+at], source[pathStart+at+1:]
		}
	}
	return source, ""
}

func stringsList(node *yaml.Node, label string) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s must be a list", label)
	}
	out := make([]string, len(node.Content))
	for i, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, fmt.Errorf("%s entries must be strings", label)
		}
		out[i] = item.Value
	}
	return out, nil
}

func nativeMap(node *yaml.Node, label string) (map[string]any, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", label)
	}
	if err := validateNativeModels(node, label); err != nil {
		return nil, err
	}
	value, err := nativeValue(node)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return value.(map[string]any), nil
}

func nativeValue(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.MappingNode:
		out := make(map[string]any, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Kind != yaml.ScalarNode || node.Content[i].Tag != "!!str" {
				return nil, fmt.Errorf("object keys must be strings")
			}
			value, err := nativeValue(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[node.Content[i].Value] = value
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, len(node.Content))
		for i, child := range node.Content {
			value, err := nativeValue(child)
			if err != nil {
				return nil, err
			}
			out[i] = value
		}
		return out, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!str":
			return node.Value, nil
		case "!!bool":
			var value bool
			_ = node.Decode(&value)
			return value, nil
		case "!!int":
			var value int64
			if err := node.Decode(&value); err != nil {
				return nil, fmt.Errorf("invalid integer")
			}
			return value, nil
		case "!!float":
			var value float64
			if err := node.Decode(&value); err != nil {
				return nil, fmt.Errorf("invalid number")
			}
			return value, nil
		default:
			return nil, fmt.Errorf("value is not JSON-compatible")
		}
	default:
		return nil, fmt.Errorf("value is not JSON-compatible")
	}
}

func mapping(node *yaml.Node, label string, allowed ...string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", label)
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = true
	}
	values := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("%s has a non-string key", label)
		}
		if !allowedSet[key.Value] {
			return nil, fmt.Errorf("unknown %s key %q", label, key.Value)
		}
		if _, exists := values[key.Value]; exists {
			return nil, fmt.Errorf("duplicate %s key %q", label, key.Value)
		}
		values[key.Value] = node.Content[i+1]
	}
	return values, nil
}

func validName(kind, name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid %s name %q", kind, name)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("invalid %s name %q", kind, name)
		}
	}
	return nil
}

// ValidMachineName reports whether name is usable as a machine identity.
func ValidMachineName(name string) error {
	return validName("machine", name)
}

// MigrateProfiles moves legacy inline profiles into profiles/*.yaml.
// It returns false when the source has no inline profiles.
func MigrateProfiles(sourceDir string) (bool, error) {
	rootPath := filepath.Join(sourceDir, FileName)
	doc, err := Load(rootPath)
	if err != nil {
		return false, err
	}
	if !doc.profilesDeclared {
		return false, nil
	}
	if len(doc.Profiles) == 0 {
		return false, errors.New("cannot migrate empty inline profiles")
	}
	if _, err := resolveProfiles(doc, doc.Profiles, ""); err != nil {
		return false, err
	}

	data, err := os.ReadFile(rootPath)
	if err != nil {
		return false, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, err
	}
	removeMappingKey(root.Content[0], "profiles")
	rootData, err := yaml.Marshal(root.Content[0])
	if err != nil {
		return false, err
	}
	rootTemp, err := stagedFile(sourceDir, rootData, 0o600)
	if err != nil {
		return false, err
	}
	defer os.Remove(rootTemp)

	profilesDir := filepath.Join(sourceDir, "profiles")
	if info, statErr := os.Stat(profilesDir); statErr == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("migration target %s is not a directory", profilesDir)
		}
		existing, loadErr := loadFileProfiles(profilesDir)
		if loadErr != nil {
			return false, loadErr
		}
		if len(existing) != 0 && !reflect.DeepEqual(normalizeProfileSpecs(existing), normalizeProfileSpecs(doc.Profiles)) {
			return false, errors.New("cannot migrate inline profiles: profiles directory already contains a different layout")
		}
		if len(existing) == 0 {
			if err := os.Remove(profilesDir); err != nil {
				return false, fmt.Errorf("remove empty profiles directory: %w", err)
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}

	if _, err := os.Stat(profilesDir); errors.Is(err, os.ErrNotExist) {
		stagedDir, err := os.MkdirTemp(sourceDir, ".profiles-migrate-")
		if err != nil {
			return false, err
		}
		defer os.RemoveAll(stagedDir)
		names := make([]string, 0, len(doc.Profiles))
		for name := range doc.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			profileData, err := yaml.Marshal(doc.Profiles[name])
			if err != nil {
				return false, fmt.Errorf("marshal profile %q: %w", name, err)
			}
			if err := os.WriteFile(filepath.Join(stagedDir, name+".yaml"), profileData, 0o600); err != nil {
				return false, fmt.Errorf("stage profile %q: %w", name, err)
			}
		}
		if err := os.Rename(stagedDir, profilesDir); err != nil {
			return false, fmt.Errorf("publish profiles directory: %w", err)
		}
	} else if err != nil {
		return false, err
	}

	if err := os.Rename(rootTemp, rootPath); err != nil {
		return false, fmt.Errorf("publish migrated %s: %w", FileName, err)
	}
	return true, nil
}

func normalizeProfileSpecs(profiles map[string]ProfileSpec) map[string]ProfileSpec {
	out := make(map[string]ProfileSpec, len(profiles))
	for name, profile := range profiles {
		profile = cloneProfileSpec(profile)
		if len(profile.Config) == 0 {
			profile.Config = nil
		}
		if len(profile.Claude) == 0 {
			profile.Claude = nil
		}
		if len(profile.Codex) == 0 {
			profile.Codex = nil
		}
		if len(profile.Disabled) == 0 {
			profile.Disabled = nil
		}
		if len(profile.Instructions) == 0 {
			profile.Instructions = nil
		}
		if len(profile.Skills) == 0 {
			profile.Skills = nil
		}
		if len(profile.Plugins) == 0 {
			profile.Plugins = nil
		}
		if len(profile.Agents) == 0 {
			profile.Agents = nil
		} else {
			for agentName, agent := range profile.Agents {
				if len(agent.Config) == 0 {
					agent.Config = nil
					profile.Agents[agentName] = agent
				}
			}
		}
		out[name] = profile
	}
	return out
}

func removeMappingKey(node *yaml.Node, key string) {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}

func stagedFile(dir string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, ".ocp-migrate-")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
