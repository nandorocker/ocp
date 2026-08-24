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

func loadWithSourceDir(t *testing.T, yml string, setupFn func(dir string)) (*Document, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	if setupFn != nil {
		setupFn(dir)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc, dir
}

func TestResolveFromDirDiscoversProfileFiles(t *testing.T) {
	doc, dir := loadWithSourceDir(t, "version: 1\nconfig:\n  model: root\n", func(dir string) {
		profilesDir := filepath.Join(dir, "profiles")
		os.MkdirAll(profilesDir, 0o700)
		os.WriteFile(filepath.Join(profilesDir, "default.yaml"), []byte(`config:
  model: default-model
`), 0o600)
	})
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Name != "default" {
		t.Fatalf("profiles = %#v, want single default", profiles)
	}
	if got := profiles[0].Config["model"]; got != "default-model" {
		t.Errorf("model = %v, want default-model", got)
	}
}

func TestResolveFromDirMergesRootAndProfileFiles(t *testing.T) {
	doc, dir := loadWithSourceDir(t, "version: 1\nconfig:\n  permission:\n    bash:\n      \"*\": ask\n  model: root\nagents:\n  worker:\n    file: ./agents/worker.md\nskills:\n  - https://github.com/example/skill\nplugins:\n  - wakatime\n", func(dir string) {
		profilesDir := filepath.Join(dir, "profiles")
		os.MkdirAll(profilesDir, 0o700)
		os.MkdirAll(filepath.Join(dir, "agents"), 0o700)
		os.WriteFile(filepath.Join(profilesDir, "deep.yaml"), []byte(`extends: default
config:
  model: deep-model
skills:
  - https://github.com/example/deep-skill
plugins:
  - extra-plugin
agents:
  worker:
    config:
      permission:
        edit: allow
`), 0o600)
		os.WriteFile(filepath.Join(profilesDir, "default.yaml"), []byte(`config:
  model: default-model
skills:
  - https://github.com/example/default-sskill
`), 0o600)
	})
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	nameMap := make(map[string]*Profile)
	for i := range profiles {
		nameMap[profiles[i].Name] = &profiles[i]
	}
	defaultProf, ok := nameMap["default"]
	if !ok {
		t.Fatalf("missing default profile, got %v", profileNames(profiles))
	}
	deepProf, ok := nameMap["deep"]
	if !ok {
		t.Fatalf("missing deep profile, got %v", profileNames(profiles))
	}
	if got := defaultProf.Config["model"]; got != "default-model" {
		t.Errorf("default model = %v, want default-model", got)
	}
	if got := deepProf.Config["model"]; got != "deep-model" {
		t.Errorf("deep model = %v, want deep-model", got)
	}
	if deepProf.Agents["worker"].File != "./agents/worker.md" {
		t.Errorf("agent file not inherited: %v", deepProf.Agents["worker"])
	}
	perm := deepProf.Agents["worker"].Config["permission"].(map[string]any)["edit"]
	if perm != "allow" {
		t.Errorf("agent permission.edit = %v, want allow", perm)
	}
	if len(deepProf.Skills) != 3 {
		t.Errorf("expected 3 skills, got %d: %v", len(deepProf.Skills), deepProf.Skills)
	}
	if len(deepProf.Plugins) != 2 {
		t.Errorf("expected 2 plugins, got %d: %v", len(deepProf.Plugins), deepProf.Plugins)
	}
}

func profileNames(profiles []Profile) []string {
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}
	return names
}

func TestResolveFromDirBackwardCompatibleWithoutProfilesDir(t *testing.T) {
	doc, dir := loadWithSourceDir(t, "version: 1\nconfig:\n  model: test\nprofiles:\n  dev:\n    config:\n      model: dev-model\n", nil)
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Name != "dev" {
		t.Fatalf("profiles = %#v, want inline dev profile", profiles)
	}
	if got := profiles[0].Config["model"]; got != "dev-model" {
		t.Errorf("model = %v, want dev-model", got)
	}
}

func TestResolveFromDirImplicitDefaultNoProfilesKeyOrDir(t *testing.T) {
	doc, dir := loadWithSourceDir(t, "version: 1\nconfig:\n  model: minimal\n", nil)
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Name != "default" {
		t.Fatalf("profiles = %#v, want implicit default", profiles)
	}
	if got := profiles[0].Config["model"]; got != "minimal" {
		t.Errorf("model = %v, want minimal", got)
	}
}

func TestResolveFromDirRejectsUnknownParentAcrossFiles(t *testing.T) {
	doc, dir := loadWithSourceDir(t, "version: 1\n", func(dir string) {
		profilesDir := filepath.Join(dir, "profiles")
		os.MkdirAll(profilesDir, 0o700)
		os.WriteFile(filepath.Join(profilesDir, "child.yaml"), []byte(`extends: nonexistent
config:
  model: child
`), 0o600)
	})
	if _, err := doc.ResolveFromDir(dir); err == nil {
		t.Error("expected error for unknown parent across files")
	}
}

func TestResolveFromDirCyclesError(t *testing.T) {
	doc, dir := loadWithSourceDir(t, "version: 1\n", func(dir string) {
		profilesDir := filepath.Join(dir, "profiles")
		os.MkdirAll(profilesDir, 0o700)
		os.WriteFile(filepath.Join(profilesDir, "a.yaml"), []byte(`extends: b
config:
  model: a
`), 0o600)
		os.WriteFile(filepath.Join(profilesDir, "b.yaml"), []byte(`extends: a
config:
  model: b
`), 0o600)
	})
	if _, err := doc.ResolveFromDir(dir); err == nil {
		t.Error("expected cycle error")
	}
}
