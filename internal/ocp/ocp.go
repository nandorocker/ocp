// Package ocp owns OCP's local generated state and OpenCode activation.
package ocp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nando/ocp/internal/config"
)

const manifestName = ".ocp-manifest.json"

// DefaultOCPSrc is the canonical user-owned OCP configuration directory.
const DefaultOCPSrc = "~/.config/ocp"

// Paths contains all machine-local locations used by OCP.
type Paths struct {
	Home, ConfigHome, DataHome, StateHome string
	OpenCode, Data, Releases, Current     string
	RunConfigHome                         string
	StateFile, LockFile                   string
	OCPSrc                                string
}

// DefaultPaths derives locations from HOME and the XDG environment variables.
func DefaultPaths() (Paths, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return Paths{}, errors.New("HOME is not set")
	}
	configHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataHome := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	stateHome := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	p := Paths{Home: home, ConfigHome: configHome, DataHome: dataHome, StateHome: stateHome}
	p.OpenCode = filepath.Join(configHome, "opencode")
	p.Data = filepath.Join(dataHome, "ocp")
	p.Releases, p.Current = filepath.Join(p.Data, "releases"), filepath.Join(p.Data, "current")
	p.RunConfigHome = filepath.Join(p.Data, "run-config")
	p.StateFile, p.LockFile = filepath.Join(stateHome, "ocp", "state.json"), filepath.Join(configHome, ".opencode.ocp.lock")
	p.OCPSrc = filepath.Join(configHome, "ocp")
	return p, nil
}
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// State is OCP's private machine-local state.
type State struct {
	Version           int    `json:"version"`
	Source            string `json:"source"`
	PreservedOriginal string `json:"preserved_original,omitempty"`
	AutoCommit        bool   `json:"auto_commit"`
}

func LoadState(p Paths) (*State, error) {
	b, err := os.ReadFile(p.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if s.Version != 1 {
		return nil, fmt.Errorf("unsupported state version %d", s.Version)
	}
	return &s, nil
}

func SaveState(p Paths, s State) error {
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Version != 1 {
		return fmt.Errorf("unsupported state version %d", s.Version)
	}
	if s.Source != "" && !filepath.IsAbs(s.Source) {
		return errors.New("state source must be absolute")
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(p.StateFile, append(b, '\n'), 0o600)
}

// AcquireLock obtains the installation-wide non-blocking advisory lock.
func AcquireLock(p Paths) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(p.LockFile), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p.LockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another OCP operation is already running")
		}
		return nil, err
	}
	return func() error {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}, nil
}

// SkillProvider supplies an already-available checkout for a Git skill. It must not fetch.
type SkillProvider interface {
	SkillDir(config.Skill) (string, error)
}

type RenderOptions struct {
	Paths         Paths
	Source        string
	Force         bool
	SkillProvider SkillProvider
}
type RenderResult struct {
	Profiles []string
	Warnings []string
	Fallback FallbackResult
}

// DriftError reports OCP-owned generated files changed since their last render.
type DriftError struct{ Paths []string }

func (e *DriftError) Error() string {
	return "generated files have drifted: " + strings.Join(e.Paths, ", ")
}

