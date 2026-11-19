package ocp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
	"github.com/pelletier/go-toml/v2"
)

func nativeFixture(t *testing.T, root, name, text string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareNativeProfileOutputs(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			source := t.TempDir()
			original := "---\nname: original\ndescription: Review changes\nmode: subagent\nmodel: openai/gpt-5\n---\nReview carefully.\n"
			nativeFixture(t, source, "agents/review.md", original)
			nativeFixture(t, source, "guide.md", "Shared guide")
			nativeFixture(t, source, "hosts/workstation.md", "Host guide\n")
			nativeFixture(t, source, "skills/check/SKILL.md", "---\nname: check\ndescription: Check things\n---\nCheck.\n")
			models := map[string]any{"claude": "sonnet", "codex": "gpt-5"}
			p := config.Profile{
				Config:       map[string]any{"model": models},
				Instructions: []string{"guide.md"},
				Skills:       []config.Skill{{Source: "check"}},
				Agents:       map[string]config.Agent{"review": {File: "agents/review.md", Config: map[string]any{"model": models}}},
			}
			out, err := PrepareNativeProfile(source, p, harness, "workstation", nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Settings["model"] != models[harness] || string(out.Guide) != "Shared guide\nHost guide\n" {
				t.Fatalf("unexpected plan: %#v", out)
			}
			if out.Skills["check"] != filepath.Join(source, "skills/check") {
				t.Fatalf("unexpected skill source: %#v", out.Skills)
			}
			if harness == "claude" {
				front, body, err := nativeFrontmatter(out.Files["agents/review.md"])
				if err != nil || front["name"] != "review" || front["description"] != "Review changes" || front["model"] != "sonnet" || string(body) != "Review carefully.\n" {
					t.Fatalf("unexpected Claude agent: %#v %q %v", front, body, err)
				}
				if _, exists := front["mode"]; exists {
					t.Fatal("OpenCode mode leaked into Claude frontmatter")
				}
			} else {
				var agent map[string]any
				if err := toml.Unmarshal(out.Files["agents/review.toml"], &agent); err != nil {
					t.Fatal(err)
				}
				if agent["name"] != "review" || agent["description"] != "Review changes" || agent["developer_instructions"] != "Review carefully.\n" || agent["model"] != "gpt-5" {
					t.Fatalf("unexpected Codex agent: %#v", agent)
				}
				if _, exists := out.Settings["agents"]; exists {
					t.Fatal("legacy role registration emitted for a standalone Codex agent")
				}
			}
			data, err := os.ReadFile(filepath.Join(source, "agents/review.md"))
			if err != nil || string(data) != original {
				t.Fatal("source agent changed")
			}
			if !reflect.DeepEqual(p.Config["model"], models) {
				t.Fatal("input configuration changed")
			}
		})
	}
}

func TestPrepareNativeProfileEffectiveModels(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			source := t.TempDir()
			// Missing selected-harness mapping must reject even a non-primary role.
			other := "codex"
			if harness == "codex" {
				other = "claude"
			}
			p := config.Profile{Config: map[string]any{"agent": map[string]any{
				"reviewer": map[string]any{"description": "Review", "model": map[string]any{other: "some-model"}},
			}}}
			if _, err := PrepareNativeProfile(source, p, harness, "", nil); err == nil || !strings.Contains(err.Error(), "reviewer") {
				t.Fatalf("expected incompatible reviewer error, got %v", err)
			}
		})
	}
	source := t.TempDir()
	nativeFixture(t, source, "agent.md", "---\ndescription: Review\nmodel: openai/gpt-5\n---\nBody")
	p := config.Profile{
		Config: map[string]any{"agent": map[string]any{"review": map[string]any{"model": "anthropic/claude-sonnet-4-5"}}},
		Agents: map[string]config.Agent{"review": {File: "agent.md"}},
	}
	if _, err := PrepareNativeProfile(source, p, "claude", "", nil); err == nil {
		t.Fatal("frontmatter model did not override config.agent base")
	}
	p.Agents["review"] = config.Agent{File: "agent.md", Config: map[string]any{"model": "sonnet"}}
	if _, err := PrepareNativeProfile(source, p, "claude", "", nil); err != nil {
		t.Fatalf("assignment did not override incompatible frontmatter: %v", err)
	}
	out, err := PrepareNativeProfile(source, p, "claude", "missing-host", nil)
	if err != nil || len(out.Guide) != 0 {
		t.Fatalf("optional host guide: %#v %v", out, err)
	}
}

