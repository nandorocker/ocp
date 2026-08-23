// Package config loads and resolves OCP configuration documents.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const FileName = "ocp.yaml"

// Document is the parsed OCP configuration.
type Document struct {
	Version          int
	Config           map[string]any
	Instructions     []string
	Skills           []Skill
	Plugins          []string
	Agents           map[string]Agent
	Profiles         map[string]ProfileSpec
	profilesDeclared bool
}

// ProfileSpec is an unresolved profile declaration.
type ProfileSpec struct {
	Extends      string
	Config       map[string]any
	Instructions []string
	Skills       []Skill
	Plugins      []string
	Agents       map[string]Agent
}

// Skill identifies a local or Git-backed skill source. Ref is optional.
type Skill struct {
	Source string
	Ref    string
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

	states := make(map[string]uint8, len(doc.Profiles))
	resolvedProfiles := make(map[string]Profile, len(doc.Profiles))
	var visit func(string) (Profile, error)
	visit = func(name string) (Profile, error) {
		switch states[name] {
		case 1:
			return Profile{}, fmt.Errorf("profile inheritance cycle at %q", name)
		case 2:
			return resolvedProfiles[name], nil
		}
		spec, ok := doc.Profiles[name]
		if !ok {
			return Profile{}, fmt.Errorf("unknown profile %q", name)
		}
		states[name] = 1
		base := compositionFromDocument(doc)
		if spec.Extends != "" {
			if _, ok := doc.Profiles[spec.Extends]; !ok {
				return Profile{}, fmt.Errorf("profile %q extends unknown profile %q", name, spec.Extends)
			}
		}
		if spec.Extends != "" {
			parent, err := visit(spec.Extends)
			if err != nil {
				return Profile{}, err
			}
			base = compositionFromResolved(parent)
		}
		profile := resolved(name, mergeComposition(base, compositionFromProfile(spec)))
		states[name] = 2
		resolvedProfiles[name] = profile
		return profile, nil
	}

	names := make([]string, 0, len(doc.Profiles))
	for name := range doc.Profiles {
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
		values, err := mapping(node.Content[i+1], "agent "+name, "file", "config")
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
