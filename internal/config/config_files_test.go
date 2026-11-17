package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveFromDirDiscoversFilesDirectories(t *testing.T) {
	dir := t.TempDir()
	for name, contents := range map[string]string{
		"ocp.yaml":                         "version: 1\n",
		"profiles/base/profile.yaml":       "config: {}\n",
		"profiles/base/files/a.txt":        "a\n",
		"profiles/child/profile.yaml":      "extends: base\n",
		"profiles/child/files/b.txt":       "b\n",
		"profiles/plain/profile.yaml":      "config: {}\n",
		"profiles/flat.yaml":               "config: {}\n",
		"profiles/grandchild/profile.yaml": "extends: child\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := Load(filepath.Join(dir, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, p := range profiles {
		got[p.Name] = p.FilesDirs
	}
	want := map[string][]string{
		"base":       {"profiles/base/files"},
		"child":      {"profiles/base/files", "profiles/child/files"},
		"grandchild": {"profiles/base/files", "profiles/child/files"},
		"plain":      nil,
		"flat":       nil,
	}
	for name, dirs := range want {
		if len(dirs) == 0 && len(got[name]) == 0 {
			continue
		}
		if !reflect.DeepEqual(got[name], dirs) {
			t.Errorf("%s FilesDirs = %#v, want %#v", name, got[name], dirs)
		}
	}
}

func TestProfileYAMLRejectsFilesKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ocp.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nprofiles:\n  a:\n    files: [x]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown key error")
	}
}
