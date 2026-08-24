// Package importer adds support for importing from OPM profile directories.
package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/nando/ocp/internal/config"
	"gopkg.in/yaml.v3"
)

const opmConfigDir = ".config/opm/profiles"

// DefaultOPMProfileDir returns the default OPM profiles directory.
func DefaultOPMProfileDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, opmConfigDir)
}

// HasOPMProfiles reports whether the default OPM profiles directory exists and contains profiles.
func HasOPMProfiles() bool {
	dir := DefaultOPMProfileDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		opcJSON := filepath.Join(dir, e.Name(), "opencode.json")
		if _, err := os.Stat(opcJSON); err == nil {
			return true
		}
	}
	return false
}

// ListOPMProfiles returns a sorted list of available OPM profile names.
func ListOPMProfiles(profileDir string) ([]string, error) {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return nil, err
	}
	var profiles []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		opcJSON := filepath.Join(profileDir, e.Name(), "opencode.json")
		if _, err := os.Stat(opcJSON); err == nil {
			profiles = append(profiles, e.Name())
		}
	}
	slices.Sort(profiles)
	return profiles, nil
}

// ImportProfileData holds parsed data from a single OPM profile directory.
type ImportProfileData struct {
	Name     string         `yaml:"name"`
	Root     string         `yaml:"-"`
	Config   map[string]any `yaml:"config"`
	Agents   map[string]string `yaml:"agents"` // name -> filename
	Guide    string         `yaml:"guide,omitempty"`
	AgentDir string         `yaml:"-"`     // actual agent dir name found on disk ("agent" or "agents")
}

func agentDir(root string) string {
	for _, d := range []string{"agent", "agents"} {
		if info, err := os.Stat(filepath.Join(root, d)); err == nil && info.IsDir() {
			return d
		}
	}
	return ""
}

