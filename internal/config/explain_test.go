package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeSource(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func explain(t *testing.T, dir string) Explanation {
	t.Helper()
	doc, err := Load(filepath.Join(dir, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := doc.ExplainFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ex
}

func profileView(t *testing.T, ex Explanation, name string) ProfileView {
	t.Helper()
	for _, v := range ex.Profiles {
		if v.Profile.Name == name {
			return v
		}
	}
	t.Fatalf("profile %q missing", name)
	return ProfileView{}
}

// who is a compact attribution for assertions: "global", "extends base",
// "profile", "@windy" for a machine overlay, with a trailing "*" for an override.
func who(a Attribution) string {
	s := a.Origin
	if a.Machine != "" {
		s = "@" + a.Machine
	}
	if a.Override {
		s += "*"
	}
	return s
}

func skillWho(v ProfileView) map[string]string {
	out := map[string]string{}
	for _, s := range v.Skills {
		key := s.Skill.Name()
		if s.Machine != "" {
			key += "@" + s.Machine
		}
		out[key] = who(s.Attribution)
	}
	return out
}

func pluginWho(v ProfileView) map[string]string {
	out := map[string]string{}
	for _, p := range v.Plugins {
		out[p.Plugin] = who(p.Attribution)
	}
	return out
}

func configWho(v ProfileView, machine string) map[string]string {
	out := map[string]string{}
	for _, c := range v.Config {
		if c.Machine == machine {
			out[c.Path] = who(c.Attribution)
		}
	}
	return out
}

const explainRoot = `version: 1
machines:
  windy:
    config:
      mcp:
        servers:
          mobbin: {type: remote, url: "https://example.test/mcp"}
          figma: {type: remote, url: "https://example.test/figma"}
  cosmonaut:
    config:
      mcp:
        servers:
          playwright: {type: local, command: [npx]}
          figma: {type: local, command: [figma]}
skills:
  - github:owner/root-skills//skills/shared
  - local-root
`

func TestExplainAttributesEachLayer(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml": explainRoot,
		"profiles/base/profile.yaml": `config:
  model: base/model
plugins:
  - base-plugin.ts
skills:
  - github:owner/base-skills//skills/from-base
`,
		"profiles/base/files/a.txt": "a",
		"profiles/child/profile.yaml": `extends: base
config:
  model: child/model
plugins:
  - child-plugin
skills:
  - own-skill
`,
		"profiles/child/files/b.txt": "b",
	})
	child := profileView(t, explain(t, dir), "child")

	wantSkills := map[string]string{"shared": "global", "local-root": "global", "from-base": "extends base", "own-skill": "profile"}
	if got := skillWho(child); !reflect.DeepEqual(got, wantSkills) {
		t.Errorf("skills = %v, want %v", got, wantSkills)
	}
	wantPlugins := map[string]string{"base-plugin.ts": "extends base", "child-plugin": "profile"}
	if got := pluginWho(child); !reflect.DeepEqual(got, wantPlugins) {
		t.Errorf("plugins = %v, want %v", got, wantPlugins)
	}
	// model is set by base and replaced by child: an override of the inherited value.
	if got := configWho(child, "")["model"]; got != "profile*" {
		t.Errorf("model = %q, want profile*", got)
	}
	files := map[string]string{}
	for _, f := range child.Files {
		files[f.Name] = f.Origin
	}
	if want := map[string]string{"a.txt": "extends base", "b.txt": "profile"}; !reflect.DeepEqual(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	if child.Extends != "base" {
		t.Errorf("Extends = %q", child.Extends)
	}
}

func TestExplainShowsEveryMachineOverlay(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                   explainRoot,
		"profiles/base/profile.yaml": "{}\n",
	})
	ex := explain(t, dir)
	if want := []string{"cosmonaut", "windy"}; !reflect.DeepEqual(ex.Machines, want) {
		t.Fatalf("machines = %v, want %v", ex.Machines, want)
	}
	v := profileView(t, ex, "base")
	if got, want := configWho(v, "windy"), map[string]string{"mcp.servers.mobbin": "@windy", "mcp.servers.figma": "@windy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("windy overlay = %v, want %v", got, want)
	}
	// figma is defined by both machines: alternatives, never overrides of each other.
	if got, want := configWho(v, "cosmonaut"), map[string]string{"mcp.servers.playwright": "@cosmonaut", "mcp.servers.figma": "@cosmonaut"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cosmonaut overlay = %v, want %v", got, want)
	}
	if len(configWho(v, "")) != 0 {
		t.Errorf("base profile has no shared config here: %v", configWho(v, ""))
	}
}

