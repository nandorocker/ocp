// Package config loads and resolves OCP configuration documents.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const FileName = "ocp.yaml"

// Document is the parsed OCP configuration.
type Document struct {
	Version          int                    `yaml:"version"`
	Config           map[string]any         `yaml:"config,omitempty"`
	Instructions     []string               `yaml:"instructions,omitempty"`
	Skills           []Skill                `yaml:"skills,omitempty"`
	Plugins          []string               `yaml:"plugins,omitempty"`
	Agents           map[string]Agent       `yaml:"agents,omitempty"`
	Profiles         map[string]ProfileSpec `yaml:"profiles,omitempty"`
	profilesDeclared bool
	SourceDir        string `yaml:"-"` // runtime only, never serialized
}

// ProfileSpec is an unresolved profile declaration.
type ProfileSpec struct {
	Extends      string           `yaml:",omitempty"`
	Config       map[string]any   `yaml:"config,omitempty"`
	Instructions []string         `yaml:"instructions,omitempty"`
	Skills       []Skill          `yaml:"skills,omitempty"`
	Plugins      []string         `yaml:"plugins,omitempty"`
	Agents       map[string]Agent `yaml:"agents,omitempty"`
}

// Skill identifies a local or Git-backed skill source. Ref is optional.
type Skill struct {
	Source string
	Ref    string
}

func (s Skill) MarshalYAML() (any, error) {
	if s.Ref == "" {
		return s.Source, nil
	}
	return struct {
		Git string `yaml:"git"`
		Ref string `yaml:"ref"`
	}{Git: s.Source, Ref: s.Ref}, nil
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
	if doc == nil {
		return nil, fmt.Errorf("document is nil")
	}
	if !doc.profilesDeclared {
		return []Profile{resolved("default", compositionFromDocument(doc))}, nil
	}
	return resolveProfiles(doc, doc.Profiles)
}

type composition struct {
	config       map[string]any
	instructions []string
	skills       []Skill
	plugins      []string
	agents       map[string]Agent
}

func compositionFromDocument(d *Document) composition {
	return composition{d.Config, d.Instructions, d.Skills, d.Plugins, d.Agents}
}
func compositionFromProfile(p ProfileSpec) composition {
	return composition{p.Config, p.Instructions, p.Skills, p.Plugins, p.Agents}
}
func compositionFromResolved(p Profile) composition {
	return composition{p.Config, p.Instructions, p.Skills, p.Plugins, p.Agents}
}
func resolved(name string, c composition) Profile {
	return Profile{name, cloneMap(c.config), append([]string(nil), c.instructions...), append([]Skill(nil), c.skills...), append([]string(nil), c.plugins...), cloneAgents(c.agents)}
}

func mergeComposition(base, child composition) composition {
	return composition{
		config:       mergeMap(base.config, child.config),
		instructions: appendUnique(base.instructions, child.instructions),
		skills:       mergeSkills(base.skills, child.skills),
		plugins:      mergePlugins(base.plugins, child.plugins),
		agents:       mergeAgents(base.agents, child.agents),
	}
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
		bySource[skill.Source] = i
	}
	for _, skill := range child {
		if _, ok := byDeclaration[skill.declaration()]; ok {
			continue
		}
		if i, ok := bySource[skill.Source]; ok {
			delete(byDeclaration, out[i].declaration())
			out[i] = skill
			byDeclaration[skill.declaration()] = i
			continue
		}
		byDeclaration[skill.declaration()] = len(out)
		bySource[skill.Source] = len(out)
		out = append(out, skill)
	}
	return out
}

func (s Skill) declaration() string { return s.Source + "\x00" + s.Ref }

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
	values, err := mapping(node, "configuration", "version", "config", "instructions", "skills", "plugins", "agents", "profiles")
	if err != nil {
		return nil, err
	}
	doc := &Document{}
	if value := values["version"]; value != nil {
		if err := value.Decode(&doc.Version); err != nil {
			return nil, fmt.Errorf("version must be an integer")
		}
	}
	if doc.Config, err = nativeMap(values["config"], "config"); err != nil {
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
		values, err := mapping(node.Content[i+1], "profile "+name, "extends", "config", "instructions", "skills", "plugins", "agents")
		if err != nil {
			return nil, err
		}
		var p ProfileSpec
		if v := values["extends"]; v != nil {
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
				return nil, fmt.Errorf("profile %q extends must be a string", name)
			}
			if err := v.Decode(&p.Extends); err != nil {
				return nil, fmt.Errorf("profile %q extends must be a string", name)
			}
			if p.Extends == "" {
				return nil, fmt.Errorf("profile %q extends must not be empty", name)
			}
		}
		if p.Config, err = nativeMap(values["config"], "profile "+name+" config"); err != nil {
			return nil, err
		}
		if p.Instructions, err = stringsList(values["instructions"], "profile "+name+" instructions"); err != nil {
			return nil, err
		}
		if p.Skills, err = skills(values["skills"]); err != nil {
			return nil, err
		}
		if p.Plugins, err = stringsList(values["plugins"], "profile "+name+" plugins"); err != nil {
			return nil, err
		}
		if p.Agents, err = agents(values["agents"]); err != nil {
			return nil, err
		}
		out[name] = p
	}
	return out, nil
}

