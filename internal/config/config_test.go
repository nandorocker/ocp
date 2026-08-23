package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadText(t *testing.T, text string) *Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMinimalConfig(t *testing.T) {
	profiles, err := Resolve(loadText(t, "version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Name != "default" {
		t.Fatalf("profiles = %#v, want implicit default", profiles)
	}
}

func TestEmptyProfilesDoesNotCreateDefault(t *testing.T) {
	profiles, err := Resolve(loadText(t, "version: 1\nprofiles: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("profiles = %#v, want no profiles", profiles)
	}
}

func TestResolveInheritanceAndDeepMerge(t *testing.T) {
	doc := loadText(t, `version: 1
config:
  permission:
    bash:
      "*": ask
  model: root
instructions: [root, shared]
plugins: [one, shared]
agents:
  worker:
    file: ./worker.md
    config:
      model: root
      permission:
        edit: ask
profiles:
  base:
    config:
      permission:
        bash:
          "git *": allow
    instructions: [base, shared]
  child:
    extends: base
    config:
      model: child
    plugins: [shared, two]
    agents:
      worker:
        config:
          permission:
            edit: allow
`)
	profiles, err := Resolve(doc)
	if err != nil {
		t.Fatal(err)
	}
	child := profiles[1]
	if child.Name != "child" {
		t.Fatalf("profiles are not sorted: %#v", profiles)
	}
	if got := child.Config["model"]; got != "child" {
		t.Errorf("model = %v, want child", got)
	}
	permission := child.Config["permission"].(map[string]any)
	bash := permission["bash"].(map[string]any)
	if bash["*"] != "ask" || bash["git *"] != "allow" {
		t.Errorf("bash = %#v", bash)
	}
	if got := child.Instructions; strings.Join(got, ",") != "root,shared,base" {
		t.Errorf("instructions = %#v", got)
	}
	if got := child.Plugins; strings.Join(got, ",") != "one,shared,two" {
		t.Errorf("plugins = %#v", got)
	}
	if got := child.Agents["worker"]; got.File != "./worker.md" || got.Config["permission"].(map[string]any)["edit"] != "allow" {
		t.Errorf("agent = %#v", got)
	}
}

func TestResolveRejectsCycleAndUnknownParent(t *testing.T) {
	for _, text := range []string{
		"version: 1\nprofiles:\n  one:\n    extends: two\n  two:\n    extends: one\n",
		"version: 1\nprofiles:\n  one:\n    extends: absent\n",
	} {
		if _, err := Resolve(loadText(t, text)); err == nil {
			t.Errorf("Resolve(%q) succeeded", text)
		}
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("version: 1\nunknown: value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown configuration key") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsInvalidVersionAndName(t *testing.T) {
	for _, text := range []string{
		"version: 2\n",
		"version: 1\nagents:\n  ../unsafe: {}\n",
	} {
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("Load(%q) succeeded", text)
		}
	}
}

func TestSkillRefOverride(t *testing.T) {
	profiles, err := Resolve(loadText(t, `version: 1
skills:
  - https://github.com/example/foo@v1
profiles:
  deep:
    skills:
      - git: https://github.com/example/foo
        ref: v2
`))
	if err != nil {
		t.Fatal(err)
	}
	skills := profiles[0].Skills
	if len(skills) != 1 || skills[0].Source != "https://github.com/example/foo" || skills[0].Ref != "v2" {
		t.Fatalf("skills = %#v, want foo@v2", skills)
	}
}

func TestPluginVersionOverride(t *testing.T) {
	profiles, err := Resolve(loadText(t, `version: 1
plugins: [foo@1, "@scope/bar@1"]
profiles:
  deep:
    plugins: [foo@2, "@scope/bar@2", extra]
`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(profiles[0].Plugins, ",")
	if got != "foo@2,@scope/bar@2,extra" {
		t.Fatalf("plugins = %q", got)
	}
}
