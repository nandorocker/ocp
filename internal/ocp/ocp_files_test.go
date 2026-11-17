package ocp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func filesSource(t *testing.T, files map[string]string) string {
	t.Helper()
	src := source(t, "version: 1\n")
	for name, contents := range files {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func TestRenderCopiesProfileFilesWithManifestAndModes(t *testing.T) {
	p := testPaths(t)
	src := filesSource(t, map[string]string{
		"profiles/dev/profile.yaml":           "config: {model: x}\n",
		"profiles/dev/files/plugin.jsonc":     "{\"a\":1}\n",
		"profiles/dev/files/nested/deep/c.md": "deep\n",
	})
	render(t, p, src, false)
	root := filepath.Join(p.Current, "dev")
	for rel, want := range map[string]string{"plugin.jsonc": "{\"a\":1}\n", "nested/deep/c.md": "deep\n"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want {
			t.Fatalf("%s = %q, %v", rel, b, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", rel, info.Mode().Perm())
		}
	}
	if info, _ := os.Stat(filepath.Join(root, "nested", "deep")); info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", info.Mode().Perm())
	}
	var m manifest
	b, _ := os.ReadFile(filepath.Join(root, manifestName))
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Files["plugin.jsonc"] == "" || m.Files["nested/deep/c.md"] == "" {
		t.Fatalf("manifest = %#v", m.Files)
	}
}

func TestEditingRenderedExtraFileIsDrift(t *testing.T) {
	p := testPaths(t)
	src := filesSource(t, map[string]string{
		"profiles/dev/profile.yaml":       "config: {}\n",
		"profiles/dev/files/plugin.jsonc": "{}\n",
	})
	render(t, p, src, false)
	if err := os.WriteFile(filepath.Join(p.Current, "dev", "plugin.jsonc"), []byte("{\"edited\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Render(RenderOptions{Paths: p, Source: src})
	var drift *DriftError
	if !errors.As(err, &drift) || len(drift.Paths) != 1 || drift.Paths[0] != "dev/plugin.jsonc" {
		t.Fatalf("error = %#v", err)
	}
	render(t, p, src, true)
}

func TestRenderExtraFilesInheritanceLaterWins(t *testing.T) {
	p := testPaths(t)
	src := filesSource(t, map[string]string{
		"profiles/base/profile.yaml":        "config: {}\n",
		"profiles/base/files/a.txt":         "base-a\n",
		"profiles/base/files/b.txt":         "base-b\n",
		"profiles/child/profile.yaml":       "extends: base\n",
		"profiles/child/files/a.txt":        "child-a\n",
		"profiles/child/files/dir/only.txt": "only\n",
	})
	render(t, p, src, false)
	read := func(profile, rel string) string {
		b, err := os.ReadFile(filepath.Join(p.Current, profile, filepath.FromSlash(rel)))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	if got := read("child", "a.txt"); got != "child-a\n" {
		t.Fatalf("child a.txt = %q", got)
	}
	if got := read("child", "b.txt"); got != "base-b\n" {
		t.Fatalf("child b.txt = %q", got)
	}
	if got := read("child", "dir/only.txt"); got != "only\n" {
		t.Fatalf("child dir/only.txt = %q", got)
	}
	if got := read("base", "a.txt"); got != "base-a\n" {
		t.Fatalf("base a.txt = %q", got)
	}
	if got := read("base", "dir/only.txt"); got != "<missing>" {
		t.Fatalf("base leaked child file: %q", got)
	}
}

func TestRenderRejectsExtraFilesCollidingWithGeneratedOutput(t *testing.T) {
	for _, name := range []string{"opencode.json", "AGENTS.md", "agents/x.md", "skills/s/SKILL.md", "sub/.ocp-manifest.json", ".ocp-manifest.json"} {
		t.Run(name, func(t *testing.T) {
			p := testPaths(t)
			src := filesSource(t, map[string]string{
				"profiles/dev/profile.yaml":  "config: {}\n",
				"profiles/dev/files/" + name: "x\n",
			})
			_, err := Render(RenderOptions{Paths: p, Source: src})
			if err == nil || !strings.Contains(err.Error(), "collides") {
				t.Fatalf("error = %v", err)
			}
			if _, statErr := os.Lstat(p.Current); statErr == nil {
				t.Fatal("failed render published a release")
			}
		})
	}
}

func TestRenderRejectsExtraFileDirectoryConflictAcrossInheritance(t *testing.T) {
	p := testPaths(t)
	src := filesSource(t, map[string]string{
		"profiles/base/profile.yaml":   "config: {}\n",
		"profiles/base/files/x":        "file\n",
		"profiles/child/profile.yaml":  "extends: base\n",
		"profiles/child/files/x/y.txt": "nested\n",
	})
	_, err := Render(RenderOptions{Paths: p, Source: src})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("error = %v", err)
	}
}

func TestRenderRejectsSymlinksInExtraFiles(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("file", func(t *testing.T) {
		p := testPaths(t)
		src := filesSource(t, map[string]string{"profiles/dev/profile.yaml": "config: {}\n", "profiles/dev/files/ok.txt": "ok\n"})
		if err := os.Symlink(outside, filepath.Join(src, "profiles/dev/files/link.txt")); err != nil {
			t.Fatal(err)
		}
		if _, err := Render(RenderOptions{Paths: p, Source: src}); err == nil {
			t.Fatal("expected symlink rejection")
		}
	})
	t.Run("subdirectory", func(t *testing.T) {
		p := testPaths(t)
		src := filesSource(t, map[string]string{"profiles/dev/profile.yaml": "config: {}\n"})
		if err := os.MkdirAll(filepath.Join(src, "profiles/dev/files"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Dir(outside), filepath.Join(src, "profiles/dev/files/d")); err != nil {
			t.Fatal(err)
		}
		if _, err := Render(RenderOptions{Paths: p, Source: src}); err == nil {
			t.Fatal("expected symlink rejection")
		}
	})
	t.Run("files directory", func(t *testing.T) {
		p := testPaths(t)
		src := filesSource(t, map[string]string{"profiles/dev/profile.yaml": "config: {}\n"})
		if err := os.Symlink(filepath.Dir(outside), filepath.Join(src, "profiles/dev/files")); err != nil {
			t.Fatal(err)
		}
		if _, err := Render(RenderOptions{Paths: p, Source: src}); err == nil {
			t.Fatal("expected symlink rejection")
		}
	})
}
