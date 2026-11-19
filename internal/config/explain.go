package config

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Origin labels describe which layer of the configuration contributed an
// entry. Entries from a machine overlay carry the machine name in
// Attribution.Machine instead.
const (
	OriginGlobal  = "global"  // the root ocp.yaml
	OriginProfile = "profile" // declared by the profile itself
	// Anything else is "extends <profile>": inherited from a parent profile.
)

// Attribution says where an entry comes from and whether it replaces a value
// defined in a lower layer.
type Attribution struct {
	// Origin is OriginGlobal, OriginProfile or "extends <profile>". It is empty
	// for entries that come from a machine overlay.
	Origin string
	// Machine is the machine whose overlay supplies the entry, if any.
	Machine string
	// Override is true only when the entry replaces a different value that a
	// lower layer already defined. Declaring something new, or declaring the
	// same value again, is not an override.
	Override bool
}

// SkillView is a resolved skill and its attribution.
type SkillView struct {
	Skill Skill
	Attribution
}

// PluginView is a resolved top-level plugin declaration and its attribution.
type PluginView struct {
	Plugin string
	Attribution
}

// NamedView is a named resolved entry (agent, instruction, file).
type NamedView struct {
	Name string
	Attribution
}

// ConfigView is one attributed entry of the native config: a top-level key, or
// an individual entry under mcp.servers, provider and providers. Value is the
// merged value at that path for the layer's resolution.
type ConfigView struct {
	Path  string
	Value any
	Attribution
}

// ProfileView is a profile annotated with where each part comes from. The
// lists hold the profile's own, inherited and global entries, plus the entries
// each machine overlay adds or replaces. Profile is resolved without any
// machine overlay and without host filtering.
type ProfileView struct {
	Profile Profile
	Extends string
	Hosts   []string

	Skills       []SkillView
	Plugins      []PluginView
	Agents       []NamedView
	Instructions []NamedView
	// Files are extra files relative to the rendered profile root, sorted by path.
	Files  []NamedView
	Config []ConfigView
}

// Explanation is the whole configuration, independent of the current machine.
type Explanation struct {
	// Machines lists every machine named by an overlay or a hosts list, sorted.
	Machines []string
	Profiles []ProfileView
	// Warnings are problems found while explaining, such as a machine whose
	// configuration does not resolve. They do not stop the explanation.
	Warnings []string
}

// ExplainFromDir resolves every profile of a source and annotates each skill,
// plugin, agent, instruction, extra file and config key with the layer that
// supplied it. It describes the configuration file and what it affects, so it
// is not filtered by machine: host-limited profiles are included, and every
// machine's overlay contributes its own attributed entries.
func (doc *Document) ExplainFromDir(sourceDir string) (Explanation, error) {
	specs, err := doc.profileSpecs(sourceDir)
	if err != nil {
		return Explanation{}, err
	}
	base, err := resolveProfiles(doc, specs, "")
	if err != nil {
		return Explanation{}, err
	}
	machines := machineNames(doc, specs)

	// Resolve once per machine; a profile limited to other hosts is absent from
	// that machine's result, which is exactly when its overlay cannot apply.
	out := Explanation{Machines: machines}
	perMachine := make(map[string]map[string]Profile, len(machines))
	for _, m := range machines {
		profiles, err := resolveProfiles(doc, specs, m)
		if err != nil {
			// The base configuration resolves; only this machine's does not.
			// Skip its overlay rows and say why instead of failing everything.
			out.Warnings = append(out.Warnings, fmt.Sprintf("machine %s does not resolve: %v", m, err))
			continue
		}
		byName := make(map[string]Profile, len(profiles))
		for _, p := range profiles {
			byName[p.Name] = p
		}
		perMachine[m] = byName
	}

	for _, p := range base {
		view := ProfileView{Profile: p, Extends: specs[p.Name].Extends, Hosts: p.Hosts}
		chain := inheritanceChain(specs, p.Name)
		t := newOriginTracker()
		replay(t, doc, specs, chain, "")
		view.fillFrom(t, p)
		files, err := explainFiles(sourceDir, p)
		if err != nil {
			return Explanation{}, err
		}
		view.Files = files

		for _, m := range machines {
			pm, ok := perMachine[m][p.Name]
			if !ok {
				continue
			}
			tm := newOriginTracker()
			replay(tm, doc, specs, chain, m)
			mv := ProfileView{}
			mv.fillFrom(tm, pm)
			view.addMachine(m, mv)
		}
		out.Profiles = append(out.Profiles, view)
	}
	return out, nil
}

