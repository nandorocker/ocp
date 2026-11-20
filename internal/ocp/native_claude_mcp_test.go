package ocp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

// withFakeClaude replaces the claude CLI with an in-process fake that edits
// ~/.claude.json as `claude mcp add-json/remove -s user` do, and counts calls.
func withFakeClaude(t *testing.T) *int {
	t.Helper()
	calls := 0
	oldCmd, oldRun := claudeCommand, runClaude
	claudeCommand = func() (string, error) { return "/fake/claude", nil }
	runClaude = func(home string, args ...string) error {
		calls++
		path := filepath.Join(home, ".claude.json")
		doc := map[string]any{}
		if b, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(b, &doc); err != nil {
				return err
			}
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		switch args[1] {
		case "remove":
			delete(servers, args[4])
		case "add-json":
			if _, exists := servers[args[4]]; exists {
				return errors.New("already exists")
			}
			var v any
			if err := json.Unmarshal([]byte(args[5]), &v); err != nil {
				return err
			}
			servers[args[4]] = v
		}
		doc["mcpServers"] = servers
		doc["numStartups"] = calls // Claude rewrites unrelated state too.
		b, _ := json.Marshal(doc)
		return os.WriteFile(path, b, 0o600)
	}
	t.Cleanup(func() { claudeCommand, runClaude = oldCmd, oldRun })
	return &calls
}

