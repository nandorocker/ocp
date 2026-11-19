package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nando/ocp/internal/config"
)

// UpgradeStatus says what an upgrade does to one locked Git skill.
type UpgradeStatus int

const (
	// Advanced means the skill's branch moved and the lock will follow it.
	Advanced UpgradeStatus = iota
	// Current means the locked commit is already the branch tip.
	Current
	// Pinned means the skill names a tag or commit and is never advanced.
	Pinned
)

// UpgradeResult reports one locked Git skill.
type UpgradeResult struct {
	Name   string
	Source string
	Path   string
	Ref    string
	Old    string
	New    string
	Status UpgradeStatus
}

// UpgradePlan is a resolved upgrade. Planning reads the lock and the remotes
// but writes nothing; Fetch, Write and Restore perform the changes.
type UpgradePlan struct {
	Results  []UpgradeResult
	lockPath string
	dataDir  string
	original []byte
	lock     Lock
}

// Changed reports whether any skill would move to a new commit.
func (p *UpgradePlan) Changed() bool {
	for _, r := range p.Results {
		if r.Status == Advanced {
			return true
		}
	}
	return false
}

// PlanUpgrade resolves which Git skills can advance. With name set, only
// skills rendered under that name are considered, and naming a pinned or
// unknown skill is an error.
func PlanUpgrade(sourceDir, dataDir string, skills []config.Skill, name string) (*UpgradePlan, error) {
	return planUpgrade(sourceDir, dataDir, skills, name, isGitSource)
}

func planUpgrade(sourceDir, dataDir string, skills []config.Skill, name string, include func(string) bool) (*UpgradePlan, error) {
	lockPath := filepath.Join(sourceDir, lockFileName)
	byKey := map[string]config.Skill{}
	var keys []string
	for _, s := range skills {
		if !include(s.Source) {
			continue
		}
		key := lockKey(s.Source, s.Path)
		if prev, ok := byKey[key]; ok {
			if prev.Ref != s.Ref {
				return nil, fmt.Errorf("Git skill %q is declared with conflicting refs", s.Source)
			}
			continue
		}
		byKey[key] = s
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if name != "" {
		var matched []string
		for _, k := range keys {
			if byKey[k].Name() == name {
				matched = append(matched, k)
			}
		}
		switch len(matched) {
		case 0:
			return nil, fmt.Errorf("no Git skill named %q; local skills are not versioned by OCP", name)
		case 1:
		default:
			return nil, fmt.Errorf("skill name %q is ambiguous: %s", name, strings.Join(matched, ", "))
		}
		keys = matched
	}
	plan := &UpgradePlan{lockPath: lockPath, dataDir: dataDir}
	if len(keys) == 0 {
		return plan, nil
	}
	original, err := os.ReadFile(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("Git skills are not locked yet; run `ocp sync` first")
	}
	if err != nil {
		return nil, fmt.Errorf("read skill lock: %w", err)
	}
	lock, _, err := readLock(lockPath)
	if err != nil {
		return nil, err
	}
	plan.original, plan.lock = original, lock
	for _, key := range keys {
		s := byKey[key]
		entry, ok := lock.Skills[key]
		if !ok {
			return nil, fmt.Errorf("Git skill %q is not in %s yet; run `ocp sync` first", s.Name(), lockFileName)
		}
		if entry.Ref != s.Ref {
			return nil, fmt.Errorf("Git skill %q ref %q does not match lock ref %q; run `ocp sync` first", s.Name(), s.Ref, entry.Ref)
		}
		result := UpgradeResult{Name: s.Name(), Source: s.Source, Path: s.Path, Ref: s.Ref, Old: entry.Commit, New: entry.Commit, Status: Current}
		commit, branch, err := branchTip(s.Source, s.Ref)
		if err != nil {
			return nil, err
		}
		if !branch {
			if name != "" {
				return nil, fmt.Errorf("Git skill %q is pinned to %q; change its ref in the source to move it", s.Name(), s.Ref)
			}
			result.Status = Pinned
		} else if commit != entry.Commit {
			result.New, result.Status = commit, Advanced
			entry.Commit = commit
			lock.Skills[key] = entry
		}
		plan.Results = append(plan.Results, result)
	}
	return plan, nil
}

// branchTip resolves a skill's ref when it follows a branch. An empty ref
// follows the remote's default branch. A tag, or a full commit ID, is pinned;
// a name that is both a tag and a branch counts as the tag, matching Git's
// own precedence.
func branchTip(source, ref string) (string, bool, error) {
	if ref == "" {
		commit, err := resolve(source, "")
		return commit, err == nil, err
	}
	if commitPattern.MatchString(ref) {
		return "", false, nil
	}
	branchName := "refs/heads/" + strings.TrimPrefix(ref, "refs/heads/")
	tagName := "refs/tags/" + strings.TrimPrefix(ref, "refs/tags/")
	out, err := gitOutput("", "ls-remote", source, ref)
	if err != nil {
		return "", false, fmt.Errorf("resolve Git skill %q at %q: %w", source, ref, err)
	}
	var branchCommit string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !commitPattern.MatchString(fields[0]) {
			continue
		}
		switch strings.TrimSuffix(fields[1], "^{}") {
		case tagName:
			return "", false, nil
		case branchName:
			branchCommit = fields[0]
		}
	}
	if branchCommit == "" {
		return "", false, fmt.Errorf("resolve Git skill %q at %q: ref not found", source, ref)
	}
	return branchCommit, true, nil
}

// Fetch checks out every advanced commit into the cache and checks that a
// subpath skill still exists there, so a broken upstream fails before the
// lock changes.
func (p *UpgradePlan) Fetch() error {
	pr := &provider{dataDir: p.dataDir}
	for _, r := range p.Results {
		if r.Status != Advanced {
			continue
		}
		entry := LockEntry{Source: r.Source, Path: r.Path, Ref: r.Ref, Commit: r.New}
		dest, err := pr.checkout(entry)
		if err != nil {
			return err
		}
		if entry.Path != "" {
			if _, err := subpathDir(dest, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// Write records the advanced commits in the lock atomically.
func (p *UpgradePlan) Write() error {
	if !p.Changed() {
		return nil
	}
	return writeLock(p.lockPath, p.lock)
}

// Restore puts back the lock exactly as it was before Write.
func (p *UpgradePlan) Restore() error {
	if p.original == nil {
		return nil
	}
	return writeLockBytes(p.lockPath, p.original)
}
