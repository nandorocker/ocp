package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncNoGit(t *testing.T) {
	dir := t.TempDir()
	r, err := Sync(dir, false, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !r.NoGit {
		t.Fatalf("result = %+v, want NoGit", r)
	}
}

func TestSyncRequiresSourceAtWorktreeRoot(t *testing.T) {
	local, _ := repository(t)
	nested := filepath.Join(local, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(nested, true, "sync"); err == nil || !strings.Contains(err.Error(), "worktree root") {
		t.Fatalf("nested source error = %v", err)
	}
}

func TestSyncCleanAndLocalAhead(t *testing.T) {
	local, _ := repository(t)
	r, err := Sync(local, false, "sync")
	if err != nil || r != (Result{}) {
		t.Fatalf("clean sync = %+v, %v", r, err)
	}
	write(t, filepath.Join(local, "local.txt"), "local\n")
	r, err = Sync(local, true, "add local")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Committed || !r.Pushed || r.Fetched || r.Merged {
		t.Fatalf("local ahead result = %+v", r)
	}
}

func TestSyncFastForwardsRemoteAdvance(t *testing.T) {
	local, bare := repository(t)
	remote := clone(t, bare)
	write(t, filepath.Join(remote, "remote.txt"), "remote\n")
	gitOK(t, remote, "add", "-A")
	gitOK(t, remote, "commit", "-m", "remote")
	gitOK(t, remote, "push")

	r, err := Sync(local, false, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fetched || r.Merged || r.Pushed {
		t.Fatalf("result = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(local, "remote.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncMergesDivergence(t *testing.T) {
	local, bare := repository(t)
	write(t, filepath.Join(local, "local.txt"), "local\n")
	gitOK(t, local, "add", "-A")
	gitOK(t, local, "commit", "-m", "local")
	remote := clone(t, bare)
	write(t, filepath.Join(remote, "remote.txt"), "remote\n")
	gitOK(t, remote, "add", "-A")
	gitOK(t, remote, "commit", "-m", "remote")
	gitOK(t, remote, "push")

	r, err := Sync(local, false, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Fetched || !r.Merged || !r.Pushed {
		t.Fatalf("result = %+v", r)
	}
}

func TestSyncLeavesMergeConflictsForManualResolution(t *testing.T) {
	local, bare := repository(t)
	write(t, filepath.Join(local, "shared.txt"), "local\n")
	gitOK(t, local, "add", "-A")
	gitOK(t, local, "commit", "-m", "local")
	remote := clone(t, bare)
	write(t, filepath.Join(remote, "shared.txt"), "remote\n")
	gitOK(t, remote, "add", "-A")
	gitOK(t, remote, "commit", "-m", "remote")
	gitOK(t, remote, "push")

	_, err := Sync(local, false, "sync")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("error = %v, want conflict", err)
	}
	out := gitOK(t, local, "diff", "--name-only", "--diff-filter=U")
	if strings.TrimSpace(out) != "shared.txt" {
		t.Fatalf("unmerged files = %q", out)
	}
}

func TestSyncRejectsInvalidRepositoryStatesBeforeMutation(t *testing.T) {
	t.Run("detached head", func(t *testing.T) {
		local, _ := repository(t)
		gitOK(t, local, "checkout", "--detach")
		if _, err := Sync(local, true, "sync"); err == nil || !strings.Contains(err.Error(), "attached branch") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing upstream", func(t *testing.T) {
		local, _ := repository(t)
		gitOK(t, local, "branch", "--unset-upstream")
		if _, err := Sync(local, true, "sync"); err == nil || !strings.Contains(err.Error(), "upstream") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("dirty manual mode", func(t *testing.T) {
		local, _ := repository(t)
		write(t, filepath.Join(local, "dirty.txt"), "dirty\n")
		gitOK(t, local, "add", "dirty.txt")
		if _, err := Sync(local, false, "sync"); err == nil || !strings.Contains(err.Error(), "tracked or untracked") {
			t.Fatalf("error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(local, "dirty.txt")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unresolved merge", func(t *testing.T) {
		local, bare := repository(t)
		write(t, filepath.Join(local, "shared.txt"), "local\n")
		gitOK(t, local, "add", "-A")
		gitOK(t, local, "commit", "-m", "local")
		remote := clone(t, bare)
		write(t, filepath.Join(remote, "shared.txt"), "remote\n")
		gitOK(t, remote, "add", "-A")
		gitOK(t, remote, "commit", "-m", "remote")
		gitOK(t, remote, "push")
		gitOK(t, local, "fetch")
		gitFail(t, local, "merge", "--no-edit", "@{upstream}")
		if _, err := Sync(local, true, "sync"); err == nil || !strings.Contains(err.Error(), "unresolved merge conflicts") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestSyncReportsPushFailure(t *testing.T) {
	local, bare := repository(t)
	write(t, filepath.Join(local, "local.txt"), "local\n")
	hook := filepath.Join(bare, "hooks", "pre-receive")
	write(t, hook, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Sync(local, true, "local")
	if err == nil || !strings.Contains(err.Error(), "push upstream") {
		t.Fatalf("error = %v", err)
	}
}

func TestSyncMergesUnrelatedHistoriesOnAdoption(t *testing.T) {
	// A machine that started with a local-only source and later adopted the
	// shared repository has no common ancestor with it.
	shared := filepath.Join(t.TempDir(), "shared.git")
	gitOK(t, "", "init", "--bare", "--initial-branch=main", shared)
	seed := clone(t, shared)
	write(t, filepath.Join(seed, "ocp.yaml"), "version: 1\n")
	gitOK(t, seed, "add", "-A")
	gitOK(t, seed, "commit", "-m", "shared config")
	gitOK(t, seed, "push", "-u", "origin", "HEAD")

	local := filepath.Join(t.TempDir(), "local")
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	gitOK(t, local, "init", "-b", "main")
	gitOK(t, local, "config", "user.name", "Test User")
	gitOK(t, local, "config", "user.email", "test@example.com")
	write(t, filepath.Join(local, "ocp.yaml"), "version: 1\n")
	write(t, filepath.Join(local, "local-note.md"), "local\n")
	gitOK(t, local, "add", "-A")
	gitOK(t, local, "commit", "-m", "local config")
	gitOK(t, local, "remote", "add", "origin", shared)
	gitOK(t, local, "fetch", "origin")
	gitOK(t, local, "branch", "--set-upstream-to=origin/main", "main")

	r, err := Sync(local, false, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Merged || !r.Pushed {
		t.Fatalf("result = %+v, want merged and pushed", r)
	}
	for _, name := range []string{"ocp.yaml", "local-note.md"} {
		if _, err := os.Stat(filepath.Join(local, name)); err != nil {
			t.Fatalf("missing %s after merge: %v", name, err)
		}
	}
	// The shared history must be reachable so later syncs fast-forward cleanly.
	gitOK(t, local, "merge-base", "--is-ancestor", "origin/main", "HEAD")
}

func repository(t *testing.T) (local, bare string) {
	t.Helper()
	bare = filepath.Join(t.TempDir(), "remote.git")
	gitOK(t, "", "init", "--bare", bare)
	local = clone(t, bare)
	write(t, filepath.Join(local, "base.txt"), "base\n")
	gitOK(t, local, "add", "-A")
	gitOK(t, local, "commit", "-m", "base")
	gitOK(t, local, "push", "-u", "origin", "HEAD")
	return local, bare
}

func clone(t *testing.T, bare string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	gitOK(t, "", "clone", bare, dir)
	gitOK(t, dir, "config", "user.name", "Test User")
	gitOK(t, dir, "config", "user.email", "test@example.com")
	return dir
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitOK(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func gitFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	args = append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("git %s unexpectedly succeeded\n%s", strings.Join(args, " "), out)
	}
}
