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
	"strings"

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
	Name     string            `yaml:"name"`
	Root     string            `yaml:"-"`
	Config   map[string]any    `yaml:"config"`
	Agents   map[string]string `yaml:"agents"` // name -> filename
	Guide    string            `yaml:"guide,omitempty"`
	AgentDir string            `yaml:"-"` // actual agent dir name found on disk ("agent" or "agents")
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
				if ag.Type()&os.ModeSymlink != 0 {
					return nil, fmt.Errorf("agent file %q must be a regular file", ag.Name())
				}
				name := ag.Name()[:len(ag.Name())-3]
				if !validImportName(name) {
					return nil, fmt.Errorf("invalid imported agent name %q", name)
				}
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

// MergeProfiles converts multiple OPM profiles into independent OCP profile
// files while reusing only byte-identical agent sources.
func MergeProfiles(data []*ImportProfileData, source string, force bool) error {
	if len(data) == 0 {
		return errors.New("no profile data to merge")
	}
	parent := filepath.Dir(source)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staged, err := os.MkdirTemp(parent, ".ocp-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	if err := mergeProfilesInto(data, staged); err != nil {
		return err
	}
	if err := os.MkdirAll(source, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(staged)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := collision(filepath.Join(source, entry.Name()), force); err != nil {
			return err
		}
	}
	backup, err := os.MkdirTemp(parent, ".ocp-import-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(backup)
	ordered := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() != "ocp.yaml" {
			ordered = append(ordered, entry)
		}
	}
	for _, entry := range entries {
		if entry.Name() == "ocp.yaml" {
			ordered = append(ordered, entry)
			break
		}
	}
	type publication struct {
		name   string
		hadOld bool
	}
	var published []publication
	rollback := func() {
		for index := len(published) - 1; index >= 0; index-- {
			item := published[index]
			destination := filepath.Join(source, item.name)
			_ = os.RemoveAll(destination)
			if item.hadOld {
				_ = os.Rename(filepath.Join(backup, item.name), destination)
			}
		}
	}
	for _, entry := range ordered {
		name := entry.Name()
		destination := filepath.Join(source, name)
		hadOld := exists(destination)
		if hadOld {
			if err := os.Rename(destination, filepath.Join(backup, name)); err != nil {
				rollback()
				return fmt.Errorf("preserve existing %s: %w", name, err)
			}
		}
		if err := os.Rename(filepath.Join(staged, name), destination); err != nil {
			if hadOld {
				_ = os.Rename(filepath.Join(backup, name), destination)
			}
			rollback()
			return fmt.Errorf("publish imported %s: %w", name, err)
		}
		published = append(published, publication{name: name, hadOld: hadOld})
	}
	return nil
}

func mergeProfilesInto(data []*ImportProfileData, source string) error {
	if len(data) == 0 {
		return errors.New("no profile data to merge")
	}
	if err := os.MkdirAll(source, 0o700); err != nil {
		return err
	}
	seenProfiles := map[string]bool{}
	for _, d := range data {
		if !validImportName(d.Name) {
			return fmt.Errorf("invalid imported profile name %q", d.Name)
		}
		if seenProfiles[d.Name] {
			return fmt.Errorf("duplicate imported profile %q", d.Name)
		}
		seenProfiles[d.Name] = true
	}

	type profileFileEntry struct {
		Config       map[string]any          `yaml:"config,omitempty"`
		Instructions []string                `yaml:"instructions,omitempty"`
		Skills       []string                `yaml:"skills,omitempty"`
		Agents       map[string]config.Agent `yaml:"agents,omitempty"`
	}

	agentContents := map[string][]byte{}
	agentDestinations := make([]map[string]string, len(data))
	for index, d := range data {
		agentDestinations[index] = map[string]string{}
		for name, filename := range d.Agents {
			contents, err := os.ReadFile(filepath.Join(d.Root, d.AgentDir, filename))
			if err != nil {
				return fmt.Errorf("read agent %s from profile %s: %w", filename, d.Name, err)
			}
			destination := filename
			if existing, used := agentContents[destination]; used && !bytes.Equal(existing, contents) {
				extension := filepath.Ext(filename)
				base := strings.TrimSuffix(filename, extension)
				destination = base + "--" + d.Name + extension
				for suffix := 2; ; suffix++ {
					if existing, used = agentContents[destination]; !used || bytes.Equal(existing, contents) {
						break
					}
					destination = fmt.Sprintf("%s--%s--%d%s", base, d.Name, suffix, extension)
				}
			}
			agentContents[destination] = contents
			agentDestinations[index][name] = destination
		}
	}
	if len(agentContents) > 0 {
		agentsDir := filepath.Join(source, "agents")
		if err := os.MkdirAll(agentsDir, 0o700); err != nil {
			return err
		}
		for filename, contents := range agentContents {
			destination := filepath.Join(agentsDir, filename)
			if err := collision(destination, true); err != nil {
				return err
			}
			if err := os.WriteFile(destination, contents, 0o600); err != nil {
				return fmt.Errorf("write agent %s: %w", filename, err)
			}
		}
	}

	profileDir := filepath.Join(source, "profiles")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return err
	}
	for index, d := range data {
		entry := profileFileEntry{Config: cloneM(d.Config)}
		if len(agentDestinations[index]) > 0 {
			entry.Agents = make(map[string]config.Agent)
			for name, filename := range agentDestinations[index] {
				entry.Agents[name] = config.Agent{File: "./agents/" + filename}
			}
		}
		if d.Guide != "" {
			guidePath := filepath.Join("instructions", d.Name+".md")
			if err := copyFile(filepath.Join(source, guidePath), filepath.Join(d.Root, "AGENTS.md"), true); err != nil {
				return fmt.Errorf("copy instructions for profile %s: %w", d.Name, err)
			}
			entry.Instructions = []string{"./" + filepath.ToSlash(guidePath)}
		}
		if skills, err := os.ReadDir(filepath.Join(d.Root, "skills")); err == nil {
			for _, skill := range skills {
				if !skill.IsDir() {
					continue
				}
				relative := filepath.Join("skills", d.Name, skill.Name())
				if err := copyTree(filepath.Join(source, relative), filepath.Join(d.Root, "skills", skill.Name()), true); err != nil {
					return fmt.Errorf("copy skill %s for profile %s: %w", skill.Name(), d.Name, err)
				}
				entry.Skills = append(entry.Skills, "./"+filepath.ToSlash(relative))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		profData, err := yaml.Marshal(&entry)
		if err != nil {
			return fmt.Errorf("marshal profile %q: %w", d.Name, err)
		}
		profilePath := filepath.Join(profileDir, d.Name+".yaml")
		if err := collision(profilePath, true); err != nil {
			return err
		}
		if err := os.WriteFile(profilePath, profData, 0o600); err != nil {
			return fmt.Errorf("write profile %q: %w", d.Name, err)
		}
	}

	y, err := yaml.Marshal(&config.Document{Version: 1})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "ocp.yaml"), y, 0o600); err != nil {
		return err
	}

	return nil
}

func validImportName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")
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
