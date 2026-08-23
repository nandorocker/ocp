package ocp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TakeOver preserves an existing OpenCode configuration and installs the managed link.
// state.Source must be the canonical, absolute OCP source location.
func TakeOver(p Paths, profile string, state State) error {
	if err := profileExists(p, profile); err != nil {
		return err
	}
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Source == "" || !filepath.IsAbs(state.Source) {
		return errors.New("takeover state requires an absolute source")
	}
	if info, err := os.Lstat(p.OpenCode); err == nil {
		if managedTarget(p, p.OpenCode) != "" { // A resumed setup or a normal profile update.
			if err := Activate(p, profile); err != nil {
				return err
			}
			return SaveState(p, state)
		}
		_ = info
		preserved := sibling(p.OpenCode, "preserved")
		if err := os.Rename(p.OpenCode, preserved); err != nil {
			return fmt.Errorf("preserve existing OpenCode configuration: %w", err)
		}
		state.PreservedOriginal = preserved
		// Persist this before creating the link so a retry can safely finish setup.
		if err := SaveState(p, state); err != nil {
			if rollbackErr := os.Rename(preserved, p.OpenCode); rollbackErr != nil {
				return fmt.Errorf("save takeover state: %v; restore original configuration: %w", err, rollbackErr)
			}
			return fmt.Errorf("save takeover state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if state.PreservedOriginal != "" { // State was committed before a failed link creation.
		if _, err := os.Lstat(state.PreservedOriginal); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := replaceSymlink(p.OpenCode, filepath.Join(p.Current, profile)); err != nil {
		return err
	}
	return SaveState(p, state)
}

// Activate atomically changes only an existing OCP-managed OpenCode link.
func Activate(p Paths, profile string) error {
	if err := profileExists(p, profile); err != nil {
		return err
	}
	if managedTarget(p, p.OpenCode) == "" {
		return errors.New("OpenCode configuration is not managed by OCP")
	}
	return replaceSymlink(p.OpenCode, filepath.Join(p.Current, profile))
}

// ActiveProfile returns the profile named by the managed OpenCode symlink.
func ActiveProfile(p Paths) (string, error) {
	target := managedTarget(p, p.OpenCode)
	if target == "" {
		return "", nil
	}
	rel, err := filepath.Rel(p.Current, target)
	if err != nil || rel == "." || strings.Contains(rel, string(filepath.Separator)) {
		return "", nil
	}
	return rel, nil
}

// FallbackResult describes what ApplyFallback did after a new release is published.
type FallbackResult struct {
	Active   string
	FellBack bool
	NoActive bool
}

// ApplyFallback retains active when present, otherwise chooses default only. It never
// takes over a user-owned OpenCode path.
func ApplyFallback(p Paths, previous string) (FallbackResult, error) {
	if managedTarget(p, p.OpenCode) == "" {
		return FallbackResult{NoActive: true}, nil
	}
	if previous != "" && profileExists(p, previous) == nil {
		if err := Activate(p, previous); err != nil {
			return FallbackResult{}, err
		}
		return FallbackResult{Active: previous}, nil
	}
	if profileExists(p, "default") == nil {
		if err := Activate(p, "default"); err != nil {
			return FallbackResult{}, err
		}
		return FallbackResult{Active: "default", FellBack: true}, nil
	}
	if err := os.Remove(p.OpenCode); err != nil && !errors.Is(err, os.ErrNotExist) {
		return FallbackResult{}, err
	}
	return FallbackResult{NoActive: true}, nil
}

// Reset detaches OCP and restores the preserved original. It returns the remaining
// canonical source location for caller reporting.
func Reset(p Paths, force bool) (string, error) {
	state, err := LoadState(p)
	if err != nil {
		return "", err
	}
	if state == nil {
		return "", errors.New("OCP is not set up")
	}
	if managedTarget(p, p.OpenCode) == "" {
		if _, err := os.Lstat(p.OpenCode); err == nil {
			if state.PreservedOriginal == "" {
				if err := os.Remove(p.StateFile); err != nil && !errors.Is(err, os.ErrNotExist) {
					return "", err
				}
				return state.Source, nil
			}
			if !force {
				return "", errors.New("refusing to replace user-owned OpenCode configuration")
			}
			if err := os.Rename(p.OpenCode, sibling(p.OpenCode, "replacement")); err != nil {
				return "", err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	} else if err := os.Remove(p.OpenCode); err != nil {
		return "", err
	}
	if state.PreservedOriginal != "" {
		if _, err := os.Lstat(state.PreservedOriginal); err == nil {
			if _, err := os.Lstat(p.OpenCode); err == nil {
				return "", errors.New("OpenCode path unexpectedly exists")
			}
			if err := os.Rename(state.PreservedOriginal, p.OpenCode); err != nil {
				return "", err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := os.Remove(p.StateFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return state.Source, nil
}

// RunConfigPath returns the active OpenCode configuration directory for a run.
func RunConfigPath(p Paths, profile string) (string, error) {
	if err := profileExists(p, profile); err != nil {
		return "", err
	}
	return filepath.Join(p.Current, profile), nil
}

// ConfigPath is the configuration directory to pass to a one-off OpenCode run.
func ConfigPath(p Paths, profile string) (string, error) { return RunConfigPath(p, profile) }

func profileExists(p Paths, profile string) error {
	if profile == "" || filepath.Base(profile) != profile {
		return fmt.Errorf("invalid profile %q", profile)
	}
	info, err := os.Stat(filepath.Join(p.Current, profile))
	if err != nil {
		return fmt.Errorf("profile %q: %w", profile, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile %q is not a directory", profile)
	}
	return nil
}
func managedTarget(p Paths, link string) string {
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Clean(filepath.Join(filepath.Dir(link), target))
	}
	rel, err := filepath.Rel(p.Current, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return ""
	}
	return target
}
func sibling(path, kind string) string {
	return fmt.Sprintf("%s.ocp-%s-%d", path, kind, time.Now().UnixNano())
}
