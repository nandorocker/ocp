package ocp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

func adoptHome(t *testing.T) Paths {
	t.Helper()
	p := nativeTestPaths(t)
	p.OpenCode = filepath.Join(p.Home, ".config", "opencode")
	guide := "# Host\nHeadless machine: start dev servers normally and report the tailnet address, never localhost, because the machine is remote and headless.\n"
	w := func(rel, text string) { nativeTestWrite(t, filepath.Join(p.Home, rel), []byte(text)) }
	w(".claude/CLAUDE.md", "Be brief.\n\n<!-- BEGIN OCP MANAGED -->\nmanaged\n<!-- END OCP MANAGED -->\n")
	w(".codex/AGENTS.md", strings.ReplaceAll(guide, "report", "always report"))
	w(".claude/skills/shared/SKILL.md", "---\nname: shared\ndescription: d\n---\nsame\n")
	w(".agents/skills/shared/SKILL.md", "---\nname: shared\ndescription: d\n---\nsame\n")
	w(".claude/skills/drifted/SKILL.md", "---\nname: drifted\ndescription: d\n---\nv1\n")
	w(".agents/skills/drifted/SKILL.md", "---\nname: drifted\ndescription: d\n---\nv2\n")
	w(".claude/skills/managed/SKILL.md", "---\nname: managed\ndescription: d\n---\n")
	w(".claude.json", `{"userID":"x","mcpServers":{"mobbin":{"type":"http","url":"https://api.mobbin.com/mcp"},"figma":{"type":"http","url":"http://127.0.0.1:9300/mcp","headers":{"Authorization":"Bearer abcdefghijklmnopqrstuvwxyz123456"}},"gh":{"type":"stdio","command":"gh-mcp","args":[],"env":{"GH_TOKEN":"${GH_TOKEN}"}}}}`)
	w(".codex/config.toml", "[mcp_servers.mobbin]\nurl = \"https://api.mobbin.com/mcp\"\n\n[mcp_servers.gh]\ncommand = \"gh-mcp\"\nenv_vars = [\"GH_TOKEN\"]\n")
	// Files OCP owns are not adopted.
	state := `{"Version":3,"Profile":"default","Files":{"skills/managed/SKILL.md":{"Data":"","Mode":384}}}`
	nativeTestWrite(t, nativeStatePath(p, "claude"), []byte(state))
	return p
}

func TestAdoptInventoriesHarnesses(t *testing.T) {
	p := adoptHome(t)
	guide := []byte("# Host\nHeadless machine: start dev servers normally and report the tailnet address, never localhost, because the machine is remote and headless.\n")
	inv, err := Adopt(p, AdoptSource{Skills: map[string]bool{"shared": true}, MCP: map[string]bool{"mobbin": true}, Instructions: map[string][]byte{"hosts/windy.md": guide}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Instructions) != 2 || string(inv.Instructions[0].text) != "Be brief." || inv.Instructions[1].CopyOf != "hosts/windy.md" {
		t.Fatalf("instructions = %+v", inv.Instructions)
	}
	byName := func(items []AdoptItem) map[string]AdoptItem {
		out := map[string]AdoptItem{}
		for _, it := range items {
			out[it.Name] = it
		}
		return out
	}
	skills := byName(inv.Skills)
	if _, ok := skills["managed"]; ok || !skills["shared"].Same || !skills["shared"].InSource || skills["drifted"].Same || len(skills) != 2 {
		t.Fatalf("skills = %+v", inv.Skills)
	}
	mcp := byName(inv.MCP)
	if !mcp["mobbin"].Same || !mcp["mobbin"].InSource || !mcp["gh"].Same {
		t.Fatalf("mcp = %+v", inv.MCP)
	}
	figma := mcp["figma"].Value.(map[string]any)["headers"].(map[string]any)["Authorization"]
	if figma != "Bearer {env:FIGMA_AUTHORIZATION}" || len(inv.Secrets) != 1 {
		t.Fatalf("figma header = %v, secrets %v", figma, inv.Secrets)
	}
}

func TestWriteAdoptionCreatesALoadableSource(t *testing.T) {
	p := adoptHome(t)
	inv, err := Adopt(p, AdoptSource{})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "source")
	if _, err := WriteAdoption(dir, inv, []string{"opencode", "claude", "codex"}); err != nil {
		t.Fatal(err)
	}
	doc, err := config.Load(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil || len(profiles) != 1 || len(profiles[0].Skills) != 2 {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, config.FileName))
	if strings.Contains(string(b), "abcdefghijklmnopqrstuvwxyz") || !strings.Contains(string(b), "{env:FIGMA_AUTHORIZATION}") {
		t.Fatalf("secret copied into the source:\n%s", b)
	}
	if _, err := WriteAdoption(dir, inv, nil); err == nil {
		t.Fatal("wrote over a non-empty directory")
	}
}