func TestPrepareNativeProfileRejectsUnsupportedBehavior(t *testing.T) {
	for _, key := range []string{"provider", "providers", "permission", "permissions", "tools", "hooks", "temperature", "agents", "unknown"} {
		t.Run(key, func(t *testing.T) {
			_, err := PrepareNativeProfile(t.TempDir(), config.Profile{Config: map[string]any{key: map[string]any{}}}, "codex", "", nil)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected unsupported %s, got %v", key, err)
			}
		})
	}
	for _, options := range []map[string]any{{"tools": []any{"Read"}}, {"permission": "deny"}, {"mode": "primary"}, {"prompt": "{file:secret}"}} {
		p := config.Profile{Agents: map[string]config.Agent{"review": {Config: mergeMap(map[string]any{"description": "Review"}, options)}}}
		if _, err := PrepareNativeProfile(t.TempDir(), p, "claude", "", nil); err == nil {
			t.Fatalf("accepted unsupported agent options %#v", options)
		}
	}
	for _, front := range []string{"tools: Read", "permissionMode: bypassPermissions", "hooks: {}", "prompt: override", "model: [broken]", "description: [broken]"} {
		source := t.TempDir()
		nativeFixture(t, source, "agent.md", "---\ndescription: Review\n"+front+"\n---\nBody")
		p := config.Profile{Agents: map[string]config.Agent{"review": {File: "agent.md"}}}
		if _, err := PrepareNativeProfile(source, p, "codex", "", nil); err == nil {
			t.Fatalf("accepted unsupported frontmatter %s", front)
		}
	}
}

func TestPrepareNativeProfilePluginWarningsAndDeterminism(t *testing.T) {
	p := config.Profile{Config: map[string]any{"plugin": []any{"plugin"}, "$schema": "schema"}, Plugins: []string{"other"}}
	root := t.TempDir()
	want := []string{"claude: omitted OpenCode config.$schema", "claude: omitted OpenCode config.plugin", "claude: omitted OpenCode plugins"}
	for i := 0; i < 5; i++ {
		out, err := PrepareNativeProfile(root, p, "claude", "", nil)
		if err != nil || !reflect.DeepEqual(out.Warnings, want) || len(out.Settings) != 0 {
			t.Fatalf("unexpected plugin plan: %#v %v", out, err)
		}
	}
}

func TestPrepareNativeProfileSourceSafety(t *testing.T) {
	root := t.TempDir()
	nativeFixture(t, root, "skills/check/SKILL.md", "---\nname: check\ndescription: Check\n---\nBody")
	if err := os.Symlink(filepath.Join(root, "skills/check/SKILL.md"), filepath.Join(root, "skills/check/link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []config.Profile{
		{Instructions: []string{"../outside"}},
		{Skills: []config.Skill{{Source: "../outside"}}},
		{Skills: []config.Skill{{Source: "check"}}},
		{Agents: map[string]config.Agent{"review": {File: "../outside"}}},
		{Agents: map[string]config.Agent{"../review": {Config: map[string]any{"description": "Review"}}}},
	} {
		if _, err := PrepareNativeProfile(root, p, "codex", "", nil); err == nil {
			t.Fatalf("accepted unsafe source: %#v", p)
		}
	}
	if _, err := PrepareNativeProfile(root, config.Profile{}, "codex", "../machine", nil); err == nil {
		t.Fatal("accepted unsafe machine")
	}
}

func TestPrepareNativeProfileSkillExtensions(t *testing.T) {
	root := t.TempDir()
	for _, field := range []string{"disable-model-invocation: true", "allowed-tools: Read", "context: fork", "agent: explorer", "hooks: {}", "user-invocable: false"} {
		nativeFixture(t, root, "skills/check/SKILL.md", "---\nname: check\ndescription: Check\n"+field+"\n---\nBody")
		p := config.Profile{Skills: []config.Skill{{Source: "check"}}}
		if _, err := PrepareNativeProfile(root, p, "codex", "", nil); err == nil || !strings.Contains(err.Error(), "not portable") {
			t.Fatalf("accepted nonportable skill %s: %v", field, err)
		}
		if _, err := PrepareNativeProfile(root, p, "claude", "", nil); err != nil {
			t.Fatalf("rejected Claude skill %s: %v", field, err)
		}
	}
}

type nativeTestSkillProvider struct{ path string }

func (p nativeTestSkillProvider) SkillDir(config.Skill) (string, error) { return p.path, nil }

func TestPrepareNativeProfileGitSkillsAndCollisions(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "check")
	nativeFixture(t, checkout, "SKILL.md", "---\nname: check\ndescription: Check\n---\nBody")
	p := config.Profile{Skills: []config.Skill{{Source: "https://example.com/skills/check"}}}
	if _, err := PrepareNativeProfile(root, p, "codex", "", nil); err == nil || !strings.Contains(err.Error(), "SkillProvider") {
		t.Fatalf("expected provider requirement, got %v", err)
	}
	out, err := PrepareNativeProfile(root, p, "codex", "", nativeTestSkillProvider{checkout})
	if err != nil || out.Skills["check"] != checkout {
		t.Fatalf("unexpected Git skill plan: %#v %v", out, err)
	}
	nativeFixture(t, root, "skills/check/SKILL.md", "---\nname: check\ndescription: Check\n---\nBody")
	p.Skills = append(p.Skills, config.Skill{Source: "check"})
	if _, err := PrepareNativeProfile(root, p, "codex", "", nativeTestSkillProvider{checkout}); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected basename collision, got %v", err)
	}
}