// ReadProfileData reads and validates an OPM profile directory.
func ReadProfileData(root string) (*ImportProfileData, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect import input: %w", err)
	}
	if info.IsDir() && filepath.Base(root) == "opencode.json" {
		root = filepath.Dir(root)
	}
	jsonPath := filepath.Join(root, "opencode.json")
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode config: %w", err)
	}
	var native map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&native); err != nil {
		return nil, fmt.Errorf("parse %s: JSONC is unsupported; provide strict JSON: %w", jsonPath, err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("parse %s: JSONC is unsupported; provide one strict JSON value", jsonPath)
	}
	native, err = normalizeNumbers(native)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", jsonPath, err)
	}

	data := &ImportProfileData{
		Name:     filepath.Base(root),
		Root:     root,
		Config:   native,
		Agents:   make(map[string]string),
		AgentDir: agentDir(root),
	}

	if data.AgentDir != "" {
		if entries, e := os.ReadDir(filepath.Join(root, data.AgentDir)); e == nil {
			for _, ag := range entries {
				if ag.IsDir() || filepath.Ext(ag.Name()) != ".md" {
					continue
				}
				name := ag.Name()[:len(ag.Name())-3]
				data.Agents[name] = ag.Name()
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}

	if exists(filepath.Join(root, "AGENTS.md")) {
		data.Guide = "./AGENTS.md"
	}

	return data, nil
}

func cloneM(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// MergeProfiles reads multiple OPM profile directories and merges them into
// a single OCP source tree under the given source path. Global resources
// (skills, agents, instructions) are collected at root-level. Each selected
// profile retains its own native config and may override globals via
// per-profile declarations.
func MergeProfiles(data []*ImportProfileData, source string, force bool) error {
	if len(data) == 0 {
		return errors.New("no profile data to merge")
	}
	if err := os.MkdirAll(source, 0o700); err != nil {
		return err
	}

	globalSkills := make(map[string]bool)
	globalPlugins := make(map[string]bool)
	var globalInstructions []string
	seenInstructions := map[string]bool{}
	guideSource := ""

	orderedNames := make([]string, 0, len(data))
	for _, d := range data {
		orderedNames = append(orderedNames, d.Name)
		if raw, ok := d.Config["skills"]; ok {
			if list, ok := raw.([]any); ok {
				for _, s := range list {
					if sk, ok := s.(string); ok {
						globalSkills[sk] = true
					}
				}
			}
		}
		if instRaw, ok := d.Config["instructions"]; ok {
			if instList, ok := instRaw.([]any); ok {
				for _, inst := range instList {
					if s, ok := inst.(string); ok {
						if seenInstructions[s] {
							continue
						}
						seenInstructions[s] = true
						globalInstructions = append(globalInstructions, s)
					}
				}
			}
		}
		if plRaw, ok := d.Config["plugins"]; ok {
			if plist, ok := plRaw.([]any); ok {
				for _, p := range plist {
					if ps, ok := p.(string); ok {
						globalPlugins[ps] = true
					}
				}
			}
		}
		if guideSource == "" && d.Guide != "" {
			guideSource = d.Root
		}
	}

	doc := &config.Document{Version: 1}

	if len(globalSkills) > 0 {
		skills := make([]config.Skill, 0, len(globalSkills))
		for sk := range globalSkills {
			skills = append(skills, config.Skill{Source: sk})
		}
		sort.Slice(skills, func(i, j int) bool { return skills[i].Source < skills[j].Source })
		doc.Skills = skills
	}
	doc.Instructions = globalInstructions

	if len(globalPlugins) > 0 {
		plugins := make([]string, 0, len(globalPlugins))
		for p := range globalPlugins {
			plugins = append(plugins, p)
		}
		sort.Strings(plugins)
		doc.Plugins = plugins
	}

	// Build per-file profile declarations.
	type profileFileEntry struct {
		Extends      string            `yaml:"extends,omitempty"`
		Config       map[string]any    `yaml:"config,omitempty"`
		Instructions []string          `yaml:"instructions,omitempty"`
		Skills       []config.Skill    `yaml:"skills,omitempty"`
		Plugins      []string          `yaml:"plugins,omitempty"`
		Agents       map[string]config.Agent `yaml:"agents,omitempty"`
	}

	profileDir := filepath.Join(source, "profiles")
	for idx, dName := range orderedNames {
		d := data[idx]
		noExcl := cloneM(d.Config)
		delete(noExcl, "skills")
		delete(noExcl, "instructions")
		delete(noExcl, "plugins")
		for k := range d.Agents {
			delete(noExcl, k)
		}

		entry := profileFileEntry{}
		if len(noExcl) > 0 {
			entry.Config = noExcl
		}
		if len(d.Agents) > 0 {
			entry.Agents = make(map[string]config.Agent)
			for aname, afname := range d.Agents {
				entry.Agents[aname] = config.Agent{File: "./agents/" + afname}
			}
		}
		if d.Guide != "" {
			entry.Instructions = []string{d.Guide}
		}
		if idx > 0 {
			entry.Extends = orderedNames[idx-1]
		}
		if entry.Config == nil && entry.Agents == nil && len(entry.Instructions) == 0 && len(entry.Skills) == 0 && len(entry.Plugins) == 0 {
			continue
		}
		if err := os.MkdirAll(profileDir, 0o700); err != nil {
			return err
		}
		profData, err := yaml.Marshal(&entry)
		if err != nil {
			return fmt.Errorf("marshal profile %q: %w", dName, err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, dName+".yaml"), profData, 0o600); err != nil {
			return fmt.Errorf("write profile %q: %w", dName, err)
		}
	}

	y, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "ocp.yaml"), y, 0o600); err != nil {
		return err
	}

	// Write resources deterministically.
	if guideSource != "" && exists(filepath.Join(guideSource, "AGENTS.md")) {
		if err := copyFile(filepath.Join(source, "AGENTS.md"), filepath.Join(guideSource, "AGENTS.md"), force); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Join(source, "agents"), 0o700); err != nil {
		return err
	}
	for _, d := range data {
		if d.AgentDir == "" {
			continue
		}
		srcDir := filepath.Join(d.Root, d.AgentDir)
		for _, fname := range d.Agents {
			dst := filepath.Join(source, "agents", fname)
			if err := copyFile(dst, filepath.Join(srcDir, fname), force); err != nil {
				return fmt.Errorf("copy agent %s: %w", fname, err)
			}
		}
	}

	// Collect unique skill dirs.
	skillDirSet := make(map[string]bool)
	for _, d := range data {
		if entries, e := os.ReadDir(filepath.Join(d.Root, "skills")); e == nil {
			for _, sk := range entries {
				if !sk.IsDir() {
					continue
				}
				skillDirSet[sk.Name()] = true
			}
		}
	}
	skillDirs := make([]string, 0, len(skillDirSet))
	for dn := range skillDirSet {
		skillDirs = append(skillDirs, dn)
	}
	sort.Strings(skillDirs)

	if len(skillDirs) > 0 {
		for _, d := range data {
			if entries, e := os.ReadDir(filepath.Join(d.Root, "skills")); e == nil {
				for _, dn := range skillDirs {
					found := false
					for _, sk := range entries {
						if sk.Name() == dn {
							found = true
							break
						}
					}
					if !found {
						continue
					}
					dst := filepath.Join(source, "skills", dn)
					if err := copyTree(dst, filepath.Join(d.Root, "skills", dn), force); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// ImportOPM delegates to Import after locating the selected OPM profile.
func ImportOPM(source string, force bool, selectFn func(profiles []string) (string, bool)) error {
	profileDir := DefaultOPMProfileDir()
	profiles, err := ListOPMProfiles(profileDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return nil // No-op: nothing to import
	}
	name, ok := selectFn(profiles)
	if !ok {
		return nil
	}
	inputPath := filepath.Join(profileDir, name)
	return Import(inputPath, source, force)
}
