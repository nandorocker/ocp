// Package repository synchronizes an OCP source worktree with its upstream.
package repository

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result describes the repository actions completed by Sync.
type Result struct {
	NoGit     bool
	Committed bool
	Fetched   bool
	Merged    bool
	Pushed    bool
}

// Sync commits (when requested), fetches, reconciles, and pushes source.
// It never rebases, resets, discards changes, or force-pushes.
func Sync(source string, autoCommit bool, message string) (Result, error) {
	root, err := worktreeRoot(source)
	if errors.Is(err, errNoGit) {
		return Result{NoGit: true}, nil
	}
	if err != nil {
		return Result{}, err
	}
	sourcePath, err := filepath.EvalSymlinks(source)
	if err != nil {
		return Result{}, fmt.Errorf("resolve canonical source: %w", err)
	}
	rootPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve Git worktree root: %w", err)
	}
	if filepath.Clean(sourcePath) != filepath.Clean(rootPath) {
		return Result{}, errors.New("canonical source must be the Git worktree root")
	}

	branch, err := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("repository must be on an attached branch: %w", err)
	}
	branch = strings.TrimSpace(branch)
	if err := requireUpstream(root, branch); err != nil {
		return Result{}, err
	}
	if conflicted, err := hasConflicts(root); err != nil {
		return Result{}, err
	} else if conflicted {
		return Result{}, errors.New("repository has unresolved merge conflicts")
	}

	result := Result{}
	if autoCommit {
		committed, err := commitIfChanged(root, message)
		if err != nil {
			return result, err
		}
		result.Committed = committed
	} else if dirty, err := hasChanges(root); err != nil {
		return result, err
	} else if dirty {
		return result, errors.New("repository has tracked or untracked changes; enable auto-commit or commit them before syncing")
	}

	before, _ := upstreamOID(root)
	if _, err := git(root, "fetch"); err != nil {
		return result, fmt.Errorf("fetch upstream: %w", err)
	}
	after, err := upstreamOID(root)
	if err != nil {
		return result, fmt.Errorf("resolve upstream after fetch: %w", err)
	}
	result.Fetched = before != after

	local, remote, err := divergence(root)
	if err != nil {
		return result, err
	}
	if remote > 0 {
		if local == 0 {
			if _, err := git(root, "merge", "--ff-only", "@{upstream}"); err != nil {
				return result, fmt.Errorf("fast-forward upstream: %w", err)
			}
		} else {
			if _, err := git(root, "merge", "--no-edit", "@{upstream}"); err != nil {
				conflicted, conflictErr := hasConflicts(root)
				if conflictErr != nil {
					return result, conflictErr
				}
				if conflicted {
					return result, fmt.Errorf("merge upstream has conflicts requiring manual resolution: %w", err)
				}
				return result, fmt.Errorf("merge upstream: %w", err)
			}
			result.Merged = true
		}
	}

	local, _, err = divergence(root)
	if err != nil {
		return result, err
	}
	if local > 0 {
		if _, err := git(root, "push"); err != nil {
			return result, fmt.Errorf("push upstream: %w", err)
		}
		result.Pushed = true
	}
	return result, nil
}

var errNoGit = errors.New("not a Git worktree")

func worktreeRoot(source string) (string, error) {
	out, err := git(source, "rev-parse", "--show-toplevel")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", errNoGit
		}
		return "", fmt.Errorf("inspect Git worktree: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func requireUpstream(root, branch string) error {
	remotes, err := git(root, "config", "--get-all", "branch."+branch+".remote")
	if err != nil {
		return errors.New("repository branch has no configured upstream")
	}
	merges, err := git(root, "config", "--get-all", "branch."+branch+".merge")
	if err != nil || lineCount(remotes) != 1 || lineCount(merges) != 1 {
		return errors.New("repository branch must have exactly one configured upstream")
	}
	return nil
}

func lineCount(s string) int {
	return len(strings.Fields(s))
}

func hasChanges(root string) (bool, error) {
	out, err := git(root, "status", "--porcelain")
	return strings.TrimSpace(out) != "", err
}

func hasConflicts(root string) (bool, error) {
	out, err := git(root, "diff", "--name-only", "--diff-filter=U")
	return strings.TrimSpace(out) != "", err
}

func commitIfChanged(root, message string) (bool, error) {
	if _, err := git(root, "add", "-A"); err != nil {
		return false, fmt.Errorf("stage repository changes: %w", err)
	}
	_, err := git(root, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false, fmt.Errorf("inspect staged changes: %w", err)
	}
	if _, err := git(root, "commit", "-m", message); err != nil {
		return false, fmt.Errorf("commit repository changes: %w", err)
	}
	return true, nil
}

func upstreamOID(root string) (string, error) {
	out, err := git(root, "rev-parse", "--verify", "@{upstream}")
	return strings.TrimSpace(out), err
}

func divergence(root string) (local, remote int, err error) {
	out, err := git(root, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return 0, 0, fmt.Errorf("compute upstream divergence: %w", err)
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d\t%d", &local, &remote); err != nil {
		return 0, 0, fmt.Errorf("parse upstream divergence %q: %w", strings.TrimSpace(out), err)
	}
	return local, remote, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
