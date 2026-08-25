package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/ocp"
)

func testPaths(t *testing.T) ocp.Paths {
	t.Helper()
	root := t.TempDir()
	return ocp.Paths{OpenCode: filepath.Join(root, "config", "opencode"), Data: filepath.Join(root, "data", "ocp"), Releases: filepath.Join(root, "data", "ocp", "releases"), Current: filepath.Join(root, "data", "ocp", "current"), StateFile: filepath.Join(root, "state", "ocp", "state.json"), LockFile: filepath.Join(root, "config", ".opencode.ocp.lock")}
}
func runner(p ocp.Paths, out *bytes.Buffer) *Runner {
	return &Runner{In: strings.NewReader(""), Out: out, Err: out, Paths: func() (ocp.Paths, error) { return p, nil }}
}
func write(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupApplyUseListStatusAndReset(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nprofiles:\n  default: {}\n  deep: {}\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Setup complete") {
		t.Fatalf("setup output: %s", out.String())
	}
	if err := r.Run([]string{"use", "deep"}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nprofiles:\n  default: {}\n")
	if err := r.Run([]string{"apply"}); err != nil {
		t.Fatal(err)
	}
	active, err := ocp.ActiveProfile(p)
	if err != nil || active != "default" {
		t.Fatalf("active=%q err=%v", active, err)
	}
	out.Reset()
	if err := r.Run([]string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "* default") {
		t.Fatalf("list: %s", out.String())
	}
	out.Reset()
	if err := r.Run([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Auto-commit: true") {
		t.Fatalf("status: %s", out.String())
	}
	if err := r.Run([]string{"reset"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("reset deleted source: %v", err)
	}
}

func TestSetupMigrateProfilesForConfiguredSource(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nprofiles:\n  default: {}\n  deep:\n    extends: default\n    config:\n      model: deep/model\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := r.Run([]string{"setup", "--migrate-profiles"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Migrated inline profiles") {
		t.Fatalf("output = %q", out.String())
	}
	root, err := os.ReadFile(filepath.Join(source, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(root), "profiles:") {
		t.Fatalf("root still contains profiles: %s", root)
	}
	if _, err := os.Stat(filepath.Join(source, "profiles", "deep.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRefusesDriftWithoutForce(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(p.Current, "default", "opencode.json"), "{}\nchanged\n")
	err := r.Run([]string{"apply"})
	if err == nil || !strings.Contains(err.Error(), "rerun with --force") {
		t.Fatalf("drift error = %v", err)
	}
	if err := r.Run([]string{"apply", "--force"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunPassesArgumentsEnvironmentAndExitCode(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	fake := filepath.Join(t.TempDir(), "fake-opencode")
	write(t, fake, "#!/bin/sh\nprintf '%s|%s|%s|%s' \"$OPENCODE_CONFIG_DIR\" \"$XDG_CONFIG_HOME\" \"$1\" \"$2\"\nexit 7\n")
	if err := os.Chmod(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	r.OpenCode = fake
	err := r.Run([]string{"run", "default", "one", "two words"})
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 7 {
		t.Fatalf("run error=%v", err)
	}
	parts := strings.Split(out.String(), "|")
	if len(parts) != 4 || !filepath.IsAbs(parts[0]) || parts[1] != ocp.OneOffConfigHome(p) || parts[2] != "one" || parts[3] != "two words" {
		t.Fatalf("fake OpenCode output=%q", out.String())
	}
}

func TestImportBasic(t *testing.T) {
	p := testPaths(t)
	input := filepath.Join(t.TempDir(), "open")
	write(t, filepath.Join(input, "opencode.json"), `{"model":"x","unknown":{"a":1}}`)
	write(t, filepath.Join(input, "AGENTS.md"), "rules\n")
	write(t, filepath.Join(input, "agents", "worker.md"), "agent\n")
	write(t, filepath.Join(input, "skills", "demo", "SKILL.md"), "skill\n")
	source := filepath.Join(t.TempDir(), "source")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"import", "--source", source, input}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(source, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "unknown:") {
		t.Fatalf("import did not preserve config: %s", b)
	}
}

func TestSelectProfiles(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single number toggles",
			input: "1\n2\n\n",
			want:  []string{"alpha", "beta"},
		},
		{
			name:  "multiple numbers in one line",
			input: "1 3\n\n",
			want:  []string{"alpha", "gamma"},
		},
		{
			name:  "select all via a then submit",
			input: "a\n\n",
			want:  []string{"alpha", "beta", "gamma"},
		},
		{
			name:  "deselect all via a, then submit empty",
			input: "a\na\n\n",
			want:  []string{},
		},
		{
			name:  "toggle one then select all",
			input: "1\na\n\n",
			want:  []string{"alpha", "beta", "gamma"},
		},
		{
			name:  "skip with blank line",
			input: "\n",
			want:  []string{},
		},
		{
			name:  "invalid then valid",
			input: "5\n1\n\n",
			want:  []string{"alpha"},
		},
		{
			name:  "mixed valid and invalid",
			input: "0 1 99 2\n\n",
			want:  []string{"alpha", "beta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := []string{"alpha", "beta", "gamma"}
			in := strings.NewReader(tt.input)
			var out, err bytes.Buffer
			r := &Runner{In: in, Out: &out, Err: &err}
			result, e := r.selectProfiles(profiles, "Choose profiles to import:")
			if e != nil {
				t.Fatalf("unexpected error: %v", e)
			}
			if len(result) != len(tt.want) {
				t.Fatalf("got %d profiles: %v, want %d: %v", len(result), result, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				if result[i] != w {
					t.Fatalf("result[%d] = %q, want %q", i, result[i], w)
				}
			}
		})
	}
}

func TestSelectSingle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "select by number",
			input: "1\n",
			want:  "alpha",
		},
		{
			name:  "select second profile",
			input: "2\n",
			want:  "beta",
		},
		{
			name:  "skip with blank line",
			input: "\n",
			want:  "",
		},
		{
			name:  "invalid then valid",
			input: "5\n1\n",
			want:  "alpha",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := []string{"alpha", "beta", "gamma"}
			in := strings.NewReader(tt.input)
			var out, err bytes.Buffer
			r := &Runner{In: in, Out: &out, Err: &err}
			result, e := r.selectSingle(profiles, "Test menu:")
			if e != nil {
				t.Fatalf("unexpected error: %v", e)
			}
			if result != tt.want {
				t.Fatalf("got %q, want %q", result, tt.want)
			}
		})
	}
}

func TestSelectAllFirstOnly(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "pick a profile",
			input: "2\n\n",
			want:  "beta",
		},
		{
			name:  "skip picks first",
			input: "\n",
			want:  "alpha",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := []string{"alpha", "beta", "gamma"}
			in := strings.NewReader(tt.input)
			p := testPaths(t)
			var out, err bytes.Buffer
			r := &Runner{In: in, Out: &out, Err: &err, Paths: func() (ocp.Paths, error) { return p, nil }}
			first, e := r.selectAllFirstOnly(p, profiles)
			if e != nil {
				t.Fatal(e)
			}
			if first != tt.want {
				t.Fatalf("first = %q, want %q", first, tt.want)
			}
		})
	}
}