// machineNames collects the machines the configuration mentions.
func machineNames(doc *Document, specs map[string]ProfileSpec) []string {
	seen := map[string]bool{}
	for name := range doc.Machines {
		seen[name] = true
	}
	for _, h := range doc.Hosts {
		seen[h] = true
	}
	for _, spec := range specs {
		for name := range spec.Machines {
			seen[name] = true
		}
		for _, h := range spec.Hosts {
			seen[h] = true
		}
	}
	return sortedKeys(seen)
}

// replay applies the layers in the order resolveProfiles merges them: global,
// then each profile of the chain (ancestors first) followed by the machine's
// overlay, whose accumulated definition is re-applied at every level.
func replay(t *originTracker, doc *Document, specs map[string]ProfileSpec, chain []string, machine string) {
	t.apply(Attribution{Origin: OriginGlobal}, compositionFromDocument(doc))
	machines := cloneMachines(doc.Machines)
	for i, name := range chain {
		spec := specs[name]
		label := Attribution{Origin: "extends " + name}
		if i == len(chain)-1 {
			label = Attribution{Origin: OriginProfile}
		}
		machines = mergeMachines(machines, spec.Machines)
		t.apply(label, compositionFromProfile(spec))
		if overlay, ok := machines[machine]; ok && machine != "" {
			t.apply(Attribution{Machine: machine}, compositionFromOverlay(overlay))
		}
	}
}

// fillFrom builds the annotated lists of p from the tracker's final state.
func (v *ProfileView) fillFrom(t *originTracker, p Profile) {
	for _, s := range p.Skills {
		v.Skills = append(v.Skills, SkillView{s, t.skills[s.identity()]})
	}
	seen := map[string]bool{}
	addPlugin := func(plugin string) {
		id := pluginIdentity(plugin)
		if !seen[id] {
			seen[id] = true
			v.Plugins = append(v.Plugins, PluginView{plugin, t.plugins[id]})
		}
	}
	for _, plugin := range p.Plugins {
		addPlugin(plugin)
	}
	for _, plugin := range ConfigPluginNames(p.Config) {
		addPlugin(plugin)
	}
	for _, name := range sortedKeys(p.Agents) {
		v.Agents = append(v.Agents, NamedView{name, t.agents[name]})
	}
	for _, in := range p.Instructions {
		a, ok := t.instructions[in]
		if !ok {
			a = Attribution{Origin: OriginProfile}
		}
		v.Instructions = append(v.Instructions, NamedView{in, a})
	}
	for _, path := range sortedKeys(t.config) {
		c := t.config[path]
		v.Config = append(v.Config, ConfigView{Path: path, Value: c.merged, Attribution: c.Attribution})
	}
}

