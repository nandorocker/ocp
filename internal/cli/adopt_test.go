package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptWriteThenSetup(t *testing.T) {
	p := testPaths(t)
	home := t.TempDir()
	p.Home = home
	write(t, filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"), "---\nname: mine\ndescription: d\n---\n")
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), "Be brief.\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"adopt"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "mine") || !strings.Contains(out.String(), "ocp adopt --write") {
		t.Fatalf("report:\n%s", out.String())
	}
	dir := filepath.Join(t.TempDir(), "adopted")
	out.Reset()
	if err := r.Run([]string{"adopt", "--write", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "mine", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := r.Run([]string{"setup", "--source", dir}); err != nil {
		t.Fatalf("setup from adopted source: %v", err)
	}
	if err := r.Run([]string{"adopt", "--write", filepath.Join(t.TempDir(), "again")}); err == nil || !strings.Contains(err.Error(), "already has a source") {
		t.Fatalf("second write = %v", err)
	}
}
