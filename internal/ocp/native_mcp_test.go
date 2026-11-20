package ocp

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

func withHelper(t *testing.T, path string, err error) {
	t.Helper()
	old := ocpExecutable
	ocpExecutable = func() (string, error) { return path, err }
	t.Cleanup(func() { ocpExecutable = old })
}

func TestOpenCodeMCPServersReadsBothShapes(t *testing.T) {
	cfg := map[string]any{"mcp": map[string]any{
		"timeout": 5000,
		"legacy":  map[string]any{"type": "local", "command": []any{"npx", "-y", "pkg"}, "environment": map[string]any{"A": "1"}},
		"servers": map[string]any{
			"remote": map[string]any{"type": "remote", "url": "https://x.test/mcp", "headers": map[string]any{"X": "y"}},
			"off":    map[string]any{"type": "remote", "url": "https://off.test", "enabled": false},
		},
	}}
	servers, err := openCodeMCPServers(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"legacy", "remote"}) {
		t.Fatalf("servers = %v", names)
	}
	for _, bad := range []map[string]any{
		{"mcp": map[string]any{"x": map[string]any{"type": "local"}}},
		{"mcp": map[string]any{"x": map[string]any{"type": "remote"}}},
		{"mcp": map[string]any{"x": map[string]any{"type": "sse", "url": "u"}}},
		{"mcp": map[string]any{"x": map[string]any{"type": "local", "command": []any{"a"}, "environment": map[string]any{"K": 1}}}},
	} {
		if _, err := openCodeMCPServers(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestCodexMCPServerTranslation(t *testing.T) {
	withHelper(t, "/usr/local/bin/ocp", nil)
	source := "/src"
	cases := []struct {
		name    string
		server  mcpServer
		want    map[string]any
		warning string
	}{
		{"plain stdio", mcpServer{Name: "a", Local: true, Command: []string{"npx", "-y", "pkg"}, Env: map[string]string{"K": "v"}},
			map[string]any{"command": "npx", "args": []any{"-y", "pkg"}, "env": map[string]any{"K": "v"}}, ""},
		{"forwarded env", mcpServer{Name: "a", Local: true, Command: []string{"srv"}, Env: map[string]string{"TOKEN": "{env:TOKEN}"}},
			map[string]any{"command": "srv", "env_vars": []any{"TOKEN"}}, ""},
		{"file env is wrapped", mcpServer{Name: "a", Local: true, Command: []string{"npx", "pkg"}, Env: map[string]string{"KEY": "{file:keys/k}", "B": "{env:OTHER}x"}},
			map[string]any{"command": "/usr/local/bin/ocp", "args": []any{"secret", "exec", "B={env:OTHER}x", "KEY={file:/src/keys/k}", "--", "npx", "pkg"}, "env_vars": []any{"OTHER"}}, ""},
		{"home file stays for runtime", mcpServer{Name: "a", Local: true, Command: []string{"srv"}, Env: map[string]string{"KEY": "{file:~/k}"}},
			map[string]any{"command": "/usr/local/bin/ocp", "args": []any{"secret", "exec", "KEY={file:~/k}", "--", "srv"}}, ""},
		{"remote headers", mcpServer{Name: "r", URL: "https://x.test", Headers: map[string]string{"X-Static": "s", "X-Key": "{env:XK}", "Authorization": "Bearer {env:TOK}"}},
			map[string]any{"url": "https://x.test", "http_headers": map[string]any{"X-Static": "s"}, "env_http_headers": map[string]any{"X-Key": "XK"}, "bearer_token_env_var": "TOK"}, ""},
		{"file header skipped", mcpServer{Name: "r", URL: "https://x.test", Headers: map[string]string{"Authorization": "Bearer {file:/t}"}}, nil, "cannot read header"},
		{"substituted command skipped", mcpServer{Name: "a", Local: true, Command: []string{"{env:BIN}"}}, nil, "in its command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warning, err := codexMCPServer(tc.server, source)
			if err != nil {
				t.Fatal(err)
			}
			if tc.warning != "" {
				if got != nil || !strings.Contains(warning, tc.warning) {
					t.Fatalf("got %v, warning %q", got, warning)
				}
				return
			}
			if warning != "" || !nativeEqual(got, tc.want) {
				t.Fatalf("got %v, warning %q\nwant %v", got, warning, tc.want)
			}
		})
	}
	withHelper(t, "", os.ErrNotExist)
	_, warning, err := codexMCPServer(mcpServer{Name: "a", Local: true, Command: []string{"srv"}, Env: map[string]string{"K": "{file:/k}"}}, source)
	if err != nil || !strings.Contains(warning, "skipped") {
		t.Fatalf("missing helper = %q, %v", warning, err)
	}
}

func TestPrepareNativeProfileMCPAndHarnessBlocks(t *testing.T) {
	withHelper(t, "/opt/ocp", nil)
	withFakeClaude(t)
	p := config.Profile{
		Config: map[string]any{"model": map[string]any{"opencode": "zai/glm", "claude": "glm-5", "codex": "glm-5"}, "providers": map[string]any{"zai": map[string]any{}},
			"mcp": map[string]any{"servers": map[string]any{"pw": map[string]any{"type": "local", "command": []any{"npx", "pw"}}}}},
		Claude: map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"}, "apiKeyHelper": "{ocp} secret print '{file:~/.zai}'"},
		Codex:  map[string]any{"model_provider": "zai", "model_providers": map[string]any{"zai": map[string]any{"base_url": "https://p.test/v1", "env_key": "ZAI_KEY"}}},
	}
	claude, err := PrepareNativeProfile(t.TempDir(), p, "claude", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if claude.Settings["apiKeyHelper"] != "'/opt/ocp' secret print '{file:~/.zai}'" || claude.Settings["model"] != "glm-5" {
		t.Fatalf("claude settings = %v", claude.Settings)
	}
	if _, has := claude.Settings["mcp_servers"]; has || !nativeEqual(claude.MCP, map[string]any{"pw": map[string]any{"type": "stdio", "command": "npx", "args": []any{"pw"}}}) {
		t.Fatalf("claude MCP handling = %v / %v", claude.Settings, claude.MCP)
	}
	codex, err := PrepareNativeProfile(t.TempDir(), p, "codex", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !nativeEqual(codex.Settings["mcp_servers"], map[string]any{"pw": map[string]any{"command": "npx", "args": []any{"pw"}}}) || codex.Settings["model_provider"] != "zai" {
		t.Fatalf("codex settings = %v", codex.Settings)
	}
	if !strings.Contains(strings.Join(codex.Warnings, "\n"), "omitted OpenCode-only config.providers") {
		t.Fatalf("codex warnings = %v", codex.Warnings)
	}
	p.Codex = map[string]any{"model": "other"}
	if _, err := PrepareNativeProfile(t.TempDir(), p, "codex", "", nil); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("collision error = %v", err)
	}
}

func TestNativeInstallAdoptsIdenticalEntriesAndRefusesOthers(t *testing.T) {
	p := nativeTestPaths(t)
	configPath := nativeTestPath(t, p, "codex", "config")
	hand := "# mine\n[mcp_servers.shared]\nurl = \"https://shared.test\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	nativeTestWrite(t, configPath, []byte(hand))
	rendered := NativeProfile{Settings: map[string]any{"mcp_servers": map[string]any{
		"shared": map[string]any{"url": "https://shared.test"},
		"new":    map[string]any{"command": "npx"},
	}}}
	plan, err := PlanNativeInstall(p, "codex", "default", rendered)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyNativePlan(p, plan); err != nil {
		t.Fatal(err)
	}
	got := string(nativeTestRead(t, configPath))
	if !strings.HasPrefix(got, hand) || !strings.Contains(got, "[mcp_servers.new]") {
		t.Fatalf("installed:\n%s", got)
	}
	nativeTestReset(t, p, "codex")
	if got := string(nativeTestRead(t, configPath)); got != hand {
		t.Fatalf("reset left:\n%s", got)
	}
	rendered.Settings["mcp_servers"].(map[string]any)["other"] = map[string]any{"command": "y"}
	if _, err := PlanNativeInstall(p, "codex", "default", rendered); err == nil || !strings.Contains(err.Error(), "not owned by OCP") {
		t.Fatalf("conflict = %v", err)
	}
}

func TestNativeClaudeEnvCreatedThenRemoved(t *testing.T) {
	p := nativeTestPaths(t)
	configPath := nativeTestPath(t, p, "claude", "config")
	nativeTestWrite(t, configPath, []byte(`{"theme":"dark"}`))
	rendered := NativeProfile{Settings: map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "https://z.test"}}}
	plan, err := PlanNativeInstall(p, "claude", "default", rendered)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyNativePlan(p, plan); err != nil {
		t.Fatal(err)
	}
	settings, _ := nativeSettings(nativeTestRead(t, configPath), "claude")
	if !nativeEqual(settings["env"], map[string]any{"ANTHROPIC_BASE_URL": "https://z.test"}) {
		t.Fatalf("settings = %v", settings)
	}
	nativeTestReset(t, p, "claude")
	settings, _ = nativeSettings(nativeTestRead(t, configPath), "claude")
	if _, has := settings["env"]; has || settings["theme"] != "dark" {
		t.Fatalf("reset settings = %v", settings)
	}
}

