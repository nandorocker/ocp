package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

// upstream is a bare repository with a working clone used to publish commits.
type upstream struct {
	t          *testing.T
	work, bare string
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	root := t.TempDir()
	u := &upstream{t: t, work: filepath.Join(root, "work"), bare: filepath.Join(root, "up.git")}
	gitRun(t, root, "init", "-b", "main", u.work)
	gitRun(t, root, "init", "--bare", "-b", "main", u.bare)
	gitRun(t, u.work, "remote", "add", "origin", u.bare)
	return u
}

// commit writes files (path -> content; empty content deletes) and pushes main.
func (u *upstream) commit(files map[string]string) string {
	u.t.Helper()
	for name, content := range files {
		file := filepath.Join(u.work, filepath.FromSlash(name))
		if content == "" {
			gitRun(u.t, u.work, "rm", "-rq", name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			u.t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			u.t.Fatal(err)
		}
		gitRun(u.t, u.work, "add", name)
	}
	gitRun(u.t, u.work, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-qm", "update")
	gitRun(u.t, u.work, "push", "-q", "origin", "main")
	return gitMust(u.t, u.work, "rev-parse", "HEAD")
}

func (u *upstream) tag(name string) {
	u.t.Helper()
	gitRun(u.t, u.work, "tag", name)
	gitRun(u.t, u.work, "push", "-q", "origin", name)
}

func allSources(string) bool { return true }

// lockAll bootstraps the lock for skills the way `ocp sync` does.
func lockAll(t *testing.T, source, data string, skills []config.Skill) {
	t.Helper()
	if _, _, err := prepare(source, data, skills, true, allSources); err != nil {
		t.Fatal(err)
	}
}

func lockedCommit(t *testing.T, source string, s config.Skill) string {
	t.Helper()
	lock, _, err := readLock(filepath.Join(source, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	return lock.Skills[lockKey(s.Source, s.Path)].Commit
}

func TestUpgradeAdvancesBranchAndDefaultButNotPinnedRefs(t *testing.T) {
	// One repository cannot be locked twice with different refs, so each
	// declaration gets its own upstream.
	u, other, tagged, pinned := newUpstream(t), newUpstream(t), newUpstream(t), newUpstream(t)
	first := u.commit(map[string]string{"SKILL.md": "one"})
	other.commit(map[string]string{"SKILL.md": "one"})
	tagged.commit(map[string]string{"SKILL.md": "one"})
	tagged.tag("v1")
	pinnedFirst := pinned.commit(map[string]string{"SKILL.md": "one"})
	source, data := t.TempDir(), t.TempDir()
	followDefault := config.Skill{Source: u.bare}
	followBranch := config.Skill{Source: other.bare, Ref: "main"}
	pinnedTag := config.Skill{Source: tagged.bare, Ref: "v1"}
	pinnedCommit := config.Skill{Source: pinned.bare, Ref: pinnedFirst}

	skills := []config.Skill{followDefault, followBranch, pinnedTag, pinnedCommit}
	lockAll(t, source, data, skills)

	second := u.commit(map[string]string{"SKILL.md": "two"})
	otherSecond := other.commit(map[string]string{"SKILL.md": "two"})
	tagged.commit(map[string]string{"SKILL.md": "two"})
	pinned.commit(map[string]string{"SKILL.md": "two"})

	plan, err := planUpgrade(source, data, skills, "", allSources)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]UpgradeStatus{u.bare: Advanced, other.bare: Advanced, tagged.bare: Pinned, pinned.bare: Pinned}
	if len(plan.Results) != len(want) {
		t.Fatalf("results = %#v", plan.Results)
	}
	for _, r := range plan.Results {
		if r.Status != want[r.Source] {
			t.Fatalf("%s status = %v, want %v", r.Source, r.Status, want[r.Source])
		}
	}
	if lockedCommit(t, source, followDefault) != first {
		t.Fatal("planning changed the lock")
	}
	if err := plan.Fetch(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(); err != nil {
		t.Fatal(err)
	}
	if got := lockedCommit(t, source, followDefault); got != second {
		t.Fatalf("default-branch skill = %s, want %s", got, second)
	}
	if got := lockedCommit(t, source, followBranch); got != otherSecond {
		t.Fatalf("branch skill = %s, want %s", got, otherSecond)
	}
	if got := lockedCommit(t, source, pinnedCommit); got != pinnedFirst {
		t.Fatalf("commit-pinned skill moved to %s", got)
	}
	// The written lock is still valid for rendering.
	if _, _, err := prepare(source, data, skills, false, allSources); err != nil {
		t.Fatalf("prepare after upgrade: %v", err)
	}
}

func TestUpgradeCurrentSkillReportsUpToDate(t *testing.T) {
	u := newUpstream(t)
	u.commit(map[string]string{"SKILL.md": "one"})
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: u.bare}
	lockAll(t, source, data, []config.Skill{skill})
	plan, err := planUpgrade(source, data, []config.Skill{skill}, "", allSources)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed() || len(plan.Results) != 1 || plan.Results[0].Status != Current {
		t.Fatalf("plan = %#v", plan.Results)
	}
}

func TestUpgradeSingleSkillTouchesOnlyThatEntry(t *testing.T) {
	u := newUpstream(t)
	first := u.commit(map[string]string{"skills/pdf/SKILL.md": "pdf", "skills/docx/SKILL.md": "docx"})
	source, data := t.TempDir(), t.TempDir()
	pdf := config.Skill{Source: u.bare, Path: "skills/pdf"}
	docx := config.Skill{Source: u.bare, Path: "skills/docx"}
	skills := []config.Skill{pdf, docx}
	lockAll(t, source, data, skills)
	second := u.commit(map[string]string{"skills/pdf/SKILL.md": "pdf two"})

	plan, err := planUpgrade(source, data, skills, "pdf", allSources)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Results) != 1 || plan.Results[0].Name != "pdf" {
		t.Fatalf("results = %#v", plan.Results)
	}
	if err := plan.Fetch(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(); err != nil {
		t.Fatal(err)
	}
	if lockedCommit(t, source, pdf) != second || lockedCommit(t, source, docx) != first {
		t.Fatal("single-skill upgrade changed the wrong entries")
	}
}

func TestUpgradeNamedSkillErrors(t *testing.T) {
	u := newUpstream(t)
	u.commit(map[string]string{"SKILL.md": "one"})
	u.tag("v1")
	source, data := t.TempDir(), t.TempDir()
	pinned := config.Skill{Source: u.bare, Ref: "v1"}
	lockAll(t, source, data, []config.Skill{pinned})
	name := pinned.Name()
	if _, err := planUpgrade(source, data, []config.Skill{pinned}, name, allSources); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("pinned error = %v", err)
	}
	if _, err := planUpgrade(source, data, []config.Skill{pinned}, "missing", allSources); err == nil || !strings.Contains(err.Error(), "no Git skill named") {
		t.Fatalf("unknown error = %v", err)
	}
	a := config.Skill{Source: "https://example.test/a/tool"}
	b := config.Skill{Source: "https://example.test/b/tool"}
	if _, err := planUpgrade(source, data, []config.Skill{a, b}, "tool", allSources); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous error = %v", err)
	}
}

func TestUpgradeRequiresLockEntry(t *testing.T) {
	u := newUpstream(t)
	u.commit(map[string]string{"SKILL.md": "one"})
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: u.bare}
	if _, err := planUpgrade(source, data, []config.Skill{skill}, "", allSources); err == nil || !strings.Contains(err.Error(), "ocp sync") {
		t.Fatalf("missing lock error = %v", err)
	}
	lockAll(t, source, data, []config.Skill{skill})
	other := newUpstream(t)
	other.commit(map[string]string{"SKILL.md": "one"})
	unlocked := config.Skill{Source: other.bare}
	if _, err := planUpgrade(source, data, []config.Skill{skill, unlocked}, "", allSources); err == nil || !strings.Contains(err.Error(), "not in ocp.lock") {
		t.Fatalf("unlocked entry error = %v", err)
	}
}

