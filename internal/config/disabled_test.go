package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDisabledRemovesInheritedSkillsAndPlugins(t *testing.T) {
	dir := t.TempDir()
	doc := `version: 1
skills: [stop-slop, github:org/skills//skills/karpathy-guidelines]
plugins: [decision.ts, opencode-model-router@1.2.0]
config:
  plugins: ['{env:HOME}/.config/ocp/plugins/harness-bridge.ts', '@scope/pkg@2']
disabled: [karpathy-guidelines]
machines:
  small:
    disabled: [stop-slop]
profiles:
  default: {}
  lean:
    disabled: [decision, opencode-model-router, harness-bridge, "@scope/pkg", typo]
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Load(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := d.ResolveFromDirForMachine(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Profile{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	def, lean := byName["default"], byName["lean"]
	if len(def.Skills) != 1 || def.Skills[0].Source != "stop-slop" {
		t.Fatalf("default skills = %v", def.Skills)
	}
	if len(def.UnmatchedDisabled) != 0 {
		t.Fatalf("default unmatched = %v", def.UnmatchedDisabled)
	}
	if len(lean.Plugins) != 0 || len(lean.Config["plugins"].([]any)) != 0 {
		t.Fatalf("lean plugins = %v / %v", lean.Plugins, lean.Config["plugins"])
	}
	if !reflect.DeepEqual(lean.Disabled, []string{"karpathy-guidelines", "decision", "opencode-model-router", "harness-bridge", "@scope/pkg", "typo"}) {
		t.Fatalf("lean disabled = %v", lean.Disabled)
	}
	if !reflect.DeepEqual(lean.UnmatchedDisabled, []string{"typo"}) {
		t.Fatalf("lean unmatched = %v", lean.UnmatchedDisabled)
	}
	small, err := d.ResolveFromDirForMachine(dir, "small")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range small {
		if len(p.Skills) != 0 {
			t.Fatalf("machine overlay did not disable stop-slop in %s: %v", p.Name, p.Skills)
		}
	}
	// The declaration itself is untouched, so the lock entry and validation stay.
	if len(d.Skills) != 2 {
		t.Fatalf("document skills changed: %v", d.Skills)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("version: 1\ndisabled: stop-slop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, FileName)); err == nil {
		t.Fatal("accepted a disabled value that is not a list")
	}
}

func TestPluginName(t *testing.T) {
	for in, want := range map[string]string{
		"decision.ts":                         "decision",
		"plugins/harness-bridge.ts":           "harness-bridge",
		"{env:HOME}/.config/ocp/plugins/x.js": "x",
		"opencode-model-router":               "opencode-model-router",
		"opencode-model-router@1.2.0":         "opencode-model-router",
		"@scope/pkg@2":                        "@scope/pkg",
		"@scope/pkg":                          "@scope/pkg",
		"file:///abs/plugins/decision":        "decision",
	} {
		if got := pluginName(in); got != want {
			t.Errorf("pluginName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisabledByAncestorStillMatchesInDescendants(t *testing.T) {
	dir := t.TempDir()
	doc := "version: 1\nskills: [a, b]\ndisabled: [a]\nprofiles:\n  default: {}\n  child:\n    extends: default\n    disabled: [b]\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Load(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := d.ResolveFromDirForMachine(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if len(p.UnmatchedDisabled) != 0 {
			t.Errorf("%s: false unmatched %v", p.Name, p.UnmatchedDisabled)
		}
		if p.Name == "child" && len(p.Skills) != 0 {
			t.Errorf("child skills = %v", p.Skills)
		}
		if p.Name == "default" && (len(p.Skills) != 1 || p.Skills[0].Source != "b") {
			t.Errorf("default skills = %v", p.Skills)
		}
	}
}
