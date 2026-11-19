package ocp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

func TestAtomicFileDoesNotPublishFailedOrShortWrites(t *testing.T) {
	for _, short := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		failure := errors.New("injected write failure")
		err := atomicFileWrite(path, []byte("replacement"), 0o600, func(f *os.File, b []byte) (int, error) {
			n, e := f.Write(b[:2])
			if e != nil {
				return n, e
			}
			if short {
				return n, nil
			}
			return n, failure
		})
		if short {
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("error=%v", err)
			}
		} else if !errors.Is(err, failure) {
			t.Fatalf("error=%v", err)
		}
		if got := string(nativeTestRead(t, path)); got != "original" {
			t.Fatalf("failed write published %q", got)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("temporary file leaked: %v %v", entries, err)
		}
	}
}

func TestNativeResetPreservesCommentsAddedToEmptyCodexConfig(t *testing.T) {
	for _, original := range []string{"", "# original comment\n", "absent"} {
		for _, reapply := range []bool{false, true} {
			p := nativeTestPaths(t)
			path := nativeTestPath(t, p, "codex", "config")
			if original != "absent" {
				nativeTestWrite(t, path, []byte(original))
			}
			rendered := NativeProfile{Settings: map[string]any{"model": "gpt-test"}}
			nativeTestInstall(t, p, "codex", "default", rendered)
			data := append(nativeTestRead(t, path), []byte("\n# personal comment added later\n")...)
			nativeTestWrite(t, path, data)
			if reapply {
				rendered.Settings["model"] = "gpt-other"
				nativeTestInstall(t, p, "codex", "other", rendered)
			}
			nativeTestReset(t, p, "codex")
			got := string(nativeTestRead(t, path))
			if !strings.Contains(got, "# personal comment added later") {
				t.Fatalf("lost personal comment: %q", got)
			}
			if original != "absent" && !strings.Contains(got, original) {
				t.Fatalf("lost original text: %q", got)
			}
		}
	}
}

func TestNativeTOMLEditsPreserveUnrelatedText(t *testing.T) {
	for _, text := range []string{
		"# preferences\n\"model\" = 'old' # keep this comment\n\n[unrelated]\nwhen = 2026-10-28T12:00:00Z\nlist = [1, 2, 3] # preserve formatting\n",
		"# preferences\nmodel = \"\"\"old\"\"\" # keep this comment\n\n[unrelated]\nflag = true\n",
		"# no model yet\n[unrelated]\nflag = true\n",
	} {
		p := nativeTestPaths(t)
		path := nativeTestPath(t, p, "codex", "config")
		nativeTestWrite(t, path, []byte(text))
		nativeTestInstall(t, p, "codex", "new", NativeProfile{Settings: map[string]any{"model": "gpt-test"}})
		got := string(nativeTestRead(t, path))
		if !strings.Contains(got, "# preferences") && !strings.Contains(got, "# no model yet") {
			t.Fatalf("lost leading comment: %s", got)
		}
		_, tail, _ := strings.Cut(text, "[unrelated]")
		if !strings.HasSuffix(got, "[unrelated]"+tail) {
			t.Fatalf("changed unrelated TOML: %s", got)
		}
		nativeTestReset(t, p, "codex")
		restored, err := nativeSettings(nativeTestRead(t, path), "codex")
		if err != nil {
			t.Fatal(err)
		}
		original, _ := nativeSettings([]byte(text), "codex")
		if !nativeEqual(restored, original) {
			t.Fatal("did not restore original values")
		}
	}
}

func TestNativeCodexOverrideCannotHideGuide(t *testing.T) {
	p := nativeTestPaths(t)
	nativeTestWrite(t, filepath.Join(p.Home, ".codex", "AGENTS.override.md"), []byte("Override\n"))
	if _, err := PlanNativeInstall(p, "codex", "default", NativeProfile{Guide: []byte("Shared\n")}); err == nil || !strings.Contains(err.Error(), "hide") {
		t.Fatalf("error=%v", err)
	}
}

func TestOpenCodeEffectiveModelsAndFrontmatterMaps(t *testing.T) {
	source := t.TempDir()
	nativeFixture(t, source, "agent.md", "---\ndescription: Review\nmodel:\n  opencode: custom/source\n  claude: opus\n---\nReview\n")
	p := config.Profile{Name: "default", Config: map[string]any{"agent": map[string]any{"reviewer": map[string]any{"model": map[string]any{"claude": "opus"}}}}, Agents: map[string]config.Agent{"reviewer": {File: "agent.md"}}}
	if err := ValidateOpenCodeProfile(source, p); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := renderProfile(dest, source, p, nil, ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "agents", "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	front, _, err := nativeFrontmatter(b)
	if err != nil || front["model"] != "custom/source" {
		t.Fatalf("frontmatter=%v err=%v", front, err)
	}
	p.Agents["reviewer"] = config.Agent{File: "agent.md", Config: map[string]any{"model": "custom/assignment"}}
	if err := ValidateOpenCodeProfile(source, p); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAssignmentMapCannotFallBackToFrontmatter(t *testing.T) {
	source := t.TempDir()
	nativeFixture(t, source, "agent.md", "---\ndescription: Review\nmodel:\n  codex: gpt-source\n---\nReview\n")
	p := config.Profile{Agents: map[string]config.Agent{"reviewer": {File: "agent.md", Config: map[string]any{"model": map[string]any{"claude": "opus"}}}}}
	if _, err := PrepareNativeProfile(source, p, "codex", "", nil); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("error=%v", err)
	}
}

func TestNativeSkillExecutableChangesAndPermissionDrift(t *testing.T) {
	p := nativeTestPaths(t)
	source := t.TempDir()
	nativeFixture(t, source, "SKILL.md", "---\nname: check\ndescription: Check things\n---\nCheck\n")
	nativeFixture(t, source, "script.sh", "#!/bin/sh\nexit 0\n")
	rendered := NativeProfile{Skills: map[string]string{"check": source}}
	nativeTestInstall(t, p, "claude", "default", rendered)
	if err := os.Chmod(filepath.Join(source, "script.sh"), 0o700); err != nil {
		t.Fatal(err)
	}
	nativeTestInstall(t, p, "claude", "updated", rendered)
	installed := nativeTestPath(t, p, "claude", "files/skills/check/script.sh")
	info, err := os.Stat(installed)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("permissions=%v error=%v", info, err)
	}
	if err := os.Chmod(installed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanNativeReset(p, "claude"); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("error=%v", err)
	}
}