func TestUpgradeFetchFailureLeavesLockAndRestoreUndoesWrite(t *testing.T) {
	u := newUpstream(t)
	u.commit(map[string]string{"skills/pdf/SKILL.md": "pdf"})
	source, data := t.TempDir(), t.TempDir()
	pdf := config.Skill{Source: u.bare, Path: "skills/pdf"}
	lockAll(t, source, data, []config.Skill{pdf})
	lockPath := filepath.Join(source, lockFileName)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Upstream removes the skill folder: the new commit cannot be used.
	u.commit(map[string]string{"skills/pdf": "", "README.md": "moved"})
	plan, err := planUpgrade(source, data, []config.Skill{pdf}, "", allSources)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Fetch(); err == nil {
		t.Fatal("fetch accepted a commit without the skill")
	}
	if after, _ := os.ReadFile(lockPath); string(after) != string(before) {
		t.Fatal("failed fetch changed the lock")
	}

	u.commit(map[string]string{"skills/pdf/SKILL.md": "pdf back"})
	plan, err = planUpgrade(source, data, []config.Skill{pdf}, "", allSources)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Fetch(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Write(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(lockPath); string(after) == string(before) {
		t.Fatal("write did not change the lock")
	}
	if err := plan.Restore(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(lockPath); string(after) != string(before) {
		t.Fatal("restore did not put back the original lock")
	}
}