func claudeServers(t *testing.T, p Paths) map[string]any {
	t.Helper()
	servers, err := readClaudeServers(p)
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func TestClaudeMCPInstallSwitchAndReset(t *testing.T) {
	calls := withFakeClaude(t)
	p := nativeTestPaths(t)
	hand := map[string]any{"type": "http", "url": "https://mine.test"}
	nativeTestWrite(t, claudeJSONPath(p), []byte(`{"userID":"u","mcpServers":{"mine":{"type":"http","url":"https://mine.test"},"shared":{"type":"http","url":"https://shared.test"}}}`))
	a := map[string]any{"type": "stdio", "command": "npx", "args": []any{"a"}}
	shared := map[string]any{"type": "http", "url": "https://shared.test"}
	applyNative(t, p, "claude", NativeProfile{MCP: map[string]any{"a": a, "shared": shared}})
	got := claudeServers(t, p)
	if !nativeEqual(got, map[string]any{"mine": hand, "shared": shared, "a": a}) {
		t.Fatalf("installed = %v", got)
	}
	if *calls != 1 {
		t.Fatalf("identical server was not adopted without a call: %d calls", *calls)
	}
	// Switch profiles: a changes, shared is no longer wanted but was adopted.
	a2 := map[string]any{"type": "stdio", "command": "npx", "args": []any{"a2"}}
	applyNative(t, p, "claude", NativeProfile{MCP: map[string]any{"a": a2}})
	if got := claudeServers(t, p); !nativeEqual(got, map[string]any{"mine": hand, "shared": shared, "a": a2}) {
		t.Fatalf("after switch = %v", got)
	}
	nativeTestReset(t, p, "claude")
	if got := claudeServers(t, p); !nativeEqual(got, map[string]any{"mine": hand, "shared": shared}) {
		t.Fatalf("after reset = %v", got)
	}
	b, _ := os.ReadFile(claudeJSONPath(p))
	if !strings.Contains(string(b), `"userID":"u"`) {
		t.Fatal("unrelated Claude state lost")
	}
}

func TestClaudeMCPConflictsAndExternalEdits(t *testing.T) {
	withFakeClaude(t)
	p := nativeTestPaths(t)
	nativeTestWrite(t, claudeJSONPath(p), []byte(`{"mcpServers":{"x":{"type":"http","url":"https://other.test"}}}`))
	want := map[string]any{"x": map[string]any{"type": "http", "url": "https://x.test"}}
	if _, err := PlanNativeInstall(p, "claude", "default", NativeProfile{MCP: want}); err == nil || !strings.Contains(err.Error(), "not owned by OCP") {
		t.Fatalf("conflict = %v", err)
	}
	p2 := nativeTestPaths(t)
	applyNative(t, p2, "claude", NativeProfile{MCP: want})
	nativeTestWrite(t, claudeJSONPath(p2), []byte(`{"mcpServers":{"x":{"type":"http","url":"https://edited.test"}}}`))
	if _, err := PlanNativeInstall(p2, "claude", "default", NativeProfile{MCP: want}); err == nil || !strings.Contains(err.Error(), "edited outside OCP") {
		t.Fatalf("external edit = %v", err)
	}
}

func TestClaudeMCPRecoveryReplaysOperations(t *testing.T) {
	withFakeClaude(t)
	p := nativeTestPaths(t)
	want := map[string]any{"a": map[string]any{"type": "http", "url": "https://a.test"}, "b": map[string]any{"type": "http", "url": "https://b.test"}}
	plan, err := PlanNativeInstall(p, "claude", "default", NativeProfile{MCP: want})
	if err != nil {
		t.Fatal(err)
	}
	// Interrupt after the first server: run it, then fail the second.
	failing := runClaude
	n := 0
	runClaude = func(home string, args ...string) error {
		n++
		if n == 2 {
			return errors.New("interrupted")
		}
		return failing(home, args...)
	}
	if err := ApplyNativePlan(p, plan); err == nil {
		t.Fatal("interrupted apply succeeded")
	}
	if active, _ := NativeActiveProfile(p, "claude"); active != "" {
		t.Fatal("state recorded before MCP servers were confirmed")
	}
	runClaude = failing
	if err := RecoverNative(p, "claude"); err != nil {
		t.Fatal(err)
	}
	if got := claudeServers(t, p); !nativeEqual(got, want) {
		t.Fatalf("after recovery = %v", got)
	}
	if active, _ := NativeActiveProfile(p, "claude"); active != "default" {
		t.Fatalf("active after recovery = %q", active)
	}
}

func TestClaudeMCPNeedsTheCLI(t *testing.T) {
	withFakeClaude(t)
	claudeCommand = func() (string, error) { return "", errors.New("not found") }
	p := nativeTestPaths(t)
	if _, err := PlanNativeInstall(p, "claude", "default", NativeProfile{MCP: map[string]any{"a": map[string]any{"type": "http", "url": "u"}}}); err == nil || !strings.Contains(err.Error(), "needs the claude command") {
		t.Fatalf("missing CLI = %v", err)
	}
	cfg := map[string]any{"mcp": map[string]any{"servers": map[string]any{"a": map[string]any{"type": "remote", "url": "https://a.test"}}}}
	if _, err := PrepareNativeProfile(t.TempDir(), configProfile(cfg), "claude", "", nil); err == nil || !strings.Contains(err.Error(), "claude command") {
		t.Fatalf("render without CLI = %v", err)
	}
}

func TestClaudeMCPServerTranslation(t *testing.T) {
	withHelper(t, "/usr/local/bin/ocp", nil)
	got, warning, err := claudeMCPServer(mcpServer{Name: "f", Local: true, Command: []string{"npx", "pkg"}, Env: map[string]string{"A": "{env:A}", "B": "x-{env:B}", "K": "{file:keys/k}"}}, "/src")
	if err != nil || warning != "" {
		t.Fatal(err, warning)
	}
	want := map[string]any{"type": "stdio", "command": "/usr/local/bin/ocp", "args": []any{"secret", "exec", "K={file:/src/keys/k}", "--", "npx", "pkg"}, "env": map[string]any{"A": "${A}", "B": "x-${B}"}}
	if !nativeEqual(got, want) {
		t.Fatalf("stdio = %v", got)
	}
	got, _, _ = claudeMCPServer(mcpServer{Name: "r", URL: "https://x.test/{env:PATHV}", Headers: map[string]string{"Authorization": "Bearer {file:~/tok}", "X-Env": "{env:XE}", "X-Static": "s"}}, "/src")
	want = map[string]any{"type": "http", "url": "https://x.test/${PATHV}", "headers": map[string]any{"X-Env": "${XE}", "X-Static": "s"}, "headersHelper": "'/usr/local/bin/ocp' secret headers 'Authorization=Bearer {file:~/tok}'"}
	if !nativeEqual(got, want) {
		t.Fatalf("http = %v", got)
	}
	_, warning, _ = claudeMCPServer(mcpServer{Name: "r", URL: "{file:/u}"}, "/src")
	if !strings.Contains(warning, "skipped") {
		t.Fatalf("file url warning = %q", warning)
	}
}

func configProfile(cfg map[string]any) config.Profile { return config.Profile{Config: cfg} }
