package ocp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

func TestEstimateContextMeasuresAlwaysLoadedParts(t *testing.T) {
	source := t.TempDir()
	nativeFixture(t, source, "AGENTS.md", strings.Repeat("a", 400))
	nativeFixture(t, source, "hosts/windy.md", strings.Repeat("h", 40))
	nativeFixture(t, source, "skills/one/SKILL.md", "---\nname: one\ndescription: "+strings.Repeat("d", 92)+"\n---\n"+strings.Repeat("b", 800))
	nativeFixture(t, source, "agents/rev.md", "---\ndescription: "+strings.Repeat("r", 33)+"\n---\nBody")
	p := config.Profile{
		Instructions: []string{"AGENTS.md"},
		Skills:       []config.Skill{{Source: "one"}, {Source: "https://example.test/org/remote"}},
		Agents:       map[string]config.Agent{"rev": {File: "agents/rev.md"}, "cfg": {Config: map[string]any{"description": "c"}}},
		Config: map[string]any{"mcp": map[string]any{"servers": map[string]any{
			"a": map[string]any{"type": "remote", "url": "https://a.test"},
			"b": map[string]any{"type": "local", "command": []any{"x"}},
		}}},
	}
	est, err := EstimateContext(source, p, "windy", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ContextPart{
		"instructions": {Kind: "instructions", Count: 2, Tokens: 110}, // 440 chars
		"skills":       {Kind: "skills", Count: 1, Tokens: 25},        // 3 + 92 + 4 = 99 chars
		"agents":       {Kind: "agents", Count: 2, Tokens: 12},        // (3+33+4) + (3+1+4) = 48 chars
	}
	if len(est.Parts) != len(want) {
		t.Fatalf("parts = %#v", est.Parts)
	}
	for _, part := range est.Parts {
		if part != want[part.Kind] {
			t.Errorf("%s = %#v, want %#v", part.Kind, part, want[part.Kind])
		}
	}
	if est.Parts[0].Kind != "instructions" || est.Tokens != 147 {
		t.Errorf("order or total wrong: %#v", est)
	}
	if est.OnDemand != 200 || est.MCPServers != 2 {
		t.Errorf("on demand %d, mcp %d", est.OnDemand, est.MCPServers)
	}
	if len(est.Unmeasured) != 1 || est.Unmeasured[0] != "remote" {
		t.Errorf("unmeasured = %v", est.Unmeasured)
	}
}

func TestFormatTokens(t *testing.T) {
	for n, want := range map[int]string{0: "0", 840: "840", 1000: "1k", 1049: "1k", 1050: "1.1k", 9140: "9.1k", 9949: "9.9k", 9950: "10k", 12400: "12k"} {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestEstimateContextRefusesSymlinkedSkillFileAndCountsNativeAgents(t *testing.T) {
	source := t.TempDir()
	nativeFixture(t, source, "secret.txt", strings.Repeat("s", 4000))
	if err := os.MkdirAll(filepath.Join(source, "skills", "evil"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "secret.txt"), filepath.Join(source, "skills", "evil", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		Skills: []config.Skill{{Source: "evil"}},
		Config: map[string]any{"agent": map[string]any{
			"helper": map[string]any{"description": "helps"},
			"off":    map[string]any{"description": "never offered", "disable": true},
		}},
	}
	est, err := EstimateContext(source, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(est.Unmeasured) != 1 || est.Unmeasured[0] != "evil" {
		t.Fatalf("symlinked SKILL.md was read: %#v", est)
	}
	if len(est.Parts) != 1 || est.Parts[0] != (ContextPart{Kind: "agents", Count: 1, Tokens: 4}) {
		t.Fatalf("native agents = %#v", est.Parts)
	}
	empty, _ := EstimateContext(source, config.Profile{}, "", nil)
	if empty.Parts == nil {
		t.Fatal("parts must be an empty list, not null, in JSON")
	}
}
