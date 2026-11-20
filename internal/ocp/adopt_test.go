package ocp

import (
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
	if err := WriteAdoption(dir, inv, []string{"opencode", "claude", "codex"}); err != nil {
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
	if err := WriteAdoption(dir, inv, nil); err == nil {
		t.Fatal("wrote over a non-empty directory")
	}
}
