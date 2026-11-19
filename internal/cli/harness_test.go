package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/ocp"
	"github.com/pelletier/go-toml/v2"
)

const harnessConfig = `version: 1
harnesses: [opencode, claude, codex]
config:
  model:
    opencode: openai/gpt-5
    claude: sonnet
    codex: gpt-5
instructions: [guide.md]
skills: [check]
agents:
  review:
    file: agents/review.md
profiles:
  default: {}
  openai:
    config:
      model: openai/gpt-5
  anthropic:
    config:
      model: anthropic/claude-sonnet-4-5
`

func harnessFixture(t *testing.T, setup bool) (ocp.Paths, string, *Runner, *bytes.Buffer) {
	t.Helper()
	p := testPaths(t)
	p.Home = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	source := t.TempDir()
	write(t, filepath.Join(source, "ocp.yaml"), harnessConfig)
	write(t, filepath.Join(source, "guide.md"), "Shared harness instructions.\n")
	write(t, filepath.Join(source, "skills/check/SKILL.md"), "---\nname: check\ndescription: Check changes\n---\nCheck carefully.\n")
	write(t, filepath.Join(source, "agents/review.md"), "---\ndescription: Review changes\n---\nReview carefully.\n")
	out := new(bytes.Buffer)
	r := runner(p, out)
	// No test may invoke any real native CLI (or other subprocess).
	r.Exec = func(string, []string, []string) error { return fmt.Errorf("unexpected execution") }
	if setup {
		harnessRun(t, r, "setup", "--source", source)
	} else if err := ocp.SaveState(p, ocp.State{Source: source}); err != nil {
		t.Fatal(err)
	}
	return p, source, r, out
}

func harnessRun(t *testing.T, r *Runner, args ...string) {
	t.Helper()
	if err := r.Run(args); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

func TestHarnessSetupHonorsExistingLock(t *testing.T) {
	p := testPaths(t)
	source := t.TempDir()
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\n")
	release, err := ocp.AcquireLock(p)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var out bytes.Buffer
	if err := runner(p, &out).Run([]string{"setup", "--source", source}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error=%v", err)
	}
	if state, err := ocp.LoadState(p); err != nil || state != nil {
		t.Fatalf("setup changed state: %v %v", state, err)
	}
}

func TestHarnessInteractiveSelectionDoesNotRequireOpenCode(t *testing.T) {
	p, source, r, _ := harnessFixture(t, false)
	text := strings.Replace(harnessConfig, "[opencode, claude, codex]", "[claude, codex]", 1)
	text = strings.Replace(text, "  default: {}", "  default: {}\n  chosen: {}", 1)
	write(t, filepath.Join(source, "ocp.yaml"), text)
	r.defaults()
	if err := r.setupRun(p, source, true, "", false, false, false, "chosen", ""); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"claude", "codex"} {
		if name, err := ocp.NativeActiveProfile(p, h); err != nil || name != "chosen" {
			t.Fatalf("%s active=%q err=%v", h, name, err)
		}
	}
	if name, err := ocp.ActiveProfile(p); err != nil || name != "" {
		t.Fatalf("OpenCode active=%q err=%v", name, err)
	}
}

