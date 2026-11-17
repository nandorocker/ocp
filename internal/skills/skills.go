// Package skills locks and materializes Git-backed OCP skills.
package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/ocp"
	"gopkg.in/yaml.v3"
)

const lockFileName = "ocp.lock"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Lock is the versioned, canonical-source keyed skill lockfile schema.
type Lock struct {
	Version int                  `yaml:"version"`
	Skills  map[string]LockEntry `yaml:"skills"`
}

// LockEntry records a source declaration and its immutable Git commit.
type LockEntry struct {
	Source string `yaml:"source"`
	Path   string `yaml:"path,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
	Commit string `yaml:"commit"`
}

// lockKey identifies a skill in the lock. Skills from one repository selected
// by different subpaths are separate entries and may pin different commits.
func lockKey(source, path string) string {
	key := canonicalSource(source)
	if path != "" {
		key += "//" + path
	}
	return key
}

// Prepare validates the canonical source lockfile and returns its checkout provider.
// It creates missing lock entries only when bootstrap is allowed.
func Prepare(sourceDir, dataDir string, skills []config.Skill, allowBootstrap bool) (ocp.SkillProvider, bool, error) {
	return prepare(sourceDir, dataDir, skills, allowBootstrap, isGitSource)
}

func prepare(sourceDir, dataDir string, skills []config.Skill, allowBootstrap bool, include func(string) bool) (ocp.SkillProvider, bool, error) {
	gitSkills := make([]config.Skill, 0, len(skills))
	for _, skill := range skills {
		if include(skill.Source) {
			gitSkills = append(gitSkills, skill)
		}
	}
	lockPath := filepath.Join(sourceDir, lockFileName)
	lock, exists, err := readLock(lockPath)
	if err != nil {
		return nil, false, err
	}
	if len(gitSkills) == 0 {
		return &provider{dataDir: dataDir, entries: nil}, false, nil
	}
	if !exists && !allowBootstrap {
		return nil, false, fmt.Errorf("Git skills require %s; rerun with bootstrap enabled", lockPath)
	}
	if !exists {
		lock = Lock{Version: 1, Skills: map[string]LockEntry{}}
	}

	changed := false
	requested := make(map[string]string, len(gitSkills))
	for _, skill := range gitSkills {
		key := lockKey(skill.Source, skill.Path)
		if ref, duplicate := requested[key]; duplicate && ref != skill.Ref {
			return nil, false, fmt.Errorf("Git skill %q is declared with conflicting refs", skill.Source)
		}
		requested[key] = skill.Ref
		entry, ok := lock.Skills[key]
		if ok {
			if entry.Ref != skill.Ref {
				return nil, false, fmt.Errorf("Git skill %q ref %q does not match lock ref %q", skill.Source, skill.Ref, entry.Ref)
			}
			continue
		}
		if !allowBootstrap {
			return nil, false, fmt.Errorf("Git skill %q has no entry in %s", skill.Source, lockPath)
		}
		commit, err := resolve(skill.Source, skill.Ref)
		if err != nil {
			return nil, false, err
		}
		lock.Skills[key] = LockEntry{Source: skill.Source, Path: skill.Path, Ref: skill.Ref, Commit: commit}
		changed = true
	}
	if changed {
		if err := writeLock(lockPath, lock); err != nil {
			return nil, false, err
		}
	}
	pruneCache(dataDir, lock)
	return &provider{dataDir: dataDir, entries: lock.Skills}, changed, nil
}

var cacheDirPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// pruneCache removes cached checkouts that no lock entry references, such as
// superseded commits and checkouts from the older source-keyed layout. It is
// best effort and only touches directories named like cache hashes.
func pruneCache(dataDir string, lock Lock) {
	keep := make(map[string]bool, len(lock.Skills))
	for _, entry := range lock.Skills {
		keep[filepath.Base(filepath.Dir(cachePath(dataDir, canonicalSource(entry.Source), entry.Commit)))] = true
	}
	root := filepath.Join(dataDir, "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && cacheDirPattern.MatchString(e.Name()) && !keep[e.Name()] {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

func readLock(filename string) (Lock, bool, error) {
	b, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return Lock{}, false, nil
	}
	if err != nil {
		return Lock{}, false, fmt.Errorf("read skill lock: %w", err)
	}
	var lock Lock
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err := decoder.Decode(&lock); err != nil {
		return Lock{}, false, fmt.Errorf("invalid skill lock %s: %w", filename, err)
	}
	if lock.Version != 1 {
		return Lock{}, false, fmt.Errorf("invalid skill lock %s: version must equal 1", filename)
	}
	if lock.Skills == nil {
		lock.Skills = map[string]LockEntry{}
	}
	for key, entry := range lock.Skills {
		if key == "" || entry.Source == "" || lockKey(entry.Source, entry.Path) != key || !commitPattern.MatchString(entry.Commit) || !validLockPath(entry) {
			return Lock{}, false, fmt.Errorf("invalid skill lock %s: invalid entry %q", filename, key)
		}
	}
	return lock, true, nil
}

// validLockPath rejects a hand-edited or hostile entry whose source embeds a
// subpath separator or whose path is not already in canonical, in-repo form.
func validLockPath(entry LockEntry) bool {
	if _, _, embedded := config.SplitSubpath(entry.Source); embedded {
		return false
	}
	if entry.Path == "" {
		return true
	}
	clean, err := config.CleanSkillPath(entry.Path)
	return err == nil && clean == entry.Path
}

func writeLock(filename string, lock Lock) error {
	// yaml.v3 sorts string map keys, making this stable for review and commits.
	b, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(filename), ".ocp.lock-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(append(b, '\n'))
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
	return os.Rename(name, filename)
}

type provider struct {
	dataDir string
	entries map[string]LockEntry
}

// SkillDir materializes the requested immutable checkout on demand and returns
// the skill directory: the repository root, or the selected subpath inside it.
func (p *provider) SkillDir(skill config.Skill) (string, error) {
	key := lockKey(skill.Source, skill.Path)
	entry, ok := p.entries[key]
	if !ok || entry.Ref != skill.Ref || entry.Path != skill.Path {
		return "", fmt.Errorf("Git skill %q is not locked for ref %q", skill.Source, skill.Ref)
	}
	dest, err := p.checkout(entry)
	if err != nil {
		return "", err
	}
	if entry.Path == "" {
		return dest, nil
	}
	return subpathDir(dest, entry)
}

// checkout returns a checkout of the entry's commit. The cache is keyed by
// commit, so skills from one repository at different commits never collide.
func (p *provider) checkout(entry LockEntry) (string, error) {
	dest := cachePath(p.dataDir, canonicalSource(entry.Source), entry.Commit)
	if checkoutAt(dest, entry.Commit) {
		return dest, nil
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := git(tmp, "init"); err != nil {
		return "", err
	}
	if err := git(tmp, "remote", "add", "origin", entry.Source); err != nil {
		return "", err
	}
	if err := git(tmp, "fetch", "--no-tags", "origin", entry.Commit); err != nil {
		return "", err
	}
	if err := git(tmp, "checkout", "--detach", entry.Commit); err != nil {
		return "", err
	}
	if !checkoutAt(tmp, entry.Commit) {
		return "", fmt.Errorf("Git skill %q checkout did not resolve to locked commit", entry.Source)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// subpathDir resolves the entry's subpath inside a checkout. It rejects
// symlinked components and anything that is not a directory holding SKILL.md,
// so a hostile repository cannot point a skill outside its own tree.
func subpathDir(root string, entry LockEntry) (string, error) {
	dir := root
	for _, part := range strings.Split(entry.Path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("Git skill %q: invalid path %q", entry.Source, entry.Path)
		}
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil {
			return "", fmt.Errorf("Git skill %q: path %q: %w", entry.Source, entry.Path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Git skill %q: path %q must not contain symlinks", entry.Source, entry.Path)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("Git skill %q: path %q is not a directory", entry.Source, entry.Path)
		}
	}
	info, err := os.Lstat(filepath.Join(dir, "SKILL.md"))
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Git skill %q: path %q has no SKILL.md", entry.Source, entry.Path)
	}
	return dir, nil
}

func resolve(source, ref string) (string, error) {
	args := []string{"ls-remote", source}
	if ref == "" {
		args = append(args, "HEAD")
	} else {
		args = append(args, ref)
	}
	out, err := gitOutput("", args...)
	if err != nil {
		return "", fmt.Errorf("resolve Git skill %q at %q: %w", source, ref, err)
	}
	var commit, peeled string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !commitPattern.MatchString(fields[0]) {
			continue
		}
		commit = fields[0]
		if strings.HasSuffix(fields[1], "^{}") {
			peeled = fields[0]
		}
	}
	if peeled != "" {
		commit = peeled
	}
	if commit == "" {
		return "", fmt.Errorf("resolve Git skill %q at %q: ref not found", source, ref)
	}
	return commit, nil
}

func checkoutAt(dir, commit string) bool {
	out, err := gitOutput(dir, "rev-parse", "HEAD")
	return err == nil && strings.TrimSpace(string(out)) == commit
}

func git(dir string, args ...string) error {
	_, err := gitOutput(dir, args...)
	return err
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	args = append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args[4:], " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func isGitSource(source string) bool {
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "ssh://")
}

func canonicalSource(source string) string {
	source = strings.TrimRight(source, "/")
	return strings.TrimSuffix(source, ".git")
}

func cachePath(dataDir, source, commit string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + commit))
	name := repositoryName(source)
	if name == "." || name == "/" || name == "" {
		name = "skill"
	}
	return filepath.Join(dataDir, "skills", hex.EncodeToString(sum[:8]), safeName(name))
}

func repositoryName(source string) string {
	if colon := strings.LastIndex(source, ":"); colon >= 0 && !strings.Contains(source[colon+1:], "/") {
		return strings.TrimSuffix(source[colon+1:], ".git")
	}
	return strings.TrimSuffix(path.Base(strings.TrimRight(source, "/")), ".git")
}

func safeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
