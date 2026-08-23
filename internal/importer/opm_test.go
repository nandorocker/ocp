package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasOPMProfilesNoDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if HasOPMProfiles() {
		t.Fatal("expected no OPM profiles when dir does not exist")
	}
}

func TestListOPMProfilesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	profiles, err := ListOPMProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestImportOPMWithValidProfile(t *testing.T) {
	tmp := t.TempDir()
	opmDir := filepath.Join(tmp, ".config", "opm", "profiles")
	profileDir := filepath.Join(opmDir, "test-profile")
	if err := os.MkdirAll(filepath.Join(profileDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"model":"openrouter/test","provider":{}}`
	if err := os.WriteFile(filepath.Join(profileDir, "opencode.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "AGENTS.md"), []byte("rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmp)

	target := filepath.Join(tmp, "source")
	err := ImportOPM(target, true, func(profiles []string) (string, bool) {
		if len(profiles) != 1 || profiles[0] != "test-profile" {
			t.Fatalf("unexpected profiles: %v", profiles)
		}
		return profiles[0], true
	})
	if err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(target, "ocp.yaml")
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "version:") {
		t.Fatalf("missing version in imported YAML:\n%s", text)
	}
	if !strings.Contains(text, "model:") {
		t.Fatalf("missing config in imported YAML:\n%s", text)
	}
}

func TestImportOPMSkipsNonOpenCodeProfiles(t *testing.T) {
	tmp := t.TempDir()
	opmDir := filepath.Join(tmp, ".config", "opm", "profiles")
	// A directory without opencode.json should be skipped
	if err := os.MkdirAll(filepath.Join(opmDir, "no-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmp)

	profiles, err := ListOPMProfiles(opmDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p == "no-config" {
			t.Fatal("non-OpenCode profile should not appear in list")
		}
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 OpenCode profiles, got %d", len(profiles))
	}
}
