package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/config"
)

func TestCachedProviderDoesNotPopulateMissingCheckout(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: "https://example.test/org/skill.git"}
	lock := Lock{Version: 1, Skills: map[string]LockEntry{canonicalSource(skill.Source): {Source: canonicalSource(skill.Source), Commit: strings.Repeat("a", 40)}}}
	if err := writeLock(filepath.Join(source, lockFileName), lock); err != nil {
		t.Fatal(err)
	}
	p, err := Cached(source, data, []config.Skill{skill})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SkillDir(skill); err == nil || !strings.Contains(err.Error(), "not cached") {
		t.Fatalf("error=%v", err)
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cache changed: %v %v", entries, err)
	}
}

func TestCachedProviderReusesLockedCheckout(t *testing.T) {
	repo, _ := bareRepository(t)
	source, data := t.TempDir(), t.TempDir()
	skill := config.Skill{Source: repo, Ref: "main"}
	prepared, _, err := prepare(source, data, []config.Skill{skill}, true, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	want, err := prepared.SkillDir(skill)
	if err != nil {
		t.Fatal(err)
	}
	p := cachedProvider{prepared.(*provider)}
	if got, err := p.SkillDir(skill); err != nil || got != want {
		t.Fatalf("got=%q error=%v", got, err)
	}
	if _, err := p.SkillDir(config.Skill{Source: repo, Ref: "other"}); err == nil {
		t.Fatal("accepted mismatched ref")
	}
}