func TestExplainIncludesHostLimitedProfilesAndHidesUnreachableOverlays(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                         explainRoot,
		"profiles/everywhere/profile.yaml": "{}\n",
		"profiles/lab/profile.yaml":        "hosts: [cosmonaut]\n",
	})
	ex := explain(t, dir)
	lab := profileView(t, ex, "lab")
	if !reflect.DeepEqual(lab.Hosts, []string{"cosmonaut"}) {
		t.Fatalf("lab hosts = %v", lab.Hosts)
	}
	if len(configWho(lab, "windy")) != 0 {
		t.Errorf("windy's overlay can never apply to a cosmonaut-only profile: %v", configWho(lab, "windy"))
	}
	if len(configWho(lab, "cosmonaut")) == 0 {
		t.Error("cosmonaut's overlay applies to lab")
	}
	if len(configWho(profileView(t, ex, "everywhere"), "windy")) == 0 {
		t.Error("an unrestricted profile gets every machine's overlay")
	}
}

// TestExplainOverridesMeanReplacedDifferentValues pins the rule: an override is
// a different value replacing one a lower layer defined. New entries and exact
// repeats are not overrides.
func TestExplainOverridesMeanReplacedDifferentValues(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		check func(t *testing.T, v ProfileView)
	}{
		{
			name: "child re-declares a skill at another ref",
			files: map[string]string{
				"ocp.yaml":                "version: 1\nskills: [github:o/r@v1]\n",
				"profiles/p/profile.yaml": "skills: [github:o/r@v2]\n",
			},
			check: func(t *testing.T, v ProfileView) { wantSkill(t, v, "r", "v2", "profile*") },
		},
		{
			name: "new skill is not an override",
			files: map[string]string{
				"ocp.yaml":                "version: 1\n",
				"profiles/p/profile.yaml": "skills: [github:o/r@v2]\n",
			},
			check: func(t *testing.T, v ProfileView) { wantSkill(t, v, "r", "v2", "profile") },
		},
		{
			name: "identical skill declaration keeps its first origin and is no override",
			files: map[string]string{
				"ocp.yaml":                "version: 1\nskills: [github:o/r@v1]\n",
				"profiles/p/profile.yaml": "skills: [github:o/r@v1]\n",
			},
			check: func(t *testing.T, v ProfileView) { wantSkill(t, v, "r", "v1", "global") },
		},
		{
			name: "plugin replaced by identity at another version",
			files: map[string]string{
				"ocp.yaml":                   "version: 1\n",
				"profiles/base/profile.yaml": "plugins: [foo@1]\n",
				"profiles/p/profile.yaml":    "extends: base\nplugins: [foo@2]\n",
			},
			check: func(t *testing.T, v ProfileView) {
				if got := pluginWho(v); !reflect.DeepEqual(got, map[string]string{"foo@2": "profile*"}) {
					t.Errorf("plugins = %v", got)
				}
			},
		},
		{
			name: "identical plugin re-declared is no override",
			files: map[string]string{
				"ocp.yaml":                "version: 1\nplugins: [foo]\n",
				"profiles/p/profile.yaml": "plugins: [foo]\n",
			},
			check: func(t *testing.T, v ProfileView) {
				if got := pluginWho(v); !reflect.DeepEqual(got, map[string]string{"foo": "global"}) {
					t.Errorf("plugins = %v", got)
				}
			},
		},
		{
			name: "model set only by the profile is not an override",
			files: map[string]string{
				"ocp.yaml":                "version: 1\n",
				"profiles/p/profile.yaml": "config:\n  model: own/model\n",
			},
			check: func(t *testing.T, v ProfileView) {
				if got := configWho(v, "")["model"]; got != "profile" {
					t.Errorf("model = %q, want profile", got)
				}
			},
		},
		{
			name: "same model declared again is neither an override nor a new origin",
			files: map[string]string{
				"ocp.yaml":                "version: 1\nconfig:\n  model: same/model\n",
				"profiles/p/profile.yaml": "config:\n  model: same/model\n",
			},
			check: func(t *testing.T, v ProfileView) {
				if got := configWho(v, "")["model"]; got != "global" {
					t.Errorf("model = %q, want global", got)
				}
			},
		},
		{
			name: "changing one field of an inherited server overrides it; adding a field does not",
			files: map[string]string{
				"ocp.yaml":                "version: 1\nconfig:\n  mcp:\n    servers:\n      a: {type: remote, url: one}\n      b: {type: remote, url: one}\n",
				"profiles/p/profile.yaml": "config:\n  mcp:\n    servers:\n      a: {url: two}\n      b: {timeout: 5}\n",
			},
			check: func(t *testing.T, v ProfileView) {
				got := configWho(v, "")
				if got["mcp.servers.a"] != "profile*" || got["mcp.servers.b"] != "global" {
					t.Errorf("servers = %v, want a=profile* b=global", got)
				}
			},
		},
		{
			name: "machine overlay replacing a profile value is an override at that machine",
			files: map[string]string{
				"ocp.yaml":                "version: 1\n",
				"profiles/p/profile.yaml": "config:\n  model: own/model\nmachines:\n  m:\n    config:\n      model: m/model\n",
			},
			check: func(t *testing.T, v ProfileView) {
				if got := configWho(v, "")["model"]; got != "profile" {
					t.Errorf("base model = %q", got)
				}
				if got := configWho(v, "m")["model"]; got != "@m*" {
					t.Errorf("machine model = %q, want @m*", got)
				}
			},
		},
		{
			name: "machine overlay adding a new server is not an override",
			files: map[string]string{
				"ocp.yaml":                "version: 1\nmachines:\n  m:\n    config:\n      mcp:\n        servers:\n          x: {type: remote}\n",
				"profiles/p/profile.yaml": "{}\n",
			},
			check: func(t *testing.T, v ProfileView) {
				if got := configWho(v, "m")["mcp.servers.x"]; got != "@m" {
					t.Errorf("server = %q, want @m", got)
				}
			},
		},
		{
			name: "global overlay re-applied at the child level beats the child",
			files: map[string]string{
				"ocp.yaml":                   "version: 1\nskills: [github:o/r@v1]\nmachines:\n  m:\n    skills: [github:o/r@v2]\n",
				"profiles/base/profile.yaml": "{}\n",
				"profiles/p/profile.yaml":    "extends: base\nskills: [github:o/r@v1]\n",
			},
			check: func(t *testing.T, v ProfileView) {
				got := skillWho(v)
				if got["r"] != "global" || got["r@m"] != "@m*" {
					t.Errorf("skills = %v, want r=global and r@m=@m*", got)
				}
			},
		},
		{
			name: "instruction declared twice keeps the first origin",
			files: map[string]string{
				"ocp.yaml":                "version: 1\ninstructions: [shared]\n",
				"profiles/p/profile.yaml": "instructions: [shared, own]\n",
			},
			check: func(t *testing.T, v ProfileView) {
				got := map[string]string{}
				for _, in := range v.Instructions {
					got[in.Name] = who(in.Attribution)
				}
				if want := map[string]string{"shared": "global", "own": "profile"}; !reflect.DeepEqual(got, want) {
					t.Errorf("instructions = %v, want %v", got, want)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSource(t, tc.files)
			tc.check(t, profileView(t, explain(t, dir), "p"))
		})
	}
}

