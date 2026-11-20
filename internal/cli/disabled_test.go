package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisabledSkillIsNotRenderedAndIsShown(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nskills: [keep, heavy]\nprofiles:\n  default: {}\n  lean:\n    disabled: [heavy, gone]\n")
	for _, name := range []string{"keep", "heavy"} {
		write(t, filepath.Join(source, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: d\n---\n")
	}
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `profile lean: disabled "gone" matches no skill or plugin`) {
		t.Errorf("no warning for an unmatched name:\n%s", out.String())
	}
	lean := filepath.Join(p.Current, "lean", "skills")
	if _, err := os.Stat(filepath.Join(lean, "keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lean, "heavy")); !os.IsNotExist(err) {
		t.Fatalf("disabled skill rendered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Current, "default", "skills", "heavy")); err != nil {
		t.Fatalf("disabling in lean affected default: %v", err)
	}
	out.Reset()
	if err := r.Run([]string{"show", "lean"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disabled\n  heavy\n  gone\n") {
		t.Errorf("show lean:\n%s", out.String())
	}
}
