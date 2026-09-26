package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func source(t *testing.T, root string) Store {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "ocp.yaml"), []byte(root), 0600); err != nil {
		t.Fatal(err)
	}
	return Store{Source: d}
}
func TestStoreProfilesAndAgents(t *testing.T) {
	s := source(t, "version: 1\nagents:\n  base:\n    file: agents/base.md\nconfig:\n  model: root\n")
	before, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProfile(before.Revision, "work", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Source, "profiles", "default.yaml")); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAgent(snap.Revision, "base.md", "body\n"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(snap.Revision, "work", ProfileUpdate{Extends: "default", Model: "child", Agents: []DirectAgent{{Name: "base", File: "base.md", Model: "agent"}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Source, "profiles", "work.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "file: ./agents/base.md") {
		t.Fatalf("library path not serialized: %s", b)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var work ProfileView
	for _, p := range snap.Profiles {
		if p.Name == "work" {
			work = p
		}
	}
	if got := work.ModelOrigin; got != "work" {
		t.Fatalf("model origin %q", got)
	}
	if _, err = s.UpdateAgent(snap.Revision, "base.md", "---\nmodel: fm\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range snap.Profiles {
		if p.Name == "work" && (p.Agents[0].SourceModel != "fm" || p.Agents[0].Model != "agent" || p.Agents[0].ModelOrigin != "work") {
			t.Fatalf("agent precedence = %#v", p.Agents[0])
		}
	}
	if _, err = s.DeleteAgent(snap.Revision, "base.md"); err == nil || !strings.Contains(err.Error(), "referenced") {
		t.Fatalf("delete referenced: %v", err)
	}
	if _, err = s.CreateAgent(snap.Revision, "free.md", "one"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateAgent(snap.Revision, "free.md", "two"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteAgent(snap.Revision, "free.md"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteProfile(snap.Revision, "default"); err == nil {
		t.Fatal("deleted dependent profile")
	}
}
func TestImplicitDefaultAndLibrarySafety(t *testing.T) {
	s := source(t, "version: 1\nagents:\n  outside:\n    file: other/reviewer.md\n")
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Profiles) != 1 || len(snap.Profiles[0].DirectAgents) != 0 {
		t.Fatal("implicit default exposed shared agents as direct")
	}
	if _, err = s.UpdateProfile(snap.Revision, "default", ProfileUpdate{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Source, "profiles", "default.yaml")); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(snap.Revision, "default", ProfileUpdate{Agents: []DirectAgent{{Name: "outside", File: "missing.md"}}}); err == nil {
		t.Fatal("unknown library file accepted")
	}
	if snap.Profiles[0].Agents[0].File != "other/reviewer.md" || snap.Profiles[0].Agents[0].SourceModel != "" {
		t.Fatal("arbitrary path treated as library")
	}
	profilePath := filepath.Join(s.Source, "profiles", "default.yaml")
	if err = os.WriteFile(profilePath, []byte("config:\n  model: m\nagents:\n  outside:\n    file: other/reviewer.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(snap.Revision, "default", ProfileUpdate{Model: "next", Agents: []DirectAgent{{Name: "outside", File: "other/reviewer.md"}}}); err != nil {
		t.Fatalf("preserve outside source: %v", err)
	}
	b, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "file: other/reviewer.md") {
		t.Fatalf("outside source changed: %s", b)
	}
}

func TestSnapshotReadsModelFromSafeNonLibraryAgent(t *testing.T) {
	s := source(t, "version: 1\nagents:\n  reviewer:\n    file: other/reviewer.md\nconfig:\n  model: profile/model\n")
	path := filepath.Join(s.Source, "other", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nmodel: source/model\n---\nReview.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	agent := snapshot.Profiles[0].Agents[0]
	if agent.Model != "source/model" || agent.SourceModel != "source/model" || agent.ModelOrigin != "reviewer.md" {
		t.Fatalf("agent = %#v", agent)
	}
}

func TestPreviewProfileResolvesWithoutWriting(t *testing.T) {
	s := source(t, "version: 1\nconfig:\n  model: root\nagents:\n  worker:\n    file: agents/worker.md\n")
	if err := os.MkdirAll(filepath.Join(s.Source, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Source, "profiles", "parent.yaml"), []byte("config:\n  model: parent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Source, "profiles", "child.yaml"), []byte("extends: parent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Source, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	canonical := "---\nmodel: source\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(s.Source, "agents", "worker.md"), []byte(canonical), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	update := ProfileUpdate{Extends: "parent", Model: "child", Agents: []DirectAgent{{Name: "worker", File: "worker.md", Model: "assignment"}}}
	preview, err := s.PreviewProfile(snap.Revision, "child", update)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Model != "child" || preview.Agents[0].Model != "assignment" || preview.Agents[0].ModelOrigin != "child" || preview.Agents[0].SourceModel != "source" {
		t.Fatalf("preview = %#v", preview)
	}
	if got, err := os.ReadFile(filepath.Join(s.Source, "profiles", "child.yaml")); err != nil || string(got) != "extends: parent\n" {
		t.Fatalf("preview wrote profile: %q, %v", got, err)
	}
	if _, err := s.PreviewProfile("stale", "child", update); err == nil {
		t.Fatal("stale preview accepted")
	}
	if _, err := s.PreviewProfile(snap.Revision, "child", ProfileUpdate{Extends: "child"}); err == nil {
		t.Fatal("cyclic preview accepted")
	}
	if _, err := s.PreviewProfile(snap.Revision, "child", ProfileUpdate{Extends: "missing"}); err == nil {
		t.Fatal("missing parent preview accepted")
	}
	after, err := s.UpdateProfile(snap.Revision, "child", update)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range after.Profiles {
		if p.Name == "child" && (p.Model != preview.Model || p.Agents[0].Model != preview.Agents[0].Model) {
			t.Fatalf("save projection %#v differs from preview %#v", p, preview)
		}
	}
}
func TestSymlinkedChildDirectoryRejected(t *testing.T) {
	s := source(t, "version: 1\n")
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(s.Source, "agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("symlinked agents directory accepted")
	}
	s = source(t, "version: 1\n")
	if err := os.Symlink(target, filepath.Join(s.Source, "profiles")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("symlinked profiles directory accepted")
	}
}
func TestStoreInlineStaleAndValidation(t *testing.T) {
	s := source(t, "version: 1\nprofiles:\n  default: {}\n")
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Editable || !strings.Contains(snap.MigrationHint, "--migrate-profiles") {
		t.Fatal("inline source editable")
	}
	if _, err = s.CreateAgent(snap.Revision, "x.md", "x"); err == nil {
		t.Fatal("inline mutation accepted")
	}
	s = source(t, "version: 1\n")
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProfile(snap.Revision, "one", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProfile(snap.Revision, "two", "", ""); err == nil {
		t.Fatal("stale revision accepted")
	} else {
		var c *ConflictError
		if !errors.As(err, &c) {
			t.Fatalf("not conflict: %v", err)
		}
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Source, "profiles", "one.yaml")
	old, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateProfile(snap.Revision, "one", ProfileUpdate{Extends: "missing"})
	if err == nil {
		t.Fatal("invalid update accepted")
	}
	now, _ := os.ReadFile(p)
	if string(old) != string(now) {
		t.Fatal("invalid update changed bytes")
	}
	if _, err = s.DeleteProfile(snap.Revision, "default"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteProfile(snap.Revision, "one"); err == nil {
		t.Fatal("deleted last profile")
	}
}
func TestUpdatePreservesConfig(t *testing.T) {
	s := source(t, "version: 1\n")
	snap, _ := s.Snapshot()
	_, err := s.CreateProfile(snap.Revision, "p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Source, "profiles", "p.yaml")
	if err = os.WriteFile(p, []byte("# keep\nconfig:\n  temperature: 1 # here\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(snap.Revision, "p", ProfileUpdate{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "temperature: 1") || !strings.Contains(string(b), "# here") {
		t.Fatalf("config not preserved: %s", b)
	}
}

func TestAgentShorthandAndNormalizedUsage(t *testing.T) {
	s := source(t, "version: 1\n")
	if err := os.MkdirAll(filepath.Join(s.Source, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Source, "agents", "worker.md"), []byte("body\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Source, "profiles"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := "agents:\n  worker:\n    file: agents/../agents/worker.md\n    model: provider/model\n"
	if err := os.WriteFile(filepath.Join(s.Source, "profiles", "default.yaml"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Profiles[0].DirectAgents[0].Model; got != "provider/model" {
		t.Fatalf("shorthand model = %q", got)
	}
	if _, err := s.DeleteAgent(snap.Revision, "worker.md"); err == nil || !strings.Contains(err.Error(), "referenced") {
		t.Fatalf("normalized reference deletion = %v", err)
	}
	if _, err := s.UpdateProfile(snap.Revision, "default", ProfileUpdate{Agents: snap.Profiles[0].DirectAgents}); err != nil {
		t.Fatal(err)
	}
	next, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := next.Profiles[0].Agents[0].Model; got != "provider/model" {
		t.Fatalf("model after save = %q", got)
	}
}