// ConfigPluginNames lists the plugin names declared in a native config map
// under "plugins" (current) and "plugin" (deprecated). An entry is a name or a
// [name, options] pair; anything else is skipped.
func ConfigPluginNames(cfg map[string]any) []string {
	var out []string
	for _, key := range []string{"plugins", "plugin"} {
		list, _ := cfg[key].([]any)
		for _, entry := range list {
			name, _ := entry.(string)
			if pair, ok := entry.([]any); ok && len(pair) > 0 {
				name, _ = pair[0].(string)
			}
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// addMachine appends the entries that machine m's overlay contributes, leaving
// out any that merely repeat an entry the base view already has.
func (v *ProfileView) addMachine(m string, mv ProfileView) {
	for _, s := range mv.Skills {
		if s.Machine == m && !v.hasSkill(s.Skill) {
			v.Skills = append(v.Skills, s)
		}
	}
	for _, p := range mv.Plugins {
		if p.Machine == m && !v.hasPlugin(p.Plugin) {
			v.Plugins = append(v.Plugins, p)
		}
	}
	for _, a := range mv.Agents {
		if a.Machine == m {
			v.Agents = append(v.Agents, a)
		}
	}
	for _, in := range mv.Instructions {
		if in.Machine == m && !v.hasInstruction(in.Name) {
			v.Instructions = append(v.Instructions, in)
		}
	}
	for _, c := range mv.Config {
		if c.Machine == m && !v.hasConfig(c) {
			v.Config = append(v.Config, c)
		}
	}
}

func (v *ProfileView) hasSkill(s Skill) bool {
	for _, b := range v.Skills {
		if b.Machine == "" && b.Skill.declaration() == s.declaration() {
			return true
		}
	}
	return false
}

func (v *ProfileView) hasPlugin(plugin string) bool {
	for _, b := range v.Plugins {
		if b.Machine == "" && b.Plugin == plugin {
			return true
		}
	}
	return false
}

func (v *ProfileView) hasInstruction(name string) bool {
	for _, b := range v.Instructions {
		if b.Machine == "" && b.Name == name {
			return true
		}
	}
	return false
}

func (v *ProfileView) hasConfig(c ConfigView) bool {
	for _, b := range v.Config {
		if b.Machine == "" && b.Path == c.Path && reflect.DeepEqual(b.Value, c.Value) {
			return true
		}
	}
	return false
}

// inheritanceChain returns the profile and its ancestors, root-most first.
func inheritanceChain(specs map[string]ProfileSpec, name string) []string {
	var reversed []string
	for current := name; current != ""; current = specs[current].Extends {
		reversed = append(reversed, current)
	}
	chain := make([]string, len(reversed))
	for i, n := range reversed {
		chain[len(reversed)-1-i] = n
	}
	return chain
}

type configAttr struct {
	Attribution
	merged any
}

// originTracker replays the merge order of resolveProfiles and records which
// layer last supplied each entry, and whether it replaced a different value.
// It mirrors the merge semantics of mergeSkills, mergePlugins, mergeAgents,
// appendUnique and mergeMap.
type originTracker struct {
	skills       map[string]Attribution
	plugins      map[string]Attribution
	agents       map[string]Attribution
	instructions map[string]Attribution
	config       map[string]configAttr

	skillDecl  map[string]string
	pluginText map[string]string
	agentSeen  map[string]Agent
}

func newOriginTracker() *originTracker {
	return &originTracker{
		skills: map[string]Attribution{}, plugins: map[string]Attribution{},
		agents: map[string]Attribution{}, instructions: map[string]Attribution{},
		config:    map[string]configAttr{},
		skillDecl: map[string]string{}, pluginText: map[string]string{}, agentSeen: map[string]Agent{},
	}
}

// localPluginIdentity reports whether a plugin declaration names a file in the
// source's plugins folder, where equal identity means the same file.
func localPluginIdentity(plugin string) bool {
	return strings.HasPrefix(pluginIdentity(plugin), "plugins/")
}

func (t *originTracker) apply(layer Attribution, c composition) {
	// existed* record whether a LOWER layer already defined the entry, so two
	// declarations inside one layer never count as overriding each other.
	existedSkill, existedPlugin := map[string]bool{}, map[string]bool{}

	for _, s := range c.skills {
		id := s.identity()
		prev, had := t.skillDecl[id]
		if _, seen := existedSkill[id]; !seen {
			existedSkill[id] = had
		}
		if had && prev == s.declaration() {
			continue // the same declaration again keeps its first origin
		}
		t.skillDecl[id] = s.declaration()
		t.skills[id] = Attribution{layer.Origin, layer.Machine, existedSkill[id]}
	}

	plugins := append(append([]string(nil), c.plugins...), ConfigPluginNames(c.config)...)
	for _, plugin := range plugins {
		id := pluginIdentity(plugin)
		prev, had := t.pluginText[id]
		if _, seen := existedPlugin[id]; !seen {
			existedPlugin[id] = had
		}
		if had && (prev == plugin || (localPluginIdentity(prev) && localPluginIdentity(plugin))) {
			continue // same plugin (a local file is identified by its path)
		}
		t.pluginText[id] = plugin
		t.plugins[id] = Attribution{layer.Origin, layer.Machine, existedPlugin[id]}
	}

	for name, agent := range c.agents {
		prev, had := t.agentSeen[name]
		merged := Agent{File: prev.File, Config: mergeMap(prev.Config, agent.Config)}
		if agent.File != "" {
			merged.File = agent.File
		}
		t.agentSeen[name] = merged
		if !had {
			t.agents[name] = Attribution{layer.Origin, layer.Machine, false}
			continue
		}
		// Only a different file or a changed existing setting replaces anything.
		if (agent.File != "" && agent.File != prev.File) || differs(prev.Config, agent.Config) {
			t.agents[name] = Attribution{layer.Origin, layer.Machine, true}
		}
	}

	for _, in := range c.instructions {
		if _, ok := t.instructions[in]; !ok {
			t.instructions[in] = Attribution{Origin: layer.Origin, Machine: layer.Machine}
		}
	}

	for _, e := range configEntries(c.config) {
		t.dropStale(e)
		prev, existed := t.config[e.path]
		if !existed {
			t.config[e.path] = configAttr{Attribution{layer.Origin, layer.Machine, false}, e.value}
			continue
		}
		merged := mergeValue(prev.merged, e.value)
		if differs(prev.merged, e.value) {
			t.config[e.path] = configAttr{Attribution{layer.Origin, layer.Machine, true}, merged}
			continue
		}
		prev.merged = merged // nothing replaced: keep the first origin
		t.config[e.path] = prev
	}
}

// dropStale forgets tracked paths that writing e makes unreachable: entries
// beneath a path that is now a non-map value, and a non-map value at an
// ancestor of a path that is now a map.
func (t *originTracker) dropStale(e configEntry) {
	if _, isMap := e.value.(map[string]any); !isMap {
		for p := range t.config {
			if strings.HasPrefix(p, e.path+".") {
				delete(t.config, p)
			}
		}
	}
	for i := 0; i < len(e.path); i++ {
		if e.path[i] != '.' {
			continue
		}
		ancestor := e.path[:i]
		if prev, ok := t.config[ancestor]; ok {
			if _, isMap := prev.merged.(map[string]any); !isMap {
				delete(t.config, ancestor)
			}
		}
	}
}

// mergeValue merges maps the way mergeMap does and replaces everything else.
func mergeValue(base, next any) any {
	b, bok := base.(map[string]any)
	n, nok := next.(map[string]any)
	if bok && nok {
		return mergeMap(b, n)
	}
	return next
}

// differs reports whether next replaces a different existing value in prev.
// For maps, only a key present in both with a different value counts: adding
// a new key replaces nothing.
func differs(prev, next any) bool {
	n, nok := next.(map[string]any)
	p, pok := prev.(map[string]any)
	if nok && pok {
		for k, nv := range n {
			if pv, has := p[k]; has && differs(pv, nv) {
				return true
			}
		}
		return false
	}
	return !reflect.DeepEqual(prev, next)
}

type configEntry struct {
	path  string
	value any
}

// configEntries lists the config values worth attributing: top-level keys, and
// one level deeper for mcp servers, provider and providers. MCP servers live
// under mcp.servers.<name> (current) or mcp.<name> (legacy); mcp.timeout is a
// plain setting. Plugin lists are tracked per item, not as one value.
func configEntries(m map[string]any) []configEntry {
	var out []configEntry
	for _, k := range sortedKeys(m) {
		switch k {
		case "plugins", "plugin":
			continue
		case "mcp":
			sub, ok := m[k].(map[string]any)
			if !ok {
				out = append(out, configEntry{k, m[k]})
				continue
			}
			for _, name := range sortedKeys(sub) {
				switch servers, ok := sub[name].(map[string]any); {
				case name == "servers" && ok:
					for _, server := range sortedKeys(servers) {
						out = append(out, configEntry{"mcp.servers." + server, servers[server]})
					}
				default:
					out = append(out, configEntry{"mcp." + name, sub[name]})
				}
			}
		case "provider", "providers":
			sub, ok := m[k].(map[string]any)
			if !ok || len(sub) == 0 {
				out = append(out, configEntry{k, m[k]})
				continue
			}
			for _, name := range sortedKeys(sub) {
				out = append(out, configEntry{k + "." + name, sub[name]})
			}
		default:
			out = append(out, configEntry{k, m[k]})
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// explainFiles lists the extra files a profile renders, attributing each to
// the files directory that wins for its path (later directories win).
func explainFiles(sourceDir string, p Profile) ([]NamedView, error) {
	origins := map[string]string{}
	for _, dir := range p.FilesDirs {
		owner := path.Base(path.Dir(filepath.ToSlash(dir)))
		label := "extends " + owner
		if owner == p.Name {
			label = OriginProfile
		}
		root := filepath.Join(sourceDir, filepath.FromSlash(dir))
		err := filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			origins[filepath.ToSlash(rel)] = label
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("list profile files %q: %w", dir, err)
		}
	}
	out := make([]NamedView, 0, len(origins))
	for _, name := range sortedKeys(origins) {
		out = append(out, NamedView{name, Attribution{Origin: origins[name]}})
	}
	return out, nil
}

// Name is the name a skill is rendered under: the last path segment of its
// subpath or source.
func (s Skill) Name() string {
	source := strings.TrimSuffix(s.Source, "/")
	if s.Path != "" {
		return path.Base(s.Path)
	}
	source = strings.TrimSuffix(source, ".git")
	if i := strings.LastIndexAny(source, "/:"); i >= 0 {
		return source[i+1:]
	}
	return source
}