// ResolveFromDir resolves profiles from YAML files or profile directories when
// the profiles directory contains them. Otherwise it falls back to inline
// profiles already parsed from ocp.yaml.
func (doc *Document) ResolveFromDir(sourceDir string) ([]Profile, error) {
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
			fileProfiles, err := loadFileProfiles(profilesDir)
			if err != nil {
				return nil, err
			}
			return resolveProfiles(doc, fileProfiles)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read profiles directory: %w", err)
	}

	if !doc.profilesDeclared {
		return []Profile{resolved("default", compositionFromDocument(doc))}, nil
	}

	return resolveProfiles(doc, doc.Profiles)
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
		base, filePath, guidePath := "", "", ""
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

		values, err := mapping(node.Content[0], "profile file "+base, "extends", "config", "instructions", "skills", "plugins", "agents")
		if err != nil {
			return nil, err
		}

		var spec ProfileSpec
		if v := values["extends"]; v != nil {
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
				return nil, fmt.Errorf("profile %q extends must be a string", base)
			}
			if err := v.Decode(&spec.Extends); err != nil {
				return nil, fmt.Errorf("profile %q extends must be a string", base)
			}
			if spec.Extends == "" {
				return nil, fmt.Errorf("profile %q extends must not be empty", base)
			}
		}
		if spec.Config, err = nativeMap(values["config"], "profile "+base+" config"); err != nil {
			return nil, err
		}
		if spec.Instructions, err = stringsList(values["instructions"], "profile "+base+" instructions"); err != nil {
			return nil, err
		}
		if guidePath != "" {
			if _, err := os.Lstat(filepath.Join(filepath.Dir(filePath), "guide.md")); err == nil {
				spec.Instructions = append([]string{guidePath}, spec.Instructions...)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect profile guide %q: %w", guidePath, err)
			}
		}
		if spec.Skills, err = skills(values["skills"]); err != nil {
			return nil, err
		}
		if spec.Plugins, err = stringsList(values["plugins"], "profile "+base+" plugins"); err != nil {
			return nil, err
		}
		if spec.Agents, err = agents(values["agents"]); err != nil {
			return nil, err
		}
		result[base] = spec
		profileFiles[base] = entryName
	}
	return result, nil
}

func resolveProfiles(rootDoc *Document, fileProfiles map[string]ProfileSpec) ([]Profile, error) {
	states := make(map[string]uint8, len(fileProfiles))
	resolvedProfiles := make(map[string]Profile, len(fileProfiles))
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
		}

		profile := resolved(name, mergeComposition(base, compositionFromProfile(profileSpec)))
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
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func cloneProfileSpec(ps ProfileSpec) ProfileSpec {
	return ProfileSpec{
		Extends:      ps.Extends,
		Config:       cloneMap(ps.Config),
		Instructions: append([]string(nil), ps.Instructions...),
		Skills:       append([]Skill(nil), ps.Skills...),
		Plugins:      append([]string(nil), ps.Plugins...),
		Agents:       cloneAgents(ps.Agents),
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
		if configured, exists := agent.Config["model"]; exists {
			model, ok := configured.(string)
			if !ok || model == "" {
				return nil, fmt.Errorf("agent %q config.model must be a non-empty string", name)
			}
		}
		if v := values["model"]; v != nil {
			if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Value == "" {
				return nil, fmt.Errorf("agent %q model must be a non-empty string", name)
			}
			if configured, exists := agent.Config["model"]; exists {
				if configured.(string) != v.Value {
					return nil, fmt.Errorf("agent %q model conflicts with config.model", name)
				}
			}
			if agent.Config == nil {
				agent.Config = map[string]any{}
			}
			agent.Config["model"] = v.Value
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
			source, ref := splitRef(item.Value)
			if source == "" {
				return nil, fmt.Errorf("skill source must not be empty")
			}
			out = append(out, Skill{source, ref})
		case yaml.MappingNode:
			values, err := mapping(item, "skill", "git", "ref")
			if err != nil {
				return nil, err
			}
			git := values["git"]
			if git == nil || git.Tag != "!!str" || git.Value == "" {
				return nil, fmt.Errorf("skill git must be a non-empty string")
			}
			var ref string
			if v := values["ref"]; v != nil {
				if v.Tag != "!!str" {
					return nil, fmt.Errorf("skill ref must be a string")
				}
				ref = v.Value
			}
			out = append(out, Skill{git.Value, ref})
		default:
			return nil, fmt.Errorf("skill must be a string or mapping")
		}
	}
	return out, nil
}

func splitRef(source string) (string, string) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if at := strings.LastIndex(source, "@"); at > strings.Index(source, "://")+2 {
			return source[:at], source[at+1:]
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
	if _, err := resolveProfiles(doc, doc.Profiles); err != nil {
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