func TestExpandSubstitutions(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("  s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCP_TEST_VAR", "v")
	got, err := ExpandSubstitutions("Bearer {file:" + file + "}/{env:OCP_TEST_VAR}")
	if err != nil || got != "Bearer s3cret/v" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ExpandSubstitutions("{env:OCP_TEST_UNSET_VAR}"); err == nil {
		t.Fatal("missing variable accepted")
	}
	if _, err := ExpandSubstitutions("{file:" + filepath.Join(dir, "missing") + "}"); err == nil {
		t.Fatal("missing file accepted")
	}
}

func applyNative(t *testing.T, p Paths, harness string, rendered NativeProfile) {
	t.Helper()
	plan, err := PlanNativeInstall(p, harness, "default", rendered)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyNativePlan(p, plan); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAdoptedEntryCanBeUpdatedAndResetRestoresHandWritten(t *testing.T) {
	p := nativeTestPaths(t)
	configPath := nativeTestPath(t, p, "codex", "config")
	hand := "[mcp_servers.shared]\nurl = \"https://shared.test\"\n"
	nativeTestWrite(t, configPath, []byte(hand))
	server := map[string]any{"url": "https://shared.test"}
	applyNative(t, p, "codex", NativeProfile{Settings: map[string]any{"mcp_servers": map[string]any{"shared": server}}})
	updated := map[string]any{"url": "https://shared.test", "env_http_headers": map[string]any{"X": "X_KEY"}}
	applyNative(t, p, "codex", NativeProfile{Settings: map[string]any{"mcp_servers": map[string]any{"shared": updated}}})
	settings, _ := nativeSettings(nativeTestRead(t, configPath), "codex")
	if !nativeEqual(settings["mcp_servers"], map[string]any{"shared": updated}) {
		t.Fatalf("adopted entry not updated: %v", settings)
	}
	nativeTestReset(t, p, "codex")
	if got := string(nativeTestRead(t, configPath)); got != hand {
		t.Fatalf("reset = %q, want the hand-written table", got)
	}
}

func TestNativeResetRemovesCollectionsAndFilesOCPCreated(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			p := nativeTestPaths(t)
			configPath := nativeTestPath(t, p, harness, "config")
			settings := map[string]any{"env": map[string]any{"A": "1", "B": "2"}}
			if harness == "codex" {
				settings = map[string]any{"mcp_servers": map[string]any{"a": map[string]any{"command": "x"}, "b": map[string]any{"command": "y"}}}
			}
			applyNative(t, p, harness, NativeProfile{Settings: settings})
			nativeTestReset(t, p, harness)
			nativeTestAbsent(t, configPath)
		})
	}
}

