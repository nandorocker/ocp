package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnabledHarnesses(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"", []string{"opencode"}},
		{"harnesses: [codex, claude, opencode]\n", []string{"codex", "claude", "opencode"}},
	} {
		doc := loadText(t, "version: 1\n"+tc.text)
		if got := doc.EnabledHarnesses(); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("EnabledHarnesses() = %v, want %v", got, tc.want)
		}
		got := doc.EnabledHarnesses()
		got[0] = "changed"
		if !reflect.DeepEqual(doc.EnabledHarnesses(), tc.want) {
			t.Fatal("accessor exposed mutable harness list")
		}
	}
}

func TestInvalidHarnesses(t *testing.T) {
	for _, value := range []string{"[]", "null", "opencode", "[opencode, opencode]", "[unknown]", "['']", "[1]"} {
		t.Run(value, func(t *testing.T) {
			assertHarnessLoadError(t, "version: 1\nharnesses: "+value+"\n")
		})
	}
}

func assertHarnessLoadError(t *testing.T, text string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("accepted invalid config:\n%s", text)
	}
}

func TestModelValidationAllScopes(t *testing.T) {
	for _, model := range []string{"{}", "null", "''", "' '", "3", "[]", "{other: x}", "{codex: ''}", "{claude: 3}", "{codex: {model: x}}", "{codex: a, codex: b}"} {
		for _, declaration := range []string{
			"config:\n  model: MODEL\n",
			"config:\n  agent:\n    worker:\n      model: MODEL\n",
			"agents:\n  worker:\n    model: MODEL\n",
			"agents:\n  worker:\n    config:\n      model: MODEL\n",
		} {
			declaration = strings.ReplaceAll(declaration, "MODEL", model)
			for _, text := range []string{
				declaration,
				"profiles:\n  main:\n" + indentHarnessYAML(declaration, 4),
				"profiles:\n  main:\n    machines:\n      laptop:\n" + indentHarnessYAML(declaration, 8),
			} {
				assertHarnessLoadError(t, "version: 1\n"+text)
			}
		}
	}
}

func indentHarnessYAML(text string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(strings.TrimSuffix(text, "\n"), "\n", "\n"+prefix) + "\n"
}

func TestAgentModelMappings(t *testing.T) {
	doc := loadText(t, `version: 1
config:
  agent:
    native:
      model: {codex: gpt-native}
agents:
  worker:
    model: {codex: gpt-worker, claude: sonnet}
    config:
      model: {claude: sonnet, codex: gpt-worker}
`)
	want := map[string]any{"codex": "gpt-worker", "claude": "sonnet"}
	if !reflect.DeepEqual(doc.Agents["worker"].Config["model"], want) {
		t.Fatalf("shorthand not normalized: %#v", doc.Agents)
	}
	for _, configured := range []string{"openai/gpt-worker", "{codex: gpt-other}", "{codex: gpt-worker, claude: sonnet}"} {
		assertHarnessLoadError(t, "version: 1\nagents:\n  worker:\n    model: {codex: gpt-worker}\n    config:\n      model: "+configured+"\n")
	}
}

func TestModelInheritance(t *testing.T) {
	for _, tc := range []struct {
		base, child string
		want        any
	}{
		{"{opencode: vendor/base, codex: gpt-base}", "{codex: gpt-child}", map[string]any{"opencode": "vendor/base", "codex": "gpt-child"}},
		{"openai/gpt-base", "{codex: gpt-child}", map[string]any{"codex": "gpt-child"}},
		{"{codex: gpt-base}", "openai/gpt-child", "openai/gpt-child"},
	} {
		doc := loadText(t, "version: 1\nconfig:\n  model: "+tc.base+"\nagents:\n  worker:\n    model: "+tc.base+"\nprofiles:\n  parent: {}\n  child:\n    extends: parent\n    config:\n      model: "+tc.child+"\n    agents:\n      worker:\n        model: "+tc.child+"\n")
		profiles, err := Resolve(doc)
		if err != nil {
			t.Fatal(err)
		}
		child := profiles[0]
		if child.Name != "child" || !reflect.DeepEqual(child.Config["model"], tc.want) || !reflect.DeepEqual(child.Agents["worker"].Config["model"], tc.want) {
			t.Fatalf("unexpected inherited model: %#v", child)
		}
	}
}

func TestResolveModel(t *testing.T) {
	for _, tc := range []struct {
		value   any
		harness string
		want    string
		bad     bool
	}{
		{nil, "codex", "", false},
		{"unknown/provider-model", "opencode", "unknown/provider-model", false},
		{"openai/gpt-test", "codex", "gpt-test", false},
		{"anthropic/claude-test", "claude", "claude-test", false},
		{"gpt-test", "codex", "gpt-test", false},
		{"o3-mini", "codex", "o3-mini", false},
		{"sonnet", "claude", "sonnet", false},
		{"claude-test", "claude", "claude-test", false},
		{"anthropic/sonnet", "codex", "", true},
		{"openai/gpt-test", "claude", "", true},
		{"other/gpt-test", "codex", "", true},
		{"unknown", "codex", "", true},
		{"unknown", "claude", "", true},
		{"openai/", "codex", "", true},
		{"openai/a/b", "codex", "", true},
		{"x", "unsupported", "", true},
		{nil, "unsupported", "", true},
		{map[string]any{"codex": "native-custom"}, "codex", "native-custom", false},
		{map[string]any{"claude": "native-custom"}, "claude", "native-custom", false},
		{map[string]any{"claude": "openai/gpt-test"}, "claude", "", true},
		{map[string]any{"codex": "gpt-test"}, "claude", "", true},
		{map[string]any{"opencode": "vendor/x", "codex": 1}, "opencode", "", true},
	} {
		got, err := ResolveModel(tc.value, tc.harness)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("ResolveModel(%#v, %q) = %q, %v; want %q, error=%v", tc.value, tc.harness, got, err, tc.want, tc.bad)
		}
	}
}

func TestMachineModelMappingMerge(t *testing.T) {
	doc := loadText(t, `version: 1
config:
  model: {opencode: vendor/base, codex: gpt-base}
  agent:
    native:
      model: {opencode: vendor/agent, codex: gpt-agent}
profiles:
  main:
    machines:
      laptop:
        config:
          model: {codex: gpt-machine}
          agent:
            native:
              model: {codex: gpt-machine-agent}
`)
	profiles, err := ResolveForMachine(doc, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := profiles[0].Config["model"], (map[string]any{"opencode": "vendor/base", "codex": "gpt-machine"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("machine model = %#v, want %#v", got, want)
	}
	agent := profiles[0].Config["agent"].(map[string]any)["native"].(map[string]any)
	if got, want := agent["model"], (map[string]any{"opencode": "vendor/agent", "codex": "gpt-machine-agent"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("machine agent model = %#v, want %#v", got, want)
	}
}

func TestNativeModelsRejectForeignFamiliesAndVariants(t *testing.T) {
	for _, test := range []struct {
		harness string
		model   any
	}{
		{"claude", map[string]any{"claude": "gpt-5"}},
		{"codex", map[string]any{"codex": "claude-sonnet"}},
		{"codex", "openai/gpt-5#high"},
		{"claude", "anthropic/claude-sonnet#high"},
	} {
		if _, err := ResolveModel(test.model, test.harness); err == nil {
			t.Errorf("accepted %v for %s", test.model, test.harness)
		}
	}
	if model, err := ResolveModel("custom/model#high", "opencode"); err != nil || model != "custom/model#high" {
		t.Fatalf("legacy model=%q error=%v", model, err)
	}
}
