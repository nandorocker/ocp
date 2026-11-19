package ocp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Native ownership is deliberately separate from the legacy OpenCode takeover.
// No credentials, session data, or whole harness directories are managed here.
type nativeKey struct {
	Path           []string
	Original       any
	OriginalExists bool
	Installed      any
	// CreatedParent records that OCP created the collection holding this
	// entry, so reset removes the collection again once it is empty.
	CreatedParent bool `json:",omitempty"`
}

// nativeStateVersion 2 adds collection entries (MCP servers, providers, env
// variables) to owned keys. Version 1 state is a subset and still loads; an
// older OCP refuses version 2 rather than forgetting what it owns.
const nativeStateVersion = 2

type nativeFile struct {
	Data []byte
	Mode uint32
}

type nativeState struct {
	Version               int
	Profile               string
	Keys                  []nativeKey
	Files                 map[string]nativeFile
	Guide                 []byte
	GuideOriginalExists   bool
	ConfigOriginalExists  bool
	ConfigOriginallyEmpty bool
	EmptyConfigData       []byte
	ConfigInstalledHash   string
	ConfigHasExternalText bool
}

type nativeChange struct {
	Key                       string
	Before, After             []byte
	BeforeExists, AfterExists bool
	Mode                      uint32
	BeforeMode                uint32
}

// NativeInstallPlan contains a fully preflighted native update. Applying it
// checks the inputs again; an editor or harness may have changed them meanwhile.
type NativeInstallPlan struct {
	Harness string
	Changes []nativeChange
}

func nativeStatePath(p Paths, harness string) string {
	return filepath.Join(filepath.Dir(p.StateFile), "native-"+harness+".json")
}

func nativeRoot(p Paths, harness string) (string, error) {
	if p.Home == "" || !filepath.IsAbs(p.Home) {
		return "", errors.New("native installation requires an absolute home directory")
	}
	switch harness {
	case "claude":
		return filepath.Join(p.Home, ".claude"), nil
	case "codex":
		return filepath.Join(p.Home, ".codex"), nil
	default:
		return "", fmt.Errorf("unknown native harness %q", harness)
	}
}

func nativePath(p Paths, harness, key string) (string, error) {
	root, err := nativeRoot(p, harness)
	if err != nil {
		return "", err
	}
	switch key {
	case "state":
		return nativeStatePath(p, harness), nil
	case "config":
		if harness == "claude" {
			return filepath.Join(root, "settings.json"), nil
		}
		return filepath.Join(root, "config.toml"), nil
	case "guide":
		if harness == "claude" {
			return filepath.Join(root, "CLAUDE.md"), nil
		}
		return filepath.Join(root, "AGENTS.md"), nil
	}
	if !strings.HasPrefix(key, "files/") {
		return "", fmt.Errorf("invalid native artifact %q", key)
	}
	rel := strings.TrimPrefix(key, "files/")
	if filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("invalid native artifact %q", key)
	}
	if strings.HasPrefix(rel, "skills/") {
		if harness == "codex" {
			root = filepath.Join(p.Home, ".agents")
		}
	} else if !strings.HasPrefix(rel, "agents/") {
		return "", fmt.Errorf("invalid native artifact %q", key)
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// Refuse symlinks in destination ancestors as well as at the leaf. This is not
// a defense against a hostile same-user process racing filesystem operations.
func nativeRead(path string) ([]byte, bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("refusing symlink destination: %s", current)
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	b, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func loadNativeState(p Paths, harness string) (nativeState, error) {
	s := nativeState{Version: nativeStateVersion, Files: map[string]nativeFile{}}
	if harness != "claude" && harness != "codex" {
		return s, fmt.Errorf("unknown native harness %q", harness)
	}
	b, exists, err := nativeRead(nativeStatePath(p, harness))
	if err != nil || !exists {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("read %s ownership: %w", harness, err)
	}
	if s.Version != 1 && s.Version != nativeStateVersion {
		return s, fmt.Errorf("unsupported %s ownership version %d; upgrade ocp", harness, s.Version)
	}
	return s, nil
}

// NativeActiveProfile returns the native profile recorded by OCP, if installed.
func NativeActiveProfile(p Paths, harness string) (string, error) {
	s, err := loadNativeState(p, harness)
	return s.Profile, err
}

func nativeSettings(data []byte, harness string) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return m, nil
	}
	var err error
	if harness == "claude" {
		err = json.Unmarshal(data, &m)
	} else {
		err = toml.Unmarshal(data, &m)
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s configuration: %w", harness, err)
	}
	if m == nil {
		return nil, fmt.Errorf("%s configuration must be an object", harness)
	}
	return m, nil
}

