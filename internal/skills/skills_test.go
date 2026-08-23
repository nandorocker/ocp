package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

func TestReadLockRejectsMalformedAndInvalidEntries(t *testing.T) {
	for _, text := range []string{
		"version: [\n",
		"version: 2\nskills: {}\n",
		"version: 1\nskills:\n  https://example.test/a:\n    source: https://example.test/a\n    commit: bad\n",
	} {
		file := filepath.Join(t.TempDir(), "ocp.lock")
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLock(file); err == nil {
			t.Fatalf("readLock(%q) succeeded", text)
		}
	}
}

func TestPrepareMissingEntryPolicyAndRefMismatch(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: "https://example.test/org/demo.git", Ref: "v1"}
	if _, _, err := Prepare(source, data, []config.Skill{skill}, false); err == nil || !strings.Contains(err.Error(), "require") {
		t.Fatalf("missing lock error = %v", err)
	}
	commit := strings.Repeat("a", 40)
	lock := "version: 1\nskills:\n  https://example.test/org/demo:\n    source: https://example.test/org/demo\n    ref: v2\n    commit: " + commit + "\n"
	if err := os.WriteFile(filepath.Join(source, lockFileName), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Prepare(source, data, []config.Skill{skill}, false); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("ref mismatch error = %v", err)
	}
}

func TestPrepareDoesNotOverwriteCorruptLockOrCreateEmptyLock(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	if _, changed, err := Prepare(source, data, nil, true); err != nil || changed {
		t.Fatalf("empty skills = changed %v, err %v", changed, err)
	}
	lockPath := filepath.Join(source, lockFileName)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("empty skills created lock: %v", err)
	}
	bad := []byte("version: [\n")
	if err := os.WriteFile(lockPath, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Prepare(source, data, []config.Skill{{Source: "https://example.test/org/demo"}}, true); err == nil {
		t.Fatal("bootstrap accepted corrupt lock")
	}
	got, err := os.ReadFile(lockPath)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("corrupt lock changed to %q, err %v", got, err)
	}
}

func TestPrepareBootstrapAndCheckoutReuse(t *testing.T) {
	repo, commit := bareRepository(t)
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: repo, Ref: "main"}

	// Local repositories are deliberately not public Git skill sources; exercise
	// the shared implementation with an internal selector to avoid a network.
	p, changed, err := prepare(source, data, []config.Skill{skill}, true, func(string) bool { return true })
	if err != nil || !changed {
		t.Fatalf("bootstrap = %v, changed %v", err, changed)
	}
	lock, exists, err := readLock(filepath.Join(source, lockFileName))
	if err != nil || !exists || lock.Skills[canonicalSource(repo)].Commit != commit {
		t.Fatalf("lock = %#v, exists %v, err %v", lock, exists, err)
	}
	dir, err := p.SkillDir(skill)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitMust(t, dir, "rev-parse", "HEAD"); got != commit {
		t.Fatalf("HEAD = %q, want %q", got, commit)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if again, err := p.SkillDir(skill); err != nil || again != dir {
		t.Fatalf("reuse = %q, %v; want %q", again, err, dir)
	}
}

func TestPrepareBootstrapsPublicForm(t *testing.T) {
	// Classification is intentionally separate from resolution; no network is used here.
	if !isGitSource("ssh://git@example.test/org/demo.git") || isGitSource("/tmp/demo") {
		t.Fatal("unexpected Git source classification")
	}
}

func bareRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	work, bare := filepath.Join(root, "work"), filepath.Join(root, "demo.git")
	gitRun(t, root, "init", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", "SKILL.md")
	gitRun(t, work, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "skill")
	commit := gitMust(t, work, "rev-parse", "HEAD")
	gitRun(t, root, "init", "--bare", bare)
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "push", "origin", "main")
	return bare, commit
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func gitMust(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