func wantSkill(t *testing.T, v ProfileView, name, ref, want string) {
	t.Helper()
	for _, s := range v.Skills {
		if s.Skill.Name() == name && s.Machine == "" {
			if s.Skill.Ref != ref || who(s.Attribution) != want {
				t.Errorf("skill %s = ref %q %q, want ref %q %q", name, s.Skill.Ref, who(s.Attribution), ref, want)
			}
			return
		}
	}
	t.Errorf("skill %s missing from %+v", name, v.Skills)
}

func TestExplainCoversEveryResolvedProfile(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                    explainRoot,
		"profiles/base/profile.yaml":  "plugins: [one.ts]\nskills: [s1]\n",
		"profiles/child/profile.yaml": "extends: base\nplugins: [two]\nskills: [s2, s1]\n",
		"profiles/gated/profile.yaml": "hosts: [elsewhere]\n",
	})
	doc, err := Load(filepath.Join(dir, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := doc.ResolveFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := doc.ExplainFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Profiles) != len(resolved) {
		t.Fatalf("%d views for %d profiles", len(ex.Profiles), len(resolved))
	}
	for i, v := range ex.Profiles {
		if !reflect.DeepEqual(v.Profile, resolved[i]) {
			t.Errorf("view %s differs from the machine-independent resolution", v.Profile.Name)
		}
	}
	if profileView(t, ex, "gated").Hosts[0] != "elsewhere" {
		t.Error("host-limited profile must be listed")
	}
}