func TestRedactServerCatchesSecretsOutsideEnvAndHeaders(t *testing.T) {
	cases := []struct {
		in       map[string]any
		field    string
		leaked   string
		contains string
	}{
		{map[string]any{"type": "remote", "url": "https://mcp.exa.ai/mcp?exaApiKey=abc123&mode=x"}, "url", "abc123", "exaApiKey={env:EXA_EXAAPIKEY}"},
		{map[string]any{"type": "remote", "url": "https://user:hunter2@host.test/mcp"}, "url", "hunter2", "{env:EXA_URL_CREDENTIALS}@host.test"},
		{map[string]any{"type": "local", "command": []any{"npx", "@smithery/cli", "run", "x", "--key", "abc123"}}, "command", "abc123", "{env:EXA_KEY}"},
		{map[string]any{"type": "local", "command": []any{"srv", "--token=s3cr3t"}}, "command", "s3cr3t", "--token={env:EXA_TOKEN}"},
		{map[string]any{"type": "local", "command": []any{"srv"}, "environment": map[string]any{"DB_PASS": "hunter2"}}, "environment", "hunter2", "{env:DB_PASS}"},
	}
	for _, tc := range cases {
		out, secrets := redactServer("exa", tc.in)
		text := describe(out)
		if strings.Contains(text, tc.leaked) || !strings.Contains(text, tc.contains) || len(secrets) == 0 {
			t.Errorf("%s: got %s, secrets %v", tc.field, text, secrets)
		}
	}
	keep, secrets := redactServer("pw", map[string]any{"type": "local", "command": []any{"npx", "-y", "@playwright/mcp@latest"}, "environment": map[string]any{"ENABLE_MCP_APPS": "true"}})
	if len(secrets) != 0 || !strings.Contains(describe(keep), "@playwright/mcp@latest") {
		t.Errorf("redacted a harmless server: %s %v", describe(keep), secrets)
	}
}

func describe(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestWriteAdoptionSkipsSecretFilesAndFollowsSymlinkedSkills(t *testing.T) {
	p := nativeTestPaths(t)
	p.OpenCode = filepath.Join(p.Home, ".config", "opencode")
	real := filepath.Join(p.Home, ".agents", "skills", "linked")
	nativeTestWrite(t, filepath.Join(real, "SKILL.md"), []byte("---\nname: linked\ndescription: d\n---\n"))
	nativeTestWrite(t, filepath.Join(real, ".env"), []byte("KEY=secret"))
	if err := os.MkdirAll(filepath.Join(p.Home, ".claude", "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(p.Home, ".claude", "skills", "linked")); err != nil {
		t.Fatal(err)
	}
	inv, err := Adopt(p, AdoptSource{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Skills) != 1 || len(inv.Skills[0].Found) != 2 || !inv.Skills[0].Same {
		t.Fatalf("skills = %+v", inv.Skills)
	}
	dir := filepath.Join(t.TempDir(), "src")
	warnings, err := WriteAdoption(dir, inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "linked", ".env")); !os.IsNotExist(err) {
		t.Fatal(".env was copied into the source")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], ".env") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestAdoptedFileUpdatedThenResetRestoresOriginal(t *testing.T) {
	p := nativeTestPaths(t)
	skill := nativeTestPath(t, p, "claude", "files/skills/hand/SKILL.md")
	nativeTestWrite(t, skill, []byte("mine"))
	if err := os.Chmod(skill, 0o644); err != nil {
		t.Fatal(err)
	}
	applyNative(t, p, "claude", NativeProfile{Files: map[string][]byte{"skills/hand/SKILL.md": []byte("mine")}})
	applyNative(t, p, "claude", NativeProfile{Files: map[string][]byte{"skills/hand/SKILL.md": []byte("newer")}})
	if got := string(nativeTestRead(t, skill)); got != "newer" {
		t.Fatalf("update = %q", got)
	}
	nativeTestReset(t, p, "claude")
	info, err := os.Stat(skill)
	if err != nil || string(nativeTestRead(t, skill)) != "mine" || info.Mode().Perm() != 0o644 {
		t.Fatalf("reset did not restore the user's file: %v %v", info, err)
	}
}
