package ocp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeTestPaths(t *testing.T) Paths {
	t.Helper()
	home := t.TempDir()
	return Paths{Home: home, StateFile: filepath.Join(home, ".state", "state.json"), Data: filepath.Join(home, ".data")}
}

func nativeTestWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nativeTestPath(t *testing.T, p Paths, harness, key string) string {
	t.Helper()
	path, err := nativePath(p, harness, key)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func nativeTestRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func nativeTestAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s absent, got %v", path, err)
	}
}

func nativeTestProfile() NativeProfile {
	return NativeProfile{
		Settings: map[string]any{"model": "managed"},
		Files:    map[string][]byte{"agents/reviewer.md": []byte("managed agent\n"), "skills/check/SKILL.md": []byte("managed skill\n")},
		Guide:    []byte("managed instructions\n"),
	}
}

func nativeTestInstall(t *testing.T, p Paths, harness, profile string, rendered NativeProfile) {
	t.Helper()
	plan, err := PlanNativeInstall(p, harness, profile, rendered)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyNativePlan(p, plan); err != nil {
		t.Fatal(err)
	}
}

func nativeTestReset(t *testing.T, p Paths, harness string) {
	t.Helper()
	plan, err := PlanNativeReset(p, harness)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyNativePlan(p, plan); err != nil {
		t.Fatal(err)
	}
}

// Include modes in snapshots so rejected plans cannot silently chmod files.
func nativeTestSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			out[path] = "symlink:" + target
			return err
		}
		b, err := os.ReadFile(path)
		out[path] = info.Mode().String() + ":" + string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func nativeTestUnchanged(t *testing.T, p Paths, before map[string]string) {
	t.Helper()
	after := nativeTestSnapshot(t, p.Home)
	x, _ := json.Marshal(before)
	y, _ := json.Marshal(after)
	if !bytes.Equal(x, y) {
		t.Errorf("operation changed files despite rejection\nbefore: %s\nafter: %s", x, y)
	}
}

func TestNativeInstallPreservesUnrelatedAndResetRestoresModel(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			p := nativeTestPaths(t)
			config := []byte("model = 'original'\ncustom = 'keep'\n")
			if harness == "claude" {
				config = []byte(`{"model":"original","custom":"keep"}`)
			}
			configPath := nativeTestPath(t, p, harness, "config")
			guidePath := nativeTestPath(t, p, harness, "guide")
			nativeTestWrite(t, configPath, config)
			nativeTestWrite(t, guidePath, []byte("user instructions\n"))
			unrelated := nativeTestPath(t, p, harness, "files/agents/personal.md")
			nativeTestWrite(t, unrelated, []byte("personal"))
			before := nativeTestSnapshot(t, p.Home)
			rendered := nativeTestProfile()
			plan, err := PlanNativeInstall(p, harness, "first", rendered)
			if err != nil {
				t.Fatal(err)
			}
			nativeTestUnchanged(t, p, before)
			if err := ApplyNativePlan(p, plan); err != nil {
				t.Fatal(err)
			}
			settings, err := nativeSettings(nativeTestRead(t, configPath), harness)
			if err != nil || settings["model"] != "managed" || settings["custom"] != "keep" {
				t.Fatalf("installed settings = %v, %v", settings, err)
			}
			wantGuide := append([]byte("user instructions\n"), guideBlock(rendered.Guide)...)
			if got := nativeTestRead(t, guidePath); !bytes.Equal(got, wantGuide) {
				t.Errorf("installed guide = %q", got)
			}
			if active, err := NativeActiveProfile(p, harness); err != nil || active != "first" {
				t.Errorf("active = %q, %v", active, err)
			}
			// Unrelated edits after installation must also survive reset.
			b := nativeTestRead(t, configPath)
			b = bytes.ReplaceAll(b, []byte("keep"), []byte("new user value"))
			nativeTestWrite(t, configPath, b)
			nativeTestWrite(t, guidePath, append(wantGuide, []byte("later user instructions\n")...))
			nativeTestReset(t, p, harness)
			settings, err = nativeSettings(nativeTestRead(t, configPath), harness)
			if err != nil || settings["model"] != "original" || settings["custom"] != "new user value" {
				t.Errorf("reset settings = %v, %v", settings, err)
			}
			if got := string(nativeTestRead(t, guidePath)); got != "user instructions\nlater user instructions\n" {
				t.Errorf("reset guide = %q", got)
			}
			for rel := range rendered.Files {
				nativeTestAbsent(t, nativeTestPath(t, p, harness, "files/"+rel))
			}
			if got := string(nativeTestRead(t, unrelated)); got != "personal" {
				t.Errorf("unrelated agent = %q", got)
			}
			nativeTestAbsent(t, nativeStatePath(p, harness))
			if active, err := NativeActiveProfile(p, harness); err != nil || active != "" {
				t.Errorf("reset active = %q, %v", active, err)
			}
		})
	}
}