func TestExplainImplicitDefaultProfile(t *testing.T) {
	dir := writeSource(t, map[string]string{"ocp.yaml": explainRoot})
	v := profileView(t, explain(t, dir), "default")
	for _, s := range v.Skills {
		if s.Origin != "global" {
			t.Errorf("%s origin = %q, want global", s.Skill.Name(), s.Origin)
		}
	}
}

func TestSkillName(t *testing.T) {
	for _, tc := range []struct {
		skill Skill
		want  string
	}{
		{Skill{Source: "https://github.com/a/skills", Path: "skills/pdf"}, "pdf"},
		{Skill{Source: "https://github.com/a/stop-slop"}, "stop-slop"},
		{Skill{Source: "git@host:team/tools.git"}, "tools"},
		{Skill{Source: "apple/staging-exposure"}, "staging-exposure"},
		{Skill{Source: "git-work-summary"}, "git-work-summary"},
	} {
		if got := tc.skill.Name(); got != tc.want {
			t.Errorf("%+v Name() = %q, want %q", tc.skill, got, tc.want)
		}
	}
}

func TestSplitRefTreatsUserinfoAsPartOfTheURL(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://github.com/o/r@v1":              {"https://github.com/o/r", "v1"},
		"https://github.com/o/r":                 {"https://github.com/o/r", ""},
		"https://user:tok@github.com/o/r":        {"https://user:tok@github.com/o/r", ""},
		"https://user:tok@github.com/o/r@v2":     {"https://user:tok@github.com/o/r", "v2"},
		"https://x-access-token:ghp_SECRET@host": {"https://x-access-token:ghp_SECRET@host", ""},
		"git@github.com:o/r.git":                 {"git@github.com:o/r.git", ""},
	} {
		if src, ref := splitRef(in); src != want[0] || ref != want[1] {
			t.Errorf("splitRef(%q) = %q, %q; want %q, %q", in, src, ref, want[0], want[1])
		}
	}
}

func TestExplainPluginListsAreAttributedPerItem(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                   "version: 1\nconfig:\n  plugins: [a]\nmachines:\n  m:\n    config:\n      plugins: [a, b-from-m]\n",
		"profiles/base/profile.yaml": "{}\n",
		"profiles/p/profile.yaml":    "extends: base\nconfig:\n  plugins: [a, own]\n",
	})
	v := profileView(t, explain(t, dir), "p")
	got := map[string]string{}
	for _, pl := range v.Plugins {
		key := pl.Plugin
		if pl.Machine != "" {
			key += "@" + pl.Machine
		}
		got[key] = who(pl.Attribution)
	}
	// "a" is global and only re-declared; "own" is new on the profile; the
	// overlay's "a" is a repeat and adds nothing; its list also replaces the
	// profile's, so only b-from-m is new on that machine.
	want := map[string]string{"a": "global", "own": "profile", "b-from-m@m": "@m"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plugins = %v, want %v", got, want)
	}
}

func TestExplainLocalPluginSpellingsAreTheSameFile(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                "version: 1\nplugins: [x.ts]\n",
		"profiles/p/profile.yaml": "plugins: [./plugins/x.ts]\n",
	})
	// The resolved list keeps the child's spelling; the origin stays global.
	if got := pluginWho(profileView(t, explain(t, dir), "p")); !reflect.DeepEqual(got, map[string]string{"./plugins/x.ts": "global"}) {
		t.Errorf("plugins = %v: another spelling of the same file is not an override", got)
	}
}

func TestExplainDuplicatesInOneLayerAreNotOverrides(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                "version: 1\n",
		"profiles/p/profile.yaml": "skills: [github:o/r@v1, github:o/r@v2]\n",
	})
	v := profileView(t, explain(t, dir), "p")
	for _, s := range v.Skills {
		if s.Override {
			t.Errorf("%s is not overriding anything: no lower layer defines it", s.Skill.Name())
		}
	}
}