func TestPatchTOMLChangesRootScalarInPlace(t *testing.T) {
	start := "# top\nmodel = \"gpt-5\" # mine\napproval_policy = \"never\"\n\n[x]\ny = 1\n"
	out, err := patchTOML(t, start, func(m map[string]any) { m["model"] = "glm-5" })
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(start, `"gpt-5"`, `"glm-5"`, 1); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	back, err := patchTOML(t, out, func(m map[string]any) { m["model"] = "gpt-5" })
	if err != nil || back != start {
		t.Fatalf("round trip = %q, %v", back, err)
	}
}

func TestPatchTOMLDottedRootKeysAndCRLF(t *testing.T) {
	start := "mcp_servers.mine.command = \"a\"\r\nmodel = \"m\"\r\n\r\n[other]\r\nk = 1\r\n"
	out, err := patchTOML(t, start, func(m map[string]any) {
		delete(m["mcp_servers"].(map[string]any), "mine")
		m["mcp_servers"].(map[string]any)["new"] = map[string]any{"command": "b"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "mine") || !strings.Contains(out, "[mcp_servers.new]") || !strings.Contains(out, "[other]\r\nk = 1\r\n") {
		t.Fatalf("got:\n%q", out)
	}
}

func TestOpenCodeMCPServersRejectsBothShapesForOneName(t *testing.T) {
	cfg := map[string]any{"mcp": map[string]any{
		"dup":     map[string]any{"type": "remote", "url": "https://a.test"},
		"servers": map[string]any{"dup": map[string]any{"type": "remote", "url": "https://b.test"}},
	}}
	if _, err := openCodeMCPServers(cfg); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("duplicate = %v", err)
	}
}

func TestNativeVersionOneStateStillResets(t *testing.T) {
	p := nativeTestPaths(t)
	configPath := nativeTestPath(t, p, "codex", "config")
	nativeTestWrite(t, configPath, []byte("model = \"original\"\n"))
	applyNative(t, p, "codex", NativeProfile{Settings: map[string]any{"model": "managed"}})
	statePath := nativeStatePath(p, "codex")
	b := nativeTestRead(t, statePath)
	nativeTestWrite(t, statePath, bytes.Replace(b, []byte(`"Version": 3`), []byte(`"Version": 1`), 1))
	if s, err := loadNativeState(p, "codex"); err != nil || s.Version != 1 {
		t.Fatalf("v1 state = %v, %v", s.Version, err)
	}
	nativeTestReset(t, p, "codex")
	if got := string(nativeTestRead(t, configPath)); got != "model = \"original\"\n" {
		t.Fatalf("reset from v1 state = %q", got)
	}
	nativeTestWrite(t, statePath, []byte(`{"Version": 4}`))
	if _, err := loadNativeState(p, "codex"); err == nil || !strings.Contains(err.Error(), "upgrade ocp") {
		t.Fatalf("future state = %v", err)
	}
}