func TestHarnessSetupFailureCanRecoverWithBrokenSource(t *testing.T) {
	p, source, r, _ := harnessFixture(t, false)
	if err := os.Remove(p.StateFile); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nharnesses: [claude]\nconfig:\n  model: anthropic/claude-sonnet\n")
	root := filepath.Join(p.Home, ".claude")
	if err := os.Mkdir(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	err := r.Run([]string{"setup", "--source", source})
	if err == nil {
		t.Skip("filesystem permissions do not prevent writes for this user")
	}
	state, err := ocp.LoadState(p)
	if err != nil || state == nil {
		t.Fatalf("bootstrap was not registered: %v %v", state, err)
	}
	pending := filepath.Join(filepath.Dir(p.StateFile), "native-claude.json.pending")
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("no recovery journal: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(source, "ocp.yaml"), "invalid: [")
	harnessRun(t, r, "apply", "--recover", "--harness", "claude")
	if name, err := ocp.NativeActiveProfile(p, "claude"); err != nil || name != "default" {
		t.Fatalf("active=%q err=%v", name, err)
	}
}

func TestHarnessPublicationPreservesExistingRunProfiles(t *testing.T) {
	p, _, r, _ := harnessFixture(t, true)
	harnessRun(t, r, "apply")
	called := false
	r.Exec = func(name string, args, env []string) error {
		called = true
		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, "OPENCODE_CONFIG_DIR=") {
				dir := strings.TrimPrefix(entry, "OPENCODE_CONFIG_DIR=")
				if filepath.Base(dir) != "openai" {
					t.Errorf("dir=%q", dir)
				}
				if _, err := os.Stat(filepath.Join(dir, "opencode.json")); err != nil {
					t.Error(err)
				}
				found = true
			}
		}
		if !found {
			t.Error("missing run config")
		}
		return nil
	}
	harnessRun(t, r, "run", "openai")
	if !called {
		t.Fatal("run did not execute")
	}
	if name, err := ocp.ActiveProfile(p); err != nil || name != "default" {
		t.Fatalf("run changed active=%q err=%v", name, err)
	}
}

func TestHarnessSkippedOpenCodeKeepsItsPinnedArtifacts(t *testing.T) {
	p, source, r, _ := harnessFixture(t, true)
	oldLink, err := os.Readlink(p.OpenCode)
	if err != nil {
		t.Fatal(err)
	}
	before := harnessRead(t, filepath.Join(p.OpenCode, "opencode.json"))
	text := strings.Replace(harnessConfig, "    opencode: openai/gpt-5\n", "", 1)
	write(t, filepath.Join(source, "ocp.yaml"), text)
	write(t, filepath.Join(source, "guide.md"), "Updated shared guide\n")
	harnessRun(t, r, "apply")
	if link, err := os.Readlink(p.OpenCode); err != nil || link != oldLink {
		t.Fatalf("OpenCode moved: %q %v", link, err)
	}
	if got := harnessRead(t, filepath.Join(p.OpenCode, "opencode.json")); !bytes.Equal(got, before) {
		t.Fatal("skipped OpenCode artifacts changed")
	}
	if !bytes.Contains(harnessRead(t, filepath.Join(p.Home, ".claude", "CLAUDE.md")), []byte("Updated shared guide")) {
		t.Fatal("compatible Claude target did not update")
	}
}

func harnessRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Include symlink destinations without following them, so ownership links and
// unrelated native files are both checked for accidental changes.
func harnessSnapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				link, err := os.Readlink(path)
				result[path] = "link:" + link
				return err
			}
			if entry.IsDir() {
				result[path] = "directory"
			} else {
				result[path] = string(harnessRead(t, path))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func harnessUnchanged(t *testing.T, before map[string]string, roots ...string) {
	t.Helper()
	if after := harnessSnapshot(t, roots...); !reflect.DeepEqual(before, after) {
		t.Errorf("files/state changed: before=%v after=%v", before, after)
	}
}

func harnessActive(t *testing.T, p ocp.Paths, harness, want string) {
	t.Helper()
	var got string
	var err error
	if harness == "opencode" {
		got, err = ocp.ActiveProfile(p)
	} else {
		got, err = ocp.NativeActiveProfile(p, harness)
	}
	if err != nil || got != want {
		t.Errorf("%s active=%q, want %q; error=%v", harness, got, want, err)
	}
}

func TestHarnessSetupPortableFilesAndModels(t *testing.T) {
	p, source, _, _ := harnessFixture(t, true)
	for _, harness := range []string{"opencode", "claude", "codex"} {
		harnessActive(t, p, harness, "default")
	}
	for _, item := range []struct{ path, model string }{
		{filepath.Join(p.Current, "default", "opencode.json"), "openai/gpt-5"},
		{filepath.Join(p.Home, ".claude/settings.json"), "sonnet"},
		{filepath.Join(p.Home, ".codex/config.toml"), "gpt-5"},
	} {
		var settings map[string]any
		data := harnessRead(t, item.path)
		var err error
		if strings.HasSuffix(item.path, ".toml") {
			err = toml.Unmarshal(data, &settings)
		} else {
			err = json.Unmarshal(data, &settings)
		}
		if err != nil || settings["model"] != item.model {
			t.Errorf("%s: model=%v want %s, error=%v", item.path, settings["model"], item.model, err)
		}
	}
	for _, path := range []string{".claude/CLAUDE.md", ".codex/AGENTS.md"} {
		if got := string(harnessRead(t, filepath.Join(p.Home, path))); !strings.Contains(got, "Shared harness instructions.") {
			t.Errorf("%s missing shared instructions: %q", path, got)
		}
	}
	for _, home := range []string{".claude", ".agents"} {
		got := harnessRead(t, filepath.Join(p.Home, home, "skills/check/SKILL.md"))
		want := harnessRead(t, filepath.Join(source, "skills/check/SKILL.md"))
		if !bytes.Equal(got, want) {
			t.Errorf("%s skill differs from portable source", home)
		}
	}
	for _, path := range []string{".claude/agents/review.md", ".codex/agents/review.toml"} {
		got := string(harnessRead(t, filepath.Join(p.Home, path)))
		if !strings.Contains(got, "Review changes") || !strings.Contains(got, "Review carefully.") {
			t.Errorf("%s missing shared agent description/body: %s", path, got)
		}
	}
}

func TestHarnessExplicitIncompatibilityNoWrites(t *testing.T) {
	for _, targets := range [][]string{{"--harness", "claude"}, {"--harness", "codex", "--harness", "claude"}} {
		t.Run(strings.Join(targets, "_"), func(t *testing.T) {
			p, source, r, _ := harnessFixture(t, true)
			roots := []string{p.Home, p.Data, filepath.Dir(p.StateFile), source}
			before := harnessSnapshot(t, roots...)
			args := append([]string{"use", "openai"}, targets...)
			if err := r.Run(args); err == nil || !strings.Contains(err.Error(), "claude") {
				t.Errorf("expected Claude incompatibility, got %v", err)
			}
			harnessUnchanged(t, before, roots...)
		})
	}
}

func TestHarnessUntargetedSkipRetainsNativeSelection(t *testing.T) {
	p, source, r, out := harnessFixture(t, true)
	write(t, filepath.Join(source, "ocp.yaml"), strings.Replace(harnessConfig, "harnesses: [opencode, claude, codex]", "harnesses: [claude, codex]", 1))
	roots := []string{filepath.Join(p.Home, ".claude"), filepath.Join(filepath.Dir(p.StateFile), "native-claude.json")}
	before := harnessSnapshot(t, roots...)
	out.Reset()
	harnessRun(t, r, "use", "openai")
	if !strings.Contains(out.String(), "claude: skipped") || !strings.Contains(out.String(), "unchanged") {
		t.Errorf("missing skip diagnostic: %s", out.String())
	}
	harnessUnchanged(t, before, roots...)
	harnessActive(t, p, "claude", "default")
	harnessActive(t, p, "codex", "openai")
	harnessActive(t, p, "opencode", "default")
}

func TestHarnessAgentIncompatibilityRejectsWholeProfile(t *testing.T) {
	p, source, r, _ := harnessFixture(t, true)
	write(t, filepath.Join(source, "agents/review.md"), "---\ndescription: Review changes\nmodel: openai/gpt-5\n---\nReview carefully.\n")
	roots := []string{p.Home, p.Data, filepath.Dir(p.StateFile)}
	before := harnessSnapshot(t, roots...)
	if err := r.Run([]string{"apply", "--harness", "claude"}); err == nil || !strings.Contains(err.Error(), "review") {
		t.Errorf("expected incompatible review agent, got %v", err)
	}
	harnessUnchanged(t, before, roots...)
}

func TestHarnessPreviewNoNativeWrites(t *testing.T) {
	p, source, r, out := harnessFixture(t, false)
	roots := []string{p.Home, p.Data, filepath.Dir(p.StateFile), source}
	before := harnessSnapshot(t, roots...)
	harnessRun(t, r, "use", "default", "--preview")
	for _, harness := range []string{"opencode", "claude", "codex"} {
		if !strings.Contains(out.String(), harness+": would apply default") {
			t.Errorf("missing %s preview: %s", harness, out.String())
		}
	}
	harnessUnchanged(t, before, roots...)
}

func TestHarnessIndependentSelectionsRepeatedApplyAndScopedReset(t *testing.T) {
	p, source, r, _ := harnessFixture(t, true)
	// Cover flags both after and before the profile argument.
	harnessRun(t, r, "use", "anthropic", "--harness", "claude")
	harnessRun(t, r, "use", "--harness", "codex", "openai")
	for i := 0; i < 2; i++ {
		harnessRun(t, r, "apply", "--harness", "claude", "--harness", "codex")
		harnessActive(t, p, "claude", "anthropic")
		harnessActive(t, p, "codex", "openai")
		harnessActive(t, p, "opencode", "default")
	}
	roots := []string{source, p.StateFile, filepath.Join(p.Home, ".codex"), filepath.Join(p.Home, ".agents"), filepath.Join(filepath.Dir(p.StateFile), "native-codex.json"), p.Data}
	before := harnessSnapshot(t, roots...)
	harnessRun(t, r, "reset", "--harness", "claude")
	harnessUnchanged(t, before, roots...)
	harnessActive(t, p, "claude", "")
	harnessActive(t, p, "codex", "openai")
	harnessActive(t, p, "opencode", "default")
	if _, err := os.Lstat(filepath.Join(p.Home, ".claude/agents/review.md")); !os.IsNotExist(err) {
		t.Errorf("reset retained managed Claude agent: %v", err)
	}
}

func TestHarnessRepeatedApplyAllTargets(t *testing.T) {
	p, _, r, _ := harnessFixture(t, true)
	for i := 0; i < 2; i++ {
		harnessRun(t, r, "apply")
		for _, harness := range []string{"opencode", "claude", "codex"} {
			harnessActive(t, p, harness, "default")
		}
	}
}

func TestHarnessNativeCollisionPreflightAvoidsOtherTargetWrites(t *testing.T) {
	for _, collision := range []string{".claude/agents/review.md", ".codex/agents/review.toml"} {
		t.Run(collision, func(t *testing.T) {
			p, source, r, _ := harnessFixture(t, false)
			write(t, filepath.Join(p.Home, collision), "User-owned agent\n")
			roots := []string{p.Home, p.Data, filepath.Dir(p.StateFile), source}
			before := harnessSnapshot(t, roots...)
			if err := r.Run([]string{"apply"}); err == nil {
				t.Error("expected native ownership collision")
			}
			harnessUnchanged(t, before, roots...)
		})
	}
}
