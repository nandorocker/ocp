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

func collectionRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	work, bare := filepath.Join(root, "work"), filepath.Join(root, "coll.git")
	gitRun(t, root, "init", "-b", "main", work)
	for _, name := range []string{"skills/pdf/SKILL.md", "skills/docx/SKILL.md", "skills/nomd/README.md"} {
		file := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("skills/pdf", filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", ".")
	gitRun(t, work, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "skills")
	commit := gitMust(t, work, "rev-parse", "HEAD")
	gitRun(t, root, "init", "--bare", bare)
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, work, "push", "origin", "main")
	return bare, commit
}

func TestSubpathSkillsLockPerPathAndShareCheckout(t *testing.T) {
	repo, commit := collectionRepository(t)
	source, data := t.TempDir(), t.TempDir()
	pdf := config.Skill{Source: repo, Path: "skills/pdf", Ref: "main"}
	docx := config.Skill{Source: repo, Path: "skills/docx", Ref: "main"}
	p, changed, err := prepare(source, data, []config.Skill{pdf, docx}, true, func(string) bool { return true })
	if err != nil || !changed {
		t.Fatalf("bootstrap = %v, changed %v", err, changed)
	}
	lock, _, err := readLock(filepath.Join(source, lockFileName))
	if err != nil || len(lock.Skills) != 2 || lock.Skills[lockKey(repo, "skills/pdf")].Commit != commit || lock.Skills[lockKey(repo, "skills/docx")].Path != "skills/docx" {
		t.Fatalf("lock = %#v, err %v", lock, err)
	}
	pdfDir, err := p.SkillDir(pdf)
	if err != nil || filepath.Base(pdfDir) != "pdf" {
		t.Fatalf("pdf dir = %q, %v", pdfDir, err)
	}
	docxDir, err := p.SkillDir(docx)
	if err != nil || filepath.Dir(docxDir) != filepath.Dir(pdfDir) {
		t.Fatalf("docx dir = %q, %v; want sibling of %q", docxDir, err, pdfDir)
	}
	// A second prepare reuses the lock without needing the network or changing it.
	if _, changed, err := prepare(source, data, []config.Skill{pdf, docx}, false, func(string) bool { return true }); err != nil || changed {
		t.Fatalf("reprepare = %v, changed %v", err, changed)
	}
}

func TestSubpathSkillRejectsMissingSymlinkAndNoSkillMD(t *testing.T) {
	repo, _ := collectionRepository(t)
	for _, path := range []string{"skills/missing", "link", "skills/nomd", "skills/pdf/SKILL.md"} {
		skill := config.Skill{Source: repo, Path: path, Ref: "main"}
		p, _, err := prepare(t.TempDir(), t.TempDir(), []config.Skill{skill}, true, func(string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if dir, err := p.SkillDir(skill); err == nil {
			t.Errorf("path %q accepted, dir %q", path, dir)
		}
	}
}

func TestReadLockKeyMustMatchSourceAndPath(t *testing.T) {
	commit := strings.Repeat("a", 40)
	file := filepath.Join(t.TempDir(), "ocp.lock")
	text := "version: 1\nskills:\n  https://example.test/a:\n    source: https://example.test/a\n    path: sub\n    commit: " + commit + "\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLock(file); err == nil {
		t.Fatal("lock key without path accepted for entry with path")
	}
}

func TestReadLockRejectsEscapingOrEmbeddedPaths(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for name, text := range map[string]string{
		"dotdot":   "https://example.test/a//../x:\n    source: https://example.test/a\n    path: ../x\n",
		"unclean":  "https://example.test/a//x/./y:\n    source: https://example.test/a\n    path: x/./y\n",
		"embedded": "https://example.test/a//../x:\n    source: https://example.test/a//../x\n",
	} {
		file := filepath.Join(t.TempDir(), "ocp.lock")
		body := "version: 1\nskills:\n  " + text + "    commit: " + commit + "\n"
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLock(file); err == nil {
			t.Errorf("%s lock entry accepted", name)
		}
	}
}

func TestPruneCacheRemovesUnreferencedCheckoutsOnly(t *testing.T) {
	repo, commit := bareRepository(t)
	source, data := t.TempDir(), t.TempDir()
	stale := filepath.Join(data, "skills", "0123456789abcdef", "old")
	other := filepath.Join(data, "skills", "not-a-cache-dir")
	for _, dir := range []string{stale, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	skill := config.Skill{Source: repo, Ref: "main"}
	p, _, err := prepare(source, data, []config.Skill{skill}, true, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	kept, err := p.SkillDir(skill)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepare(source, data, []config.Skill{skill}, false, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale checkout survived: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("non-cache directory removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kept, "SKILL.md")); err != nil {
		t.Errorf("referenced checkout removed: %v", err)
	}
	_ = commit
}

func TestPrepareLocksFullCommitRef(t *testing.T) {
	repo, commit := bareRepository(t)
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: repo, Ref: commit}
	p, changed, err := prepare(source, data, []config.Skill{skill}, true, func(string) bool { return true })
	if err != nil || !changed {
		t.Fatalf("bootstrap commit ref = %v, changed %v", err, changed)
	}
	dir, err := p.SkillDir(skill)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
