package ocp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func source(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ocp.yaml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func render(t *testing.T, p Paths, src string, force bool) RenderResult {
	t.Helper()
	r, err := Render(RenderOptions{Paths: p, Source: src, Force: force})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRenderActivateResetAndRunPath(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\nconfig:\n  model: test\n")
	render(t, p, src, false)
	if err := os.MkdirAll(p.OpenCode, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.OpenCode, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := TakeOver(p, "default", State{Source: abs, AutoCommit: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ActiveProfile(p); got != "default" {
		t.Fatalf("active = %q", got)
	}
	path, err := RunConfigPath(p, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "opencode.json")); err != nil {
		t.Fatal(err)
	}
	remaining, err := Reset(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != abs {
		t.Fatalf("remaining = %q, want %q", remaining, abs)
	}
	if _, err := os.Stat(filepath.Join(p.OpenCode, "old")); err != nil {
		t.Fatal(err)
	}
}

func TestRenderMaterializesNativeFiles(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\nconfig:\n  plugin: [native]\ninstructions: [guide.md]\nplugins: [added, native]\nagents:\n  helper:\n    file: agents/helper.md\n    config:\n      model: small\nskills: [skills/demo]\n")
	for name, contents := range map[string]string{"guide.md": "be careful", "agents/helper.md": "agent", "skills/demo/SKILL.md": "skill"} {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	render(t, p, src, false)
	root := filepath.Join(p.Current, "default")
	if b, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err != nil || string(b) != "be careful\n" {
		t.Fatalf("AGENTS.md = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "helper.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if err := json.Unmarshal(b, &native); err != nil {
		t.Fatal(err)
	}
	if got := native["plugin"].([]any); len(got) != 2 || got[0] != "native" || got[1] != "added" {
		t.Fatalf("plugin = %#v", got)
	}
	if native["agent"].(map[string]any)["helper"].(map[string]any)["model"] != "small" {
		t.Fatalf("agent = %#v", native["agent"])
	}
}

func TestRenderSkillOmitsGitMetadataAndPreservesExecutableBit(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\nskills: [skills/demo]\n")
	write := func(name, contents string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/demo/SKILL.md", "skill", 0o600)
	write("skills/demo/run.sh", "#!/bin/sh\n", 0o700)
	write("skills/demo/.git/config", "metadata", 0o600)
	render(t, p, src, false)
	root := filepath.Join(p.Current, "default", "skills", "demo")
	if _, err := os.Stat(filepath.Join(root, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("copied .git metadata: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("executable mode = %v", info.Mode().Perm())
	}
}

func TestRenderFailurePreservesCurrent(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\n")
	render(t, p, src, false)
	before, err := os.Readlink(p.Current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ocp.yaml"), []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(RenderOptions{Paths: p, Source: src}); err == nil {
		t.Fatal("Render succeeded")
	}
	after, err := os.Readlink(p.Current)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("current changed from %q to %q", before, after)
	}
}

func TestDriftRefusalAndForce(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\n")
	render(t, p, src, false)
	if err := os.WriteFile(filepath.Join(p.Current, "default", "opencode.json"), []byte("{\"model\":\"changed\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Render(RenderOptions{Paths: p, Source: src})
	var drift *DriftError
	if !errors.As(err, &drift) || len(drift.Paths) != 1 {
		t.Fatalf("error = %#v", err)
	}
	render(t, p, src, true)
}

func TestFallbackUsesDefaultOnly(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\nprofiles:\n  default: {}\n  deep: {}\n")
	render(t, p, src, false)
	abs, _ := filepath.Abs(src)
	if err := TakeOver(p, "deep", State{Source: abs}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ocp.yaml"), []byte("version: 1\nprofiles:\n  default: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := render(t, p, src, false)
	if !r.Fallback.FellBack || r.Fallback.Active != "default" {
		t.Fatalf("fallback = %#v", r.Fallback)
	}
	if got, _ := ActiveProfile(p); got != "default" {
		t.Fatalf("active = %q", got)
	}
}

func TestFallbackWithoutDefaultLeavesNoActiveProfile(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\nprofiles:\n  deep: {}\n  lean: {}\n")
	render(t, p, src, false)
	abs, _ := filepath.Abs(src)
	if err := TakeOver(p, "deep", State{Source: abs}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ocp.yaml"), []byte("version: 1\nprofiles:\n  lean: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := render(t, p, src, false)
	if !r.Fallback.NoActive {
		t.Fatalf("fallback = %#v", r.Fallback)
	}
	if _, err := os.Lstat(p.OpenCode); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenCode path still active: %v", err)
	}
}

func TestResetLeavesExistingUserConfigWhenNoOriginalWasPreserved(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\n")
	render(t, p, src, false)
	abs, _ := filepath.Abs(src)
	if err := TakeOver(p, "default", State{Source: abs}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.OpenCode); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.OpenCode, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.OpenCode, "user.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reset(p, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.OpenCode, "user.json")); err != nil {
		t.Fatalf("reset removed user config: %v", err)
	}
}

func TestAcquireLockRejectsConcurrentMutation(t *testing.T) {
	p := testPaths(t)
	release, err := AcquireLock(p)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := AcquireLock(p); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock error = %v", err)
	}
}

func TestTakeOverRestoresOriginalWhenStateCannotBeSaved(t *testing.T) {
	p := testPaths(t)
	src := source(t, "version: 1\n")
	render(t, p, src, false)
	if err := os.MkdirAll(p.OpenCode, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.OpenCode, "original"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockingParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingParent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.StateFile = filepath.Join(blockingParent, "state.json")
	abs, _ := filepath.Abs(src)
	if err := TakeOver(p, "default", State{Source: abs}); err == nil {
		t.Fatal("TakeOver succeeded")
	}
	if _, err := os.Stat(filepath.Join(p.OpenCode, "original")); err != nil {
		t.Fatalf("original configuration was not restored: %v", err)
	}
}

func TestSSHSkillIsGitBacked(t *testing.T) {
	if !isGit("ssh://git@example.test/team/skill.git") {
		t.Fatal("ssh Git skill was treated as a local path")
	}
}

func TestDefaultPathsSetsOCPSrcToConfigHomeOcp(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configHome, "ocp")
	if p.OCPSrc != want {
		t.Fatalf("OCPSrc = %q, want %q", p.OCPSrc, want)
	}
}

func TestDefaultOCPSrcConstantMatchesExpectedValue(t *testing.T) {
	want := "~/.config/ocp"
	if DefaultOCPSrc != want {
		t.Fatalf("DefaultOCPSrc = %q, want %q", DefaultOCPSrc, want)
	}
}