func nativeEqual(a, b any) bool {
	// JSON normalizes integer representations from TOML and persisted ownership.
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && bytes.Equal(x, y)
}

func keyValue(m map[string]any, path []string) (any, bool) {
	for i, k := range path {
		v, ok := m[k]
		if i == len(path)-1 {
			return v, ok
		}
		m, ok = v.(map[string]any)
		if !ok {
			return nil, false
		}
	}
	return nil, false
}

func putKey(m map[string]any, path []string, value any, exists bool) error {
	for i, k := range path {
		if i == len(path)-1 {
			if exists {
				m[k] = value
			} else {
				delete(m, k)
			}
			return nil
		}
		next, ok := m[k]
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m, ok = next.(map[string]any)
		if !ok {
			return fmt.Errorf("native setting %q is not an object", k)
		}
	}
	return nil
}

func settingsKeys(harness string, settings map[string]any) []nativeKey {
	var result []nativeKey
	for _, k := range sortedKeys(settings) {
		if nativeCollections[harness][k] {
			if agents, ok := settings[k].(map[string]any); ok {
				for _, name := range sortedKeys(agents) {
					result = append(result, nativeKey{Path: []string{k, name}, Installed: agents[name]})
				}
				continue
			}
		}
		result = append(result, nativeKey{Path: []string{k}, Installed: settings[k]})
	}
	return result
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func guideBlock(guide []byte) []byte {
	if len(guide) == 0 {
		return nil
	}
	return []byte("\n<!-- BEGIN OCP MANAGED -->\n" + strings.TrimRight(string(guide), "\n") + "\n<!-- END OCP MANAGED -->\n")
}

// PlanNativeInstall validates every affected native file without writing it.
func PlanNativeInstall(p Paths, harness, profile string, rendered NativeProfile) (NativeInstallPlan, error) {
	return planNative(p, harness, profile, rendered, false)
}

func PlanNativeReset(p Paths, harness string) (NativeInstallPlan, error) {
	return planNative(p, harness, "", NativeProfile{}, true)
}

func planNative(p Paths, harness, profile string, rendered NativeProfile, reset bool) (NativeInstallPlan, error) {
	plan := NativeInstallPlan{Harness: harness}
	if _, err := nativeRoot(p, harness); err != nil {
		return plan, err
	}
	if _, exists, err := nativeRead(nativeStatePath(p, harness) + ".pending"); err != nil {
		return plan, err
	} else if exists {
		return plan, fmt.Errorf("%s has an interrupted update; run ocp apply --recover --harness %s", harness, harness)
	}
	old, err := loadNativeState(p, harness)
	if err != nil {
		return plan, err
	}
	if harness == "codex" && len(rendered.Guide) > 0 {
		root, _ := nativeRoot(p, harness)
		if data, exists, err := nativeRead(filepath.Join(root, "AGENTS.override.md")); err != nil {
			return plan, err
		} else if exists && len(bytes.TrimSpace(data)) > 0 {
			return plan, errors.New("Codex AGENTS.override.md would hide the OCP guide; remove the override or omit shared instructions")
		}
	}
	next := nativeState{Version: nativeStateVersion, Profile: profile, Files: map[string]nativeFile{}}
	add := func(key string, after []byte, afterExists bool, mode uint32) error {
		if !afterExists {
			after = nil
		}
		path, err := nativePath(p, harness, key)
		if err != nil {
			return err
		}
		before, exists, err := nativeRead(path)
		if err != nil {
			return err
		}
		var beforeMode uint32
		if exists && strings.HasPrefix(key, "files/") {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			beforeMode = uint32(info.Mode().Perm())
		}
		if exists == afterExists && bytes.Equal(before, after) && (beforeMode == 0 || beforeMode == mode) {
			return nil
		}
		plan.Changes = append(plan.Changes, nativeChange{Key: key, Before: before, After: after, BeforeExists: exists, AfterExists: afterExists, Mode: mode, BeforeMode: beforeMode})
		return nil
	}
	configPath, _ := nativePath(p, harness, "config")
	data, configExists, err := nativeRead(configPath)
	if err != nil {
		return plan, err
	}
	next.ConfigOriginalExists = configExists
	if old.Profile != "" {
		next.ConfigOriginalExists = old.ConfigOriginalExists
	}
	settings, err := nativeSettings(data, harness)
	if err != nil {
		return plan, err
	}
	next.ConfigOriginallyEmpty = len(settings) == 0
	if next.ConfigOriginallyEmpty {
		next.EmptyConfigData = append([]byte(nil), data...)
	}
	if old.Profile != "" {
		next.ConfigOriginallyEmpty = old.ConfigOriginallyEmpty
		next.EmptyConfigData = old.EmptyConfigData
		next.ConfigHasExternalText = old.ConfigHasExternalText || (old.ConfigInstalledHash != "" && old.ConfigInstalledHash != nativeHash(data))
	}
	next.ConfigInstalledHash = nativeHash(data)
	beforeSettings := cloneMap(settings)
	for _, owned := range old.Keys {
		value, exists := keyValue(settings, owned.Path)
		if !exists || !nativeEqual(value, owned.Installed) {
			return plan, fmt.Errorf("%s setting %s was edited outside OCP", harness, strings.Join(owned.Path, "."))
		}
		if err := putKey(settings, owned.Path, owned.Original, owned.OriginalExists); err != nil {
			return plan, err
		}
		if owned.CreatedParent && len(owned.Path) == 2 {
			if parent, ok := settings[owned.Path[0]].(map[string]any); ok && len(parent) == 0 {
				delete(settings, owned.Path[0])
			}
		}
	}
	// Snapshot which collections exist before installing, so every entry of
	// a collection OCP creates knows it, not just the first one.
	present := map[string]bool{}
	for k := range settings {
		present[k] = true
	}
	ownedBefore := map[string]bool{}
	for _, owned := range old.Keys {
		ownedBefore[strings.Join(owned.Path, "\x00")] = true
	}
	for _, owned := range settingsKeys(harness, rendered.Settings) {
		owned.Original, owned.OriginalExists = keyValue(settings, owned.Path)
		if len(owned.Path) == 2 {
			owned.CreatedParent = !present[owned.Path[0]]
			// Named entries the user wrote by hand are never replaced; an
			// identical one is adopted and left in place on reset, and OCP
			// may then update it. Environment variables are plain settings:
			// overwritten, then restored.
			adopted := ownedBefore[strings.Join(owned.Path, "\x00")]
			if owned.Path[0] != "env" && !adopted && owned.OriginalExists && !nativeEqual(owned.Original, owned.Installed) {
				return plan, fmt.Errorf("%s %s %q already exists and is not owned by OCP; remove it or make it identical", harness, owned.Path[0], owned.Path[1])
			}
		}
		if err := putKey(settings, owned.Path, owned.Installed, true); err != nil {
			return plan, err
		}
		next.Keys = append(next.Keys, owned)
	}
	if !reflect.DeepEqual(beforeSettings, settings) {
		var encoded []byte
		if harness == "claude" {
			encoded, err = json.MarshalIndent(settings, "", "  ")
			encoded = append(encoded, '\n')
		} else {
			encoded, err = patchNativeTOML(data, beforeSettings, settings)
		}
		if len(settings) == 0 && next.ConfigOriginallyEmpty && !next.ConfigHasExternalText {
			encoded = next.EmptyConfigData
		}
		if err != nil {
			return plan, err
		}
		next.ConfigInstalledHash = nativeHash(encoded)
		keep := next.ConfigOriginalExists || len(settings) > 0 || (harness == "codex" && len(bytes.TrimSpace(encoded)) > 0)
		if err = add("config", encoded, keep, 0o600); err != nil {
			return plan, err
		}
	}
	for rel, b := range rendered.Files {
		next.Files[rel] = nativeFile{Data: b, Mode: 0o600}
	}
	for _, name := range sortedKeys(rendered.Skills) {
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return plan, fmt.Errorf("invalid skill name %q", name)
		}
		src := rendered.Skills[name]
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, _ := filepath.Rel(src, path)
			if rel == ".git" && d.IsDir() {
				return filepath.SkipDir
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink not allowed in skill: %s", path)
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("unsupported skill file: %s", path)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := uint32(0o600)
			if info.Mode().Perm()&0o111 != 0 {
				mode = 0o700
			}
			next.Files[filepath.ToSlash(filepath.Join("skills", name, rel))] = nativeFile{Data: b, Mode: mode}
			return nil
		})
		if err != nil {
			return plan, err
		}
		if _, ok := next.Files["skills/"+name+"/SKILL.md"]; !ok {
			return plan, fmt.Errorf("skill %q has no SKILL.md", name)
		}
	}
	files := map[string]bool{}
	for rel := range old.Files {
		files[rel] = true
	}
	for rel := range next.Files {
		files[rel] = true
	}
	for _, rel := range sortedKeys(files) {
		path, err := nativePath(p, harness, "files/"+rel)
		if err != nil {
			return plan, err
		}
		b, exists, err := nativeRead(path)
		if err != nil {
			return plan, err
		}
		owned, wasOwned := old.Files[rel]
		desired, want := next.Files[rel]
		if wasOwned && (!exists || !bytes.Equal(b, owned.Data)) {
			return plan, fmt.Errorf("%s was edited outside OCP", path)
		}
		if wasOwned && exists {
			info, err := os.Stat(path)
			if err != nil {
				return plan, err
			}
			if uint32(info.Mode().Perm()) != owned.Mode {
				return plan, fmt.Errorf("%s permissions were edited outside OCP", path)
			}
		}
		if !wasOwned && exists {
			return plan, fmt.Errorf("refusing unmanaged file collision: %s", path)
		}
		if err = add("files/"+rel, desired.Data, want, desired.Mode); err != nil {
			return plan, err
		}
	}
	guidePath, _ := nativePath(p, harness, "guide")
	guide, guideExists, err := nativeRead(guidePath)
	if err != nil {
		return plan, err
	}
	next.GuideOriginalExists = guideExists
	if len(old.Guide) > 0 {
		next.GuideOriginalExists = old.GuideOriginalExists
	}
	if len(old.Guide) > 0 {
		if bytes.Count(guide, old.Guide) != 1 {
			return plan, fmt.Errorf("%s managed instruction block was edited outside OCP", harness)
		}
		guide = bytes.Replace(guide, old.Guide, nil, 1)
	}
	if bytes.Contains(guide, []byte("<!-- BEGIN OCP MANAGED -->")) || bytes.Contains(guide, []byte("<!-- END OCP MANAGED -->")) {
		return plan, fmt.Errorf("%s contains an unowned OCP instruction block", guidePath)
	}
	if bytes.Contains(rendered.Guide, []byte("<!-- BEGIN OCP MANAGED -->")) || bytes.Contains(rendered.Guide, []byte("<!-- END OCP MANAGED -->")) {
		return plan, errors.New("instructions contain reserved OCP block markers")
	}
	next.Guide = guideBlock(rendered.Guide)
	guide = append(guide, next.Guide...)
	if len(old.Guide) > 0 || len(next.Guide) > 0 {
		if err = add("guide", guide, len(guide) > 0 || next.GuideOriginalExists, 0o600); err != nil {
			return plan, err
		}
	}
	stateData, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return plan, err
	}
	if err = add("state", stateData, !reset, 0o600); err != nil {
		return plan, err
	}
	return plan, nil
}