// Render creates a new immutable release and publishes it only after all work succeeds.
func Render(o RenderOptions) (RenderResult, error) {
	source, err := filepath.Abs(o.Source)
	if err != nil {
		return RenderResult{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return RenderResult{}, fmt.Errorf("canonical source: %w", err)
	}
	doc, err := config.Load(filepath.Join(source, config.FileName))
	if err != nil {
		return RenderResult{}, err
	}
	profiles, err := doc.ResolveFromDir(source)
	if err != nil {
		return RenderResult{}, err
	}
	if len(profiles) == 0 {
		return RenderResult{}, errors.New("configuration resolves no profiles")
	}
	if err := os.MkdirAll(o.Paths.Releases, 0o700); err != nil {
		return RenderResult{}, err
	}
	if err := os.MkdirAll(runConfigHome(o.Paths), 0o700); err != nil {
		return RenderResult{}, err
	}
	active, _ := ActiveProfile(o.Paths)
	tmp, err := os.MkdirTemp(o.Paths.Releases, ".tmp-")
	if err != nil {
		return RenderResult{}, err
	}
	defer os.RemoveAll(tmp)
	result := RenderResult{Profiles: make([]string, 0, len(profiles))}
	for _, profile := range profiles {
		warnings, err := renderProfile(filepath.Join(tmp, profile.Name), source, profile, o.SkillProvider)
		if err != nil {
			return RenderResult{}, err
		}
		result.Profiles, result.Warnings = append(result.Profiles, profile.Name), append(result.Warnings, warnings...)
	}
	if !o.Force {
		if paths, err := drift(o.Paths); err != nil {
			return RenderResult{}, err
		} else if len(paths) != 0 {
			return RenderResult{}, &DriftError{Paths: paths}
		}
	}
	release := filepath.Join(o.Paths.Releases, fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.Rename(tmp, release); err != nil {
		return RenderResult{}, err
	}
	if err := replaceSymlink(o.Paths.Current, release); err != nil {
		return RenderResult{}, err
	}
	result.Fallback, err = ApplyFallback(o.Paths, active)
	if err != nil {
		return RenderResult{}, err
	}
	return result, nil
}

func renderProfile(dest, source string, p config.Profile, provider SkillProvider) ([]string, error) {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, err
	}
	native := cloneMap(p.Config)
	plugins, err := pluginList(native["plugin"])
	if err != nil {
		return nil, err
	}
	if native["plugin"] != nil || len(p.Plugins) != 0 {
		native["plugin"] = unique(append(plugins, p.Plugins...))
	}
	agents := map[string]any{}
	if old, ok := native["agent"]; ok {
		var valid bool
		agents, valid = old.(map[string]any)
		if !valid {
			return nil, errors.New("config.agent must be an object")
		}
		agents = cloneMap(agents)
	}
	for name, a := range p.Agents {
		if a.Config != nil {
			agents[name] = cloneMap(a.Config)
		}
	}
	if len(agents) > 0 {
		native["agent"] = agents
	}
	b, err := json.MarshalIndent(native, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicFile(filepath.Join(dest, "opencode.json"), append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	var warnings []string
	var instructions []byte
	for _, name := range p.Instructions {
		b, err := readSource(source, name, false)
		if err != nil {
			return nil, fmt.Errorf("instruction %q: %w", name, err)
		}
		instructions = append(instructions, b...)
		if len(b) > 0 && b[len(b)-1] != '\n' {
			instructions = append(instructions, '\n')
		}
	}
	if len(instructions) > 0 {
		if err := atomicFile(filepath.Join(dest, "AGENTS.md"), instructions, 0o600); err != nil {
			return nil, err
		}
	}
	for name, a := range p.Agents {
		if a.File == "" {
			continue
		}
		b, err := readSource(source, a.File, false)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", name, err)
		}
		if err := atomicFile(filepath.Join(dest, "agents", name+".md"), b, 0o600); err != nil {
			return nil, err
		}
	}
	used := map[string]bool{}
	for _, skill := range p.Skills {
		path := skill.Source
		external := filepath.IsAbs(path)
		provided := false
		if isGit(skill.Source) {
			if provider == nil {
				return nil, fmt.Errorf("Git skill %q requires a SkillProvider", skill.Source)
			}
			var err error
			path, err = provider.SkillDir(skill)
			if err != nil {
				return nil, err
			}
			provided = true
		}
		if !external && !provided {
			path = filepath.Join(source, path)
			if !within(source, path) {
				return nil, fmt.Errorf("skill %q escapes source", skill.Source)
			}
			if err := sourcePathSafe(source, path); err != nil {
				return nil, fmt.Errorf("skill %q: %w", skill.Source, err)
			}
		}
		base := filepath.Base(filepath.Clean(path))
		if base == "." || base == string(filepath.Separator) || used[base] {
			return nil, fmt.Errorf("skill destination collision: %q", base)
		}
		used[base] = true
		if _, err := os.Stat(path); err != nil {
			if external && errors.Is(err, os.ErrNotExist) {
				warnings = append(warnings, "local skill path not found: "+path)
				continue
			}
			return nil, fmt.Errorf("skill %q: %w", skill.Source, err)
		}
		if err := copyTree(filepath.Join(dest, "skills", base), path); err != nil {
			return nil, err
		}
	}
	return warnings, writeManifest(dest)
}

func pluginList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, errors.New("config.plugin must be a JSON array")
	}
	out := make([]string, 0, len(a))
	for _, v := range a {
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("config.plugin entries must be strings")
		}
		out = append(out, s)
	}
	return out, nil
}
func unique(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			out, seen[s] = append(out, s), true
		}
	}
	return out
}
func cloneMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func readSource(root, name string, dir bool) ([]byte, error) {
	if filepath.IsAbs(name) {
		return nil, errors.New("path must be relative to source")
	}
	path := filepath.Join(root, name)
	if !within(root, path) {
		return nil, errors.New("path escapes source")
	}
	if err := sourcePathSafe(root, path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("symlinks are not allowed")
	}
	if info.IsDir() != dir {
		return nil, fmt.Errorf("unexpected %s", map[bool]string{true: "file", false: "directory"}[dir])
	}
	return os.ReadFile(path)
}
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func sourcePathSafe(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes source")
	}
	current := root
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlinks are not allowed")
		}
	}
	return nil
}

func copyTree(dest, src string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlinks are not allowed in skills")
	}
	if !info.IsDir() {
		return errors.New("skill must be a directory")
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed: %s", path)
		}
		rel, _ := filepath.Rel(src, path)
		if rel == ".git" && d.IsDir() {
			return filepath.SkipDir
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported skill file: %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		inErr := in.Close()
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if inErr != nil {
			return inErr
		}
		return closeErr
	})
}

// OneOffConfigHome is an empty XDG config root used to keep one-off runs from
// inheriting the persistently active global OpenCode profile.
func OneOffConfigHome(p Paths) string { return runConfigHome(p) }

func runConfigHome(p Paths) string {
	if p.RunConfigHome != "" {
		return p.RunConfigHome
	}
	return filepath.Join(p.Data, "run-config")
}
func isGit(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://")
}

type manifest struct {
	Files map[string]string `json:"files"`
}

func writeManifest(root string) error {
	m := manifest{Files: map[string]string{}}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) == manifestName {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		sum := sha256.Sum256(b)
		m.Files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return atomicFile(filepath.Join(root, manifestName), append(b, '\n'), 0o600)
}
func drift(p Paths) ([]string, error) {
	target, err := os.Readlink(p.Current)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	root := target
	if !filepath.IsAbs(root) {
		root = filepath.Join(filepath.Dir(p.Current), root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name(), manifestName))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var m manifest
		if json.Unmarshal(b, &m) != nil {
			return nil, fmt.Errorf("invalid manifest for %s", e.Name())
		}
		for rel, want := range m.Files {
			b, err := os.ReadFile(filepath.Join(root, e.Name(), filepath.FromSlash(rel)))
			if err != nil {
				out = append(out, filepath.ToSlash(filepath.Join(e.Name(), rel)))
				continue
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != want {
				out = append(out, filepath.ToSlash(filepath.Join(e.Name(), rel)))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func atomicFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func replaceSymlink(link, target string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	tmp := link + ".tmp-" + fmt.Sprint(time.Now().UnixNano())
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, link)
}
