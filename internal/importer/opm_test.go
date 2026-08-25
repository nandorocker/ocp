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

func TestMergeProfilesCopiesAgentFiles(t *testing.T) {
	src := t.TempDir()

	writeFile := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	profile1 := filepath.Join(src, "profile1")
	writeFile(filepath.Join(profile1, "opencode.json"), `{"agent":{"build":{"model":"test"}}}`)
	writeFile(filepath.Join(profile1, "AGENTS.md"), "guide\n")
	writeFile(filepath.Join(profile1, "agent", "deep-explore.md"), "explore agent\n")
	writeFile(filepath.Join(profile1, "agent", "chore.md"), "chore agent\n")

	profile2 := filepath.Join(src, "profile2")
	writeFile(filepath.Join(profile2, "opencode.json"), `{"config":{"custom":"val"}}`)
	writeFile(filepath.Join(profile2, "agent", "deep-explore.md"), "explore agent v2\n") // dup name
	writeFile(filepath.Join(profile2, "agent", "research.md"), "research agent\n")       // unique name

	source := t.TempDir()
	data1, err := ReadProfileData(profile1)
	if err != nil {
		t.Fatalf("read profile1: %v", err)
	}
	data2, err := ReadProfileData(profile2)
	if err != nil {
		t.Fatalf("read profile2: %v", err)
	}

	if err := MergeProfiles([]*ImportProfileData{data1, data2}, source, true); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if _, err := os.Stat(filepath.Join(source, "ocp.yaml")); err != nil {
		t.Fatal("ocp.yaml not created")
	}

	agentsDir := filepath.Join(source, "agents")
	for _, f := range []string{"deep-explore.md", "deep-explore--profile2.md", "chore.md", "research.md"} {
		path := filepath.Join(agentsDir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected agent file %s but got: %v", path, err)
		}
	}

	content, err := os.ReadFile(filepath.Join(agentsDir, "deep-explore.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "explore agent\n" {
		t.Fatalf("deep-explore.md content = %q, want %q", string(content), "explore agent\n")
	}

	if _, err := os.Stat(filepath.Join(source, "instructions", "profile1.md")); err != nil {
		t.Error("profile instructions not copied")
	}

	// Verify profile files are written instead of inline profiles
	profilesDir := filepath.Join(source, "profiles")
	for _, name := range []string{"profile1", "profile2"} {
		profPath := filepath.Join(profilesDir, name+".yaml")
		if _, err := os.Stat(profPath); err != nil {
			t.Errorf("expected profile file %s", profPath)
		} else {
			content, err := os.ReadFile(profPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(content), "extends:") {
				t.Errorf("imported profile should remain independent, got:\n%s", content)
			}
			if name == "profile2" && !strings.Contains(string(content), "file: ./agents/deep-explore--profile2.md") {
				t.Errorf("profile2 should reference its distinct agent source, got:\n%s", content)
			}
		}
	}

	// Ensure ocp.yaml does NOT contain inline profiles
	yamlContent, err := os.ReadFile(filepath.Join(source, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(yamlContent)
	if strings.Contains(text, "profiles:") {
		t.Error("ocp.yaml should not contain inline profiles key")
	}
}

func TestMergeProfilesPreflightsAndReplacesProfileDirectory(t *testing.T) {
	root := t.TempDir()
	profile1 := filepath.Join(root, "profile1")
	profile2 := filepath.Join(root, "profile2")
	for _, profile := range []string{profile1, profile2} {
		if err := os.MkdirAll(profile, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile, "opencode.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data1, err := ReadProfileData(profile1)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := ReadProfileData(profile2)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "ocp.yaml"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MergeProfiles([]*ImportProfileData{data1, data2}, source, false); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("MergeProfiles error = %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(source, "ocp.yaml")); err != nil || string(content) != "keep\n" {
		t.Fatalf("ocp.yaml = %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(source, "profiles")); !os.IsNotExist(err) {
		t.Fatalf("profiles written before preflight completed: %v", err)
	}

	if err := MergeProfiles([]*ImportProfileData{data1, data2}, source, true); err != nil {
		t.Fatal(err)
	}
	if err := MergeProfiles([]*ImportProfileData{data1}, source, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "profiles", "profile2.yaml")); !os.IsNotExist(err) {
		t.Fatalf("stale profile remains after forced import: %v", err)
	}
}