func nativeHash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// ApplyNativePlan journals a target update before its first native write.
// A crash or partial failure can be resumed explicitly, without overwriting a
// file that has since been changed by the user or the harness.
func ApplyNativePlan(p Paths, plan NativeInstallPlan) error {
	for _, change := range plan.Changes {
		if err := checkNativeChange(p, plan.Harness, change, false); err != nil {
			return err
		}
	}
	b, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	journal := nativeStatePath(p, plan.Harness) + ".pending"
	if _, exists, err := nativeRead(journal); err != nil {
		return err
	} else if exists {
		return errors.New("native update already pending; recover it first")
	}
	if err := atomicFile(journal, b, 0o600); err != nil {
		return err
	}
	return finishNativePlan(p, plan)
}

func checkNativeChange(p Paths, harness string, c nativeChange, allowAfter bool) error {
	path, err := nativePath(p, harness, c.Key)
	if err != nil {
		return err
	}
	b, exists, err := nativeRead(path)
	if err != nil {
		return err
	}
	if exists == c.BeforeExists && bytes.Equal(b, c.Before) {
		if c.BeforeMode == 0 || !exists {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if uint32(info.Mode().Perm()) == c.BeforeMode {
			return nil
		}
	}
	if allowAfter && exists == c.AfterExists && bytes.Equal(b, c.After) {
		if !strings.HasPrefix(c.Key, "files/") || !exists {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if uint32(info.Mode().Perm()) == c.Mode {
			return nil
		}
	}
	return fmt.Errorf("%s changed during native update; refusing to overwrite it", path)
}

func finishNativePlan(p Paths, plan NativeInstallPlan) error {
	// Preflight every remaining file before resuming any writes.
	for _, c := range plan.Changes {
		if err := checkNativeChange(p, plan.Harness, c, true); err != nil {
			return err
		}
	}
	for _, c := range plan.Changes {
		if err := checkNativeChange(p, plan.Harness, c, true); err != nil {
			return err
		}
		path, _ := nativePath(p, plan.Harness, c.Key)
		if c.AfterExists {
			if err := atomicFile(path, c.After, os.FileMode(c.Mode)); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		} else if strings.HasPrefix(c.Key, "files/") {
			removeEmptyParents(p, plan.Harness, c.Key, path)
		}
	}
	return os.Remove(nativeStatePath(p, plan.Harness) + ".pending")
}

// removeEmptyParents removes directories OCP left empty under the harness's
// skills/ or agents/ folder, such as a removed skill's own folder. Removing a
// non-empty directory fails, so nothing else is touched.
func removeEmptyParents(p Paths, harness, key, path string) {
	top, _, _ := strings.Cut(strings.TrimPrefix(key, "files/"), "/")
	probe, err := nativePath(p, harness, "files/"+top+"/x")
	if err != nil {
		return
	}
	base := filepath.Dir(probe)
	for dir := filepath.Dir(path); dir != base && strings.HasPrefix(dir, base+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

func RecoverNative(p Paths, harness string) error {
	if _, err := nativeRoot(p, harness); err != nil {
		return err
	}
	b, exists, err := nativeRead(nativeStatePath(p, harness) + ".pending")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var plan NativeInstallPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	if plan.Harness != harness {
		return errors.New("native recovery target mismatch")
	}
	return finishNativePlan(p, plan)
}