func TestNativeConflictRefusesWrites(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, kind := range []string{"agent collision", "skill collision", "model edit", "agent edit", "skill edit", "guide edit"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				p := nativeTestPaths(t)
				rendered := nativeTestProfile()
				if !strings.Contains(kind, "collision") {
					nativeTestInstall(t, p, harness, "first", rendered)
				}
				key := "files/agents/reviewer.md"
				switch {
				case strings.HasPrefix(kind, "skill"):
					key = "files/skills/check/SKILL.md"
				case kind == "model edit":
					key = "config"
				case kind == "guide edit":
					key = "guide"
				}
				path := nativeTestPath(t, p, harness, key)
				data := []byte("external edit\n")
				if kind == "model edit" {
					data = bytes.ReplaceAll(nativeTestRead(t, path), []byte("managed"), []byte("external"))
				}
				nativeTestWrite(t, path, data)
				before := nativeTestSnapshot(t, p.Home)
				if _, err := PlanNativeInstall(p, harness, "second", rendered); err == nil {
					t.Error("install accepted unmanaged collision or edited ownership")
				}
				nativeTestUnchanged(t, p, before)
				if !strings.Contains(kind, "collision") {
					if _, err := PlanNativeReset(p, harness); err == nil {
						t.Error("reset accepted edited ownership")
					}
					nativeTestUnchanged(t, p, before)
				}
			})
		}
	}
}

func TestNativeUnsafeDestinationSymlinks(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, key := range []string{"config", "guide", "files/agents/reviewer.md", "files/skills/check/SKILL.md", "state", "ancestor"} {
			t.Run(harness+"/"+key, func(t *testing.T) {
				p := nativeTestPaths(t)
				target := filepath.Join(p.Home, "external", "target")
				nativeTestWrite(t, target, []byte("untouched"))
				path := ""
				if key == "ancestor" {
					path = filepath.Join(p.Home, "."+harness)
					target = filepath.Dir(target)
				} else {
					path = nativeTestPath(t, p, harness, key)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				before := nativeTestSnapshot(t, p.Home)
				if _, err := PlanNativeInstall(p, harness, "first", nativeTestProfile()); err == nil || !strings.Contains(err.Error(), "symlink") {
					t.Errorf("expected symlink rejection, got %v", err)
				}
				nativeTestUnchanged(t, p, before)
			})
		}
	}
}

func TestNativeStalePlanRefusesAllWrites(t *testing.T) {
	for _, key := range []string{"config", "guide", "files/agents/reviewer.md", "state"} {
		t.Run(key, func(t *testing.T) {
			p := nativeTestPaths(t)
			plan, err := PlanNativeInstall(p, "claude", "first", nativeTestProfile())
			if err != nil {
				t.Fatal(err)
			}
			nativeTestWrite(t, nativeTestPath(t, p, "claude", key), []byte("external edit"))
			before := nativeTestSnapshot(t, p.Home)
			if err := ApplyNativePlan(p, plan); err == nil {
				t.Error("accepted stale plan")
			}
			nativeTestUnchanged(t, p, before)
		})
	}
}

