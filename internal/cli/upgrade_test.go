package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUpgradeArguments(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		name    string
		preview bool
		err     string
	}{
		{args: nil},
		{args: []string{"--preview"}, preview: true},
		{args: []string{"skill", "pdf"}, name: "pdf"},
		{args: []string{"skill", "pdf", "--preview"}, name: "pdf", preview: true},
		{args: []string{"--preview", "skill", "pdf"}, name: "pdf", preview: true},
		{args: []string{"skill"}, err: "requires a skill name"},
		{args: []string{"pdf"}, err: "unexpected arguments"},
		{args: []string{"skill", "a", "b"}, err: "unexpected arguments"},
		{args: []string{"--bogus"}, err: "bogus"},
	} {
		var o upgradeOptions
		name, err := parseUpgrade(tc.args, &o)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("%v: error = %v, want %q", tc.args, err, tc.err)
			}
			continue
		}
		if err != nil || name != tc.name || o.preview != tc.preview {
			t.Fatalf("%v: name %q preview %v err %v", tc.args, name, o.preview, err)
		}
	}
}

func TestUpgradeWithoutGitSkills(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nskills: [local]\n")
	write(t, filepath.Join(source, "skills", "local", "SKILL.md"), "---\nname: local\ndescription: d\n---\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := r.Run([]string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No Git skills to upgrade") {
		t.Fatalf("output = %q", out.String())
	}
	err := r.Run([]string{"upgrade", "skill", "local"})
	if err == nil || !strings.Contains(err.Error(), "local skills are not versioned") {
		t.Fatalf("local skill error = %v", err)
	}
}