func TestExplainAgentsCompareMergedValues(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml": "version: 1\nagents:\n  same:\n    file: ./a.md\n    model: x/y\n  tweak:\n    file: ./b.md\n    model: x/y\n  change:\n    file: ./c.md\n    model: x/y\nmachines:\n  m:\n    agents:\n      only-on-m:\n        file: ./d.md\n",
		"a.md":     "a", "b.md": "b", "c.md": "c", "d.md": "d",
		"profiles/p/profile.yaml": "agents:\n  same:\n    model: x/y\n  tweak:\n    config:\n      temperature: 0.1\n  change:\n    model: other/model\n",
	})
	v := profileView(t, explain(t, dir), "p")
	got := map[string]string{}
	for _, a := range v.Agents {
		got[a.Name+"@"+a.Machine] = who(a.Attribution)
	}
	want := map[string]string{
		"same@":   "global",   // the same value again changes nothing
		"tweak@":  "global",   // adding a setting replaces nothing
		"change@": "profile*", // a different model replaces the inherited one
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("agent %s = %q, want %q (all: %v)", k, got[k], w, got)
		}
	}
	if got["only-on-m@m"] != "@m" {
		t.Errorf("the overlay's agent should be attributed to the machine: %v", got)
	}
}

func TestExplainShowsInstructionsFromMachineOverlays(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                "version: 1\nmachines:\n  windy:\n    instructions: [hosts/windy-extra.md]\n",
		"hosts/windy-extra.md":    "x",
		"profiles/p/profile.yaml": "{}\n",
	})
	v := profileView(t, explain(t, dir), "p")
	if len(v.Instructions) != 1 || who(v.Instructions[0].Attribution) != "@windy" {
		t.Errorf("instructions = %+v, want the windy overlay's instruction", v.Instructions)
	}
}

func TestExplainDoesNotDuplicateEntriesAnOverlayAlsoSuppliesToTheBase(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                "version: 1\nmachines:\n  windy:\n    skills: [x]\n",
		"profiles/a/profile.yaml": "{}\n",
		"profiles/b/profile.yaml": "extends: a\nskills: [x]\n",
	})
	v := profileView(t, explain(t, dir), "b")
	count := 0
	for _, s := range v.Skills {
		if s.Skill.Name() == "x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("x listed %d times, want once: %+v", count, v.Skills)
	}
}

func TestExplainForgetsPathsUnderAReplacedMap(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                "version: 1\nconfig:\n  provider:\n    openai: {name: x}\n",
		"profiles/p/profile.yaml": "config:\n  provider: null\n",
	})
	v := profileView(t, explain(t, dir), "p")
	for _, c := range v.Config {
		if c.Path == "provider.openai" {
			t.Errorf("provider.openai no longer exists in the resolved profile: %+v", v.Config)
		}
	}
}

func TestExplainSupportsBothMCPShapes(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                "version: 1\nconfig:\n  mcp:\n    timeout: {startup: 5}\n    servers:\n      current: {type: remote}\n    legacy: {type: local, command: [x]}\n",
		"profiles/p/profile.yaml": "{}\n",
	})
	got := configWho(profileView(t, explain(t, dir), "p"), "")
	for _, path := range []string{"mcp.servers.current", "mcp.legacy", "mcp.timeout"} {
		if got[path] != "global" {
			t.Errorf("%s = %q, want global (all: %v)", path, got[path], got)
		}
	}
}

func TestExplainWarnsInsteadOfFailingWhenOneMachineDoesNotResolve(t *testing.T) {
	dir := writeSource(t, map[string]string{
		"ocp.yaml":                   "version: 1\nmachines:\n  windy:\n    skills: [w]\n",
		"profiles/base/profile.yaml": "hosts: [windy]\n",
		"profiles/lab/profile.yaml":  "extends: base\nhosts: [cosmonaut]\n",
		"profiles/free/profile.yaml": "{}\n",
	})
	ex := explain(t, dir)
	if len(ex.Warnings) == 0 {
		t.Fatal("expected a warning for the machine that does not resolve")
	}
	if len(ex.Profiles) != 3 {
		t.Errorf("every profile is still listed: %d", len(ex.Profiles))
	}
}