func nativeTestJournal(t *testing.T, p Paths, plan NativeInstallPlan, completed int) {
	t.Helper()
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicFile(nativeStatePath(p, plan.Harness)+".pending", b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Changes[:completed] {
		path := nativeTestPath(t, p, plan.Harness, c.Key)
		if c.AfterExists {
			err = atomicFile(path, c.After, os.FileMode(c.Mode))
		} else {
			err = os.Remove(path)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeRecovery(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, completion := range []string{"none", "first", "all"} {
			t.Run(harness+"/"+completion, func(t *testing.T) {
				p := nativeTestPaths(t)
				plan, err := PlanNativeInstall(p, harness, "first", nativeTestProfile())
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				if completion == "first" {
					n = 1
				} else if completion == "all" {
					n = len(plan.Changes)
				}
				nativeTestJournal(t, p, plan, n)
				if _, err := PlanNativeInstall(p, harness, "second", nativeTestProfile()); err == nil {
					t.Error("planning accepted pending update")
				}
				if err := RecoverNative(p, harness); err != nil {
					t.Fatal(err)
				}
				for _, c := range plan.Changes {
					if err := checkNativeChange(p, harness, nativeChange{Key: c.Key, Before: c.After, BeforeExists: c.AfterExists}, false); err != nil {
						t.Error(err)
					}
				}
				nativeTestAbsent(t, nativeStatePath(p, harness)+".pending")
				if err := RecoverNative(p, harness); err != nil {
					t.Errorf("recovery without pending journal: %v", err)
				}
			})
		}
	}
}

func TestNativeRecoveryPreservesExternalEdits(t *testing.T) {
	for _, edited := range []string{"completed", "remaining"} {
		t.Run(edited, func(t *testing.T) {
			p := nativeTestPaths(t)
			plan, err := PlanNativeInstall(p, "claude", "first", nativeTestProfile())
			if err != nil {
				t.Fatal(err)
			}
			nativeTestJournal(t, p, plan, 1)
			index := 0
			if edited == "remaining" {
				index = len(plan.Changes) - 1
			}
			nativeTestWrite(t, nativeTestPath(t, p, plan.Harness, plan.Changes[index].Key), []byte("external edit"))
			before := nativeTestSnapshot(t, p.Home)
			if err := RecoverNative(p, "claude"); err == nil {
				t.Error("recovery accepted external edit")
			}
			nativeTestUnchanged(t, p, before)
		})
	}
}

func TestNativeUnknownHarness(t *testing.T) {
	p := nativeTestPaths(t)
	for name, call := range map[string]func() error{
		"install": func() error { _, err := PlanNativeInstall(p, "unknown", "first", nativeTestProfile()); return err },
		"reset":   func() error { _, err := PlanNativeReset(p, "unknown"); return err },
		"recover": func() error { return RecoverNative(p, "unknown") },
		"active":  func() error { _, err := NativeActiveProfile(p, "unknown"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			before := nativeTestSnapshot(t, p.Home)
			if err := call(); err == nil {
				t.Error("unknown harness accepted")
			}
			nativeTestUnchanged(t, p, before)
		})
	}
}

func TestNativeExecutableSkillPermission(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			p := nativeTestPaths(t)
			source := filepath.Join(p.Home, "source")
			nativeTestWrite(t, filepath.Join(source, "SKILL.md"), []byte("skill"))
			script := filepath.Join(source, "scripts", "check.sh")
			nativeTestWrite(t, script, []byte("#!/bin/sh\nexit 0\n"))
			if err := os.Chmod(script, 0o755); err != nil {
				t.Fatal(err)
			}
			nativeTestInstall(t, p, harness, "first", NativeProfile{Skills: map[string]string{"check": source}})
			for rel, mode := range map[string]os.FileMode{"SKILL.md": 0o600, "scripts/check.sh": 0o700} {
				path := nativeTestPath(t, p, harness, "files/skills/check/"+rel)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != mode {
					t.Errorf("%s mode = %o, want %o", rel, info.Mode().Perm(), mode)
				}
				if !bytes.Equal(nativeTestRead(t, path), nativeTestRead(t, filepath.Join(source, rel))) {
					t.Errorf("%s content differs", rel)
				}
			}
		})
	}
}

func TestNativeCrossHarnessIndependentState(t *testing.T) {
	p := nativeTestPaths(t)
	nativeTestInstall(t, p, "claude", "claude-profile", nativeTestProfile())
	nativeTestInstall(t, p, "codex", "codex-profile", nativeTestProfile())
	codexBefore := map[string][]byte{}
	for _, key := range []string{"config", "guide", "files/agents/reviewer.md", "files/skills/check/SKILL.md", "state"} {
		path := nativeTestPath(t, p, "codex", key)
		codexBefore[path] = nativeTestRead(t, path)
	}
	nativeTestReset(t, p, "claude")
	for path, want := range codexBefore {
		if got := nativeTestRead(t, path); !bytes.Equal(got, want) {
			t.Errorf("reset claude changed %s", path)
		}
	}
	if active, err := NativeActiveProfile(p, "codex"); err != nil || active != "codex-profile" {
		t.Errorf("codex active = %q, %v", active, err)
	}
	nativeTestReset(t, p, "codex")
}

func TestNativeProfileSwitchRemovesPriorFiles(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			p := nativeTestPaths(t)
			first := nativeTestProfile()
			nativeTestInstall(t, p, harness, "first", first)
			second := NativeProfile{Settings: map[string]any{"model": "second"}, Files: map[string][]byte{"agents/new.md": []byte("new")}}
			nativeTestInstall(t, p, harness, "second", second)
			for rel := range first.Files {
				nativeTestAbsent(t, nativeTestPath(t, p, harness, "files/"+rel))
			}
			nativeTestAbsent(t, nativeTestPath(t, p, harness, "guide"))
			if got := string(nativeTestRead(t, nativeTestPath(t, p, harness, "files/agents/new.md"))); got != "new" {
				t.Errorf("new agent = %q", got)
			}
			if active, err := NativeActiveProfile(p, harness); err != nil || active != "second" {
				t.Errorf("active = %q, %v", active, err)
			}
			nativeTestReset(t, p, harness)
			nativeTestAbsent(t, nativeTestPath(t, p, harness, "config"))
		})
	}
}

func TestNativeResetRestoresOriginallyEmptyFiles(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			p := nativeTestPaths(t)
			for _, key := range []string{"config", "guide"} {
				nativeTestWrite(t, nativeTestPath(t, p, harness, key), []byte{})
			}
			nativeTestInstall(t, p, harness, "first", nativeTestProfile())
			nativeTestReset(t, p, harness)
			for _, key := range []string{"config", "guide"} {
				if got := nativeTestRead(t, nativeTestPath(t, p, harness, key)); len(got) != 0 {
					t.Errorf("originally empty %s restored as %q", key, got)
				}
			}
		})
	}
}
