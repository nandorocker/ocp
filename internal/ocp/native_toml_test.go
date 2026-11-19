package ocp

import (
	"strings"
	"testing"
)

func patchTOML(t *testing.T, data string, change func(m map[string]any)) (string, error) {
	t.Helper()
	before, err := nativeSettings([]byte(data), "codex")
	if err != nil {
		t.Fatal(err)
	}
	after := cloneMap(before)
	change(after)
	out, err := patchNativeTOML([]byte(data), before, after)
	return string(out), err
}

const userTOML = `# user settings
model = "gpt-5" # keep this comment
approval_policy = "on-request"

# my projects
[projects."/home/u"]
trust_level = "trusted"

[mcp_servers.mine]
url = "https://example.test/mcp" # hand-written

[mcp_servers.mine.env]
A = "1"
`

func TestPatchTOMLAddsTablesAndKeepsEverythingElse(t *testing.T) {
	out, err := patchTOML(t, userTOML, func(m map[string]any) {
		m["mcp_servers"].(map[string]any)["ocp"] = map[string]any{"command": "npx", "args": []any{"-y", "pkg@1"}, "env": map[string]any{"K": "v"}}
		m["model_providers"] = map[string]any{"zai": map[string]any{"base_url": "https://api.z.ai/v1", "env_key": "ZAI_KEY"}}
		m["model_provider"] = "zai"
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"# user settings", `model = "gpt-5" # keep this comment`, "# my projects", `url = "https://example.test/mcp" # hand-written`, "[mcp_servers.mine.env]"} {
		if !strings.Contains(out, kept) {
			t.Errorf("lost %q in:\n%s", kept, out)
		}
	}
	if !strings.HasPrefix(out, userTOML[:strings.Index(userTOML, "\n# my projects")]) {
		t.Errorf("top of file changed:\n%s", out)
	}
	if !strings.Contains(out, "model_provider = \"zai\"\n\n# my projects") {
		t.Errorf("new top-level key not placed above the first table's notes:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.ocp]\nargs = [\"-y\", \"pkg@1\"]\ncommand = \"npx\"\nenv = { K = \"v\" }\n") {
		t.Errorf("table not written as expected:\n%s", out)
	}
}

func TestPatchTOMLReplacesAndRemovesOwnedTablesOnly(t *testing.T) {
	start := userTOML + "\n[mcp_servers.ocp]\ncommand = \"old\"\n\n[mcp_servers.ocp.env]\nX = \"1\"\n\n# notes for zzz\n[zzz]\nkeep = true\n"
	out, err := patchTOML(t, start, func(m map[string]any) {
		m["mcp_servers"].(map[string]any)["ocp"] = map[string]any{"command": "new"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"old"`) || strings.Contains(out, "[mcp_servers.ocp.env]") || !strings.Contains(out, "# notes for zzz\n[zzz]\nkeep = true") {
		t.Fatalf("replace went wrong:\n%s", out)
	}
	out, err = patchTOML(t, out, func(m map[string]any) {
		delete(m["mcp_servers"].(map[string]any), "ocp")
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "mcp_servers.ocp") || !strings.Contains(out, "[mcp_servers.mine]") {
		t.Fatalf("remove went wrong:\n%s", out)
	}
	out, err = patchTOML(t, out, func(m map[string]any) {
		delete(m, "model")
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "gpt-5") || !strings.Contains(out, `approval_policy = "on-request"`) {
		t.Fatalf("top-level remove went wrong:\n%s", out)
	}
}

func TestPatchTOMLRemovesMultilineValue(t *testing.T) {
	start := "model = \"a\"\nargs = [\n  \"x\",\n  \"y\",\n]\nother = 1\n"
	out, err := patchTOML(t, start, func(m map[string]any) { delete(m, "args") })
	if err != nil {
		t.Fatal(err)
	}
	if out != "model = \"a\"\nother = 1\n" {
		t.Fatalf("got:\n%s", out)
	}
}

func TestPatchTOMLRefusesInlineParent(t *testing.T) {
	start := "mcp_servers = { mine = { url = \"https://x.test\" } }\n"
	_, err := patchTOML(t, start, func(m map[string]any) {
		m["mcp_servers"].(map[string]any)["ocp"] = map[string]any{"command": "x"}
	})
	if err != nil {
		// Adding a sibling to an inline table cannot be done by appending a table.
		if !strings.Contains(err.Error(), "could not preserve") && !strings.Contains(err.Error(), "invalid TOML") && !strings.Contains(err.Error(), "inline") {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	t.Fatal("edited a collection written inline")
}

func TestPatchTOMLEmptyFileAndQuotedKeys(t *testing.T) {
	out, err := patchTOML(t, "", func(m map[string]any) {
		m["model"] = "gpt-5"
		m["mcp_servers"] = map[string]any{"my server": map[string]any{"url": "https://x.test", "http_headers": map[string]any{"X-Key": "a\"b"}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "model = \"gpt-5\"\n\n[mcp_servers.\"my server\"]\nhttp_headers = { X-Key = \"a\\\"b\" }\nurl = \"https://x.test\"\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}
