package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/color"
	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/ocp"
)

// showSetup writes a source tree and the local state that points at it,
// without running setup (so Git skills need no network).
func showSetup(t *testing.T, machine string, files map[string]string) (ocp.Paths, *Runner, *bytes.Buffer) {
	t.Helper()
	t.Setenv("COLUMNS", "")
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	for name, text := range files {
		write(t, filepath.Join(source, name), text)
	}
	state := `{"version":1,"source":` + jsonString(source) + `,"auto_commit":false,"machine":` + jsonString(machine) + `}`
	write(t, p.StateFile, state)
	var out bytes.Buffer
	return p, runner(p, &out), &out
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var showFiles = map[string]string{
	"ocp.yaml": `version: 1
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
  - github:owner/skills//skills/shared
`,
	"profiles/default/profile.yaml": "config:\n  model: vendor/default\n  plugins:\n    - local-plugin.ts\n    - [\"tuple-plugin\", {a: 1}]\n",
	"profiles/router/profile.yaml": `extends: default
config:
  model: vendor/router
plugins:
  - npm-plugin
skills:
  - own-skill
  - github:owner/skills//skills/shared@v2
`,
	"profiles/lab/profile.yaml":                   "hosts: [cosmonaut]\nconfig:\n  model: vendor/lab\n",
	"skills/own-skill/SKILL.md":                   "---\nname: own-skill\n---\n",
	"skills/unused-skill/SKILL.md":                "---\nname: unused-skill\n---\n",
	"plugins/local-plugin.ts":                     "export default {}\n",
	"plugins/unused-plugin.ts":                    "export default {}\n",
	"profiles/router/files/router.overrides.json": "{}\n",
}

func asciiOnly(t *testing.T, s string) {
	t.Helper()
	for _, b := range []byte(s) {
		if b >= 0x80 || (b < 0x20 && b != '\n') {
			t.Fatalf("plain output must be ASCII without escapes, found byte %#x:\n%s", b, s)
		}
	}
}

func TestShowOverviewPlainListsOnlyWhatEachProfileAdds(t *testing.T) {
	_, r, out := showSetup(t, "windy", showFiles)
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	asciiOnly(t, got)
	// Tree: default has router nested beneath it; lab is separate and gated.
	for _, want := range []string{
		"Global\n",
		"skills     shared\n",
		"machines   cosmonaut, windy\n",
		"-- default\n",
		"-- lab (cosmonaut only)\n",
		"-- router  ~4 tokens\n",
		"model     vendor/router *\n",
		"skills    shared*  own-skill\n",
		"plugins   npm-plugin\n",
		"files     router.overrides.json\n",
		"! 2 unreferenced local files: plugins/unused-plugin.ts, skills/unused-skill\n",
		"* overrides an inherited value\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("overview missing %q:\n%s", want, got)
		}
	}
	// Nothing inherited from Global is repeated under a profile.
	if strings.Count(got, "frontend") != 0 || strings.Count(got, "skills     shared") != 1 {
		t.Errorf("global skills must appear once:\n%s", got)
	}
	// The word ROOT and the file name are gone.
	for _, gone := range []string{"ROOT", "ocp.yaml", "extends"} {
		if strings.Contains(got, gone) {
			t.Errorf("overview should not contain %q:\n%s", gone, got)
		}
	}
}

func TestShowDetailShowsOriginsMachinesAndOverrides(t *testing.T) {
	_, r, out := showSetup(t, "windy", showFiles)
	if err := r.Run([]string{"show", "router"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	asciiOnly(t, got)
	for _, want := range []string{
		"router\nextends   default\n",
		"model     vendor/router *\n",
		"\nskills\n",
		"shared     owner/skills@v2  *\n", // replaces the global declaration: one row, marked
		"own-skill  local\n",              // own and new: no mark
		"\nplugins\n",
		"npm-plugin       npm\n",
		"local-plugin.ts  local  extends default\n",
		"tuple-plugin     npm    extends default\n",
		"\nmcp\n",
		"mobbin      remote  windy\n",
		"playwright  stdio   cosmonaut\n",
		"\nfiles\n  router.overrides.json\n",
		"* overrides an inherited value\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q:\n%s", want, got)
		}
	}
	// A section title is always preceded by a blank line.
	for _, title := range []string{"skills", "plugins", "mcp", "files"} {
		if !strings.Contains(got, "\n\n"+title+"\n") {
			t.Errorf("section %q must follow a blank line:\n%s", title, got)
		}
	}
	// figma exists on both machines: one row per machine, neither marked.
	if strings.Count(got, "figma       remote  windy\n") != 1 || strings.Count(got, "figma       stdio   cosmonaut\n") != 1 {
		t.Errorf("figma should appear once per machine, unmarked:\n%s", got)
	}
	// The current machine's rows come before the other machine's.
	if strings.Index(got, "mobbin") > strings.Index(got, "playwright") {
		t.Errorf("current machine (windy) must be listed first:\n%s", got)
	}
}

func TestShowHostLimitedProfile(t *testing.T) {
	_, r, out := showSetup(t, "windy", showFiles)
	if err := r.Run([]string{"show", "lab"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "hosts     cosmonaut\n") {
		t.Errorf("detail should show hosts:\n%s", got)
	}
	// windy's overlay can never apply to a cosmonaut-only profile.
	if strings.Contains(got, "mobbin") || !strings.Contains(got, "playwright") {
		t.Errorf("only cosmonaut's servers apply to lab:\n%s", got)
	}
	// Host-limited profiles are listed even when you are on another machine.
	out.Reset()
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "lab (cosmonaut only)") {
		t.Errorf("host-limited profile must be listed:\n%s", out.String())
	}
}

func TestShowStyledOutputUsesAgreedStyles(t *testing.T) {
	color.Force()
	t.Cleanup(color.Reset)
	_, r, out := showSetup(t, "windy", showFiles)
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"\x1b[1mGlobal\x1b[0m",
		"\x1b[2m├── \x1b[0m",
		"\x1b[1;35mwindy\x1b[0m",   // current machine: bold purple
		"\x1b[35mcosmonaut\x1b[0m", // other machines: purple
		"\x1b[33m*\x1b[0m",         // override mark
		"\x1b[2m(\x1b[0m",          // dim host tag
		"\x1b[33m!\x1b[0m",         // warning
	} {
		if !strings.Contains(got, want) {
			t.Errorf("styled overview missing %q:\n%q", want, got)
		}
	}
	out.Reset()
	if err := r.Run([]string{"show", "router"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\x1b[1;2mskills\x1b[0m", // section header: bold dim
		"\x1b[34mextends default\x1b[0m",
		"\x1b[33mlocal",  // local: yellow
		"\x1b[31mnpm",    // npm: red
		"\x1b[32mremote", // remote: green
		"\x1b[1;35mwindy\x1b[0m",
		"\x1b[35mcosmonaut\x1b[0m",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("styled detail missing %q:\n%q", want, out.String())
		}
	}
	if strings.Contains(out.String(), "profile") {
		t.Errorf("own entries must carry no origin mark:\n%q", out.String())
	}
	out.Reset()
	if err := r.Run([]string{"show", "lab"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[36mglobal\x1b[0m") { // inherited from Global: cyan
		t.Errorf("global origin should be cyan:\n%q", out.String())
	}
}

func TestShowFitsListsToTerminalWidth(t *testing.T) {
	var y strings.Builder
	y.WriteString("version: 1\n")
	_, r, out := showSetup(t, "", map[string]string{
		"ocp.yaml": y.String(),
		"profiles/p/profile.yaml": "skills:\n" + strings.Repeat("", 0) +
			"  - github:o/r//skills/alpha-skill\n  - github:o/r//skills/beta-skill\n  - github:o/r//skills/gamma-skill\n" +
			"  - github:o/r//skills/delta-skill\n  - github:o/r//skills/epsilon-skill\n",
	})
	t.Setenv("COLUMNS", "52")
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, line := range strings.Split(got, "\n") {
		if n := len([]rune(line)); n > 52 {
			t.Errorf("line is %d columns, want at most 52: %q", n, line)
		}
	}
	if !strings.Contains(got, "alpha-skill") || !strings.Contains(got, " others") {
		t.Errorf("should show the first names and a count of the rest:\n%s", got)
	}
	if strings.Contains(got, "(o/r)") || strings.Contains(got, "o/r") {
		t.Errorf("hidden skills must not carry extra detail:\n%s", got)
	}
	t.Setenv("COLUMNS", "200")
	out.Reset()
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "others") || !strings.Contains(out.String(), "epsilon-skill") {
		t.Errorf("a wide terminal shows everything:\n%s", out.String())
	}
}

func TestShowCollapsesLongSkillListsUnlessAll(t *testing.T) {
	var skills strings.Builder
	skills.WriteString("version: 1\nskills:\n")
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		skills.WriteString("  - github:owner/big//skills/" + n + "\n")
	}
	skills.WriteString("  - github:other/one//skills/solo\n")
	_, r, out := showSetup(t, "", map[string]string{"ocp.yaml": skills.String()})
	if err := r.Run([]string{"show", "default"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "10 skills  owner/big  global") || !strings.Contains(got, "1 skill    other/one  global") {
		t.Errorf("long list not collapsed by repository:\n%s", got)
	}
	if !strings.Contains(got, "--all lists every skill") {
		t.Errorf("collapsed output needs the --all footnote:\n%s", got)
	}
	out.Reset()
	if err := r.Run([]string{"show", "default", "--all"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  j     owner/big  global") || !strings.Contains(out.String(), "solo") {
		t.Errorf("--all should list every skill:\n%s", out.String())
	}
	if strings.Contains(out.String(), "--all lists") {
		t.Errorf("no footnote when nothing is grouped:\n%s", out.String())
	}
}

func TestShowJSONShape(t *testing.T) {
	_, r, out := showSetup(t, "windy", showFiles)
	if err := r.Run([]string{"show", "--json"}); err != nil {
		t.Fatal(err)
	}
	var doc showDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Current != "windy" || doc.Global == nil || len(doc.Profiles) != 3 {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if len(doc.Machines) != 2 || doc.Machines[0] != "cosmonaut" {
		t.Errorf("machines = %v", doc.Machines)
	}
	if got := doc.Global.Skills[0]; got.Name != "shared" || got.Kind != "git" || got.Source != "owner/skills" || got.Origin != "global" {
		t.Errorf("global skill = %+v", got)
	}
	var router showProfile
	for _, p := range doc.Profiles {
		if p.Name == "router" {
			router = p
		}
	}
	var sawOverride, sawMachine bool
	for _, s := range router.Skills {
		sawOverride = sawOverride || (s.Name == "shared" && s.Override && s.Ref == "v2")
	}
	for _, m := range router.MCP {
		sawMachine = sawMachine || (m.Machine == "cosmonaut" && m.Name == "playwright")
	}
	if !sawOverride || !sawMachine {
		t.Errorf("router JSON lacks override or machine entry: %+v", router)
	}
	if strings.Contains(out.String(), "example.test") {
		t.Errorf("MCP settings must not be printed:\n%s", out.String())
	}
}

func TestShowErrors(t *testing.T) {
	_, r, out := showSetup(t, "windy", showFiles)
	err := r.Run([]string{"show", "nope"})
	if err == nil || !strings.Contains(err.Error(), `unknown profile "nope"; available: default, lab, router`) {
		t.Fatalf("unknown profile error = %v", err)
	}
	if err := r.Run([]string{"show", "a", "b"}); err == nil {
		t.Fatal("expected usage error for two profiles")
	}
	if err := r.Run([]string{"show", "--machine", "x"}); err == nil {
		t.Fatal("--machine no longer exists: show describes the whole configuration")
	}
	out.Reset()
	p := testPaths(t)
	if err := runner(p, out).Run([]string{"show"}); err == nil || !strings.Contains(err.Error(), "ocp setup") {
		t.Fatalf("not-set-up error = %v", err)
	}
}

func TestShowFlagOrder(t *testing.T) {
	_, r, out := showSetup(t, "windy", showFiles)
	for _, args := range [][]string{{"show", "--json", "router"}, {"show", "router", "--json"}} {
		out.Reset()
		if err := r.Run(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var doc showDoc
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Profiles) != 1 || doc.Profiles[0].Name != "router" {
			t.Fatalf("%v: %v %+v", args, err, doc)
		}
	}
}

func TestShowDoesNotTakeTheLockOrWrite(t *testing.T) {
	p, r, _ := showSetup(t, "windy", showFiles)
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.LockFile); err == nil {
		t.Error("show must not create the advisory lock file")
	}
	if _, err := os.Stat(p.Releases); err == nil {
		t.Error("show must not render releases")
	}
}

func TestShowCleanConfigHasNoWarnings(t *testing.T) {
	_, r, out := showSetup(t, "", map[string]string{
		"ocp.yaml":                "version: 1\nskills: [kept]\n",
		"skills/kept/SKILL.md":    "x",
		"profiles/p/profile.yaml": "{}\n",
	})
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "!") || strings.Contains(out.String(), "unreferenced") {
		t.Errorf("a clean config prints no warnings:\n%s", out.String())
	}
}

func TestUnreferencedLocalFiles(t *testing.T) {
	source := t.TempDir()
	for name, text := range map[string]string{
		"skills/used/SKILL.md":              "x",
		"skills/apple/nested-used/SKILL.md": "x",
		"skills/apple/nested-free/SKILL.md": "x",
		"skills/abs-used/SKILL.md":          "x",
		"skills/.hidden/SKILL.md":           "x",
		"skills/real-linked/SKILL.md":       "x",
		"plugins/used.ts":                   "x",
		"plugins/dir-plugin/index.ts":       "x",
		"plugins/pkg-plugin/package.json":   "{}",
		"plugins/free.ts":                   "x",
		"plugins/package.json":              "{}",
		"plugins/tsconfig.json":             "{}",
		"plugins/node_modules/dep/index.js": "x",
		"plugins/notes/readme.md":           "x",
		"plugins/.DS_Store":                 "x",
	} {
		write(t, filepath.Join(source, name), text)
	}
	if err := os.Symlink(filepath.Join(source, "skills", "real-linked"), filepath.Join(source, "skills", "linked-free")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	t.Setenv("HOME", filepath.Dir(source))
	ex := config.Explanation{Profiles: []config.ProfileView{{
		Skills: []config.SkillView{
			{Skill: config.Skill{Source: "used"}},
			{Skill: config.Skill{Source: "./skills/apple/nested-used"}},
			{Skill: config.Skill{Source: filepath.Join(source, "skills", "abs-used")}},
			{Skill: config.Skill{Source: "https://github.com/o/r", Path: "skills/x"}},
		},
		// The second profile only mentions a skill inside a machine overlay.
		Plugins: []config.PluginView{{Plugin: "used.ts"}, {Plugin: "npm-pkg"}},
	}, {
		Skills: []config.SkillView{{Skill: config.Skill{Source: "real-linked"}, Attribution: config.Attribution{Machine: "windy"}}},
	}}}
	got := unreferencedLocal(source, ex)
	want := []string{"plugins/dir-plugin", "plugins/free.ts", "plugins/pkg-plugin", "skills/apple/nested-free", "skills/linked-free"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unreferenced = %v, want %v", got, want)
	}
}

func TestRepoLabelAndPluginKinds(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/anthropics/skills":                     "anthropics/skills",
		"https://github.com/o/r.git":                               "o/r",
		"https://gitlab.com/team/skills":                           "gitlab.com/team/skills",
		"git@myserver:team/skills.git":                             "myserver/team/skills",
		"ssh://git@myserver.example/team/skills":                   "myserver.example/team/skills",
		"https://x-access-token:ghp_secret@github.com/org/private": "org/private",
		"https://user:pw@gitlab.example:8443/team/r.git":           "gitlab.example:8443/team/r",
		"user:pw@host:team/skills.git":                             "host/team/skills",
		"https://host.example/o/r?token=SECRET#frag":               "host.example/o/r",
	} {
		if got := repoLabel(in); got != want {
			t.Errorf("repoLabel(%q) = %q, want %q", in, got, want)
		}
	}
	attr := config.Attribution{Origin: "profile"}
	if got := pluginEntry("git+https://user:tok@github.com/o/plugin", attr, "/src").Name; strings.Contains(got, "tok") || strings.Contains(got, "user") {
		t.Errorf("plugin name leaks credentials: %q", got)
	}
	for plugin, want := range map[string]string{
		"opencode-model-router@0.3.0": "opencode-model-router@0.3.0",
		"@scope/pkg":                  "@scope/pkg",
		"@scope/pkg@1.2.3":            "@scope/pkg@1.2.3",
		"pkg@1":                       "pkg@1",
	} {
		if got := pluginEntry(plugin, attr, "/src").Name; got != want {
			t.Errorf("pluginEntry(%q).Name = %q, want %q: versions and scopes are not credentials", plugin, got, want)
		}
	}
	if got := clean("a\x1b[31mb\x07"); strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("clean left control characters: %q", got)
	}
	t.Setenv("HOME", "/home/u")
	for plugin, want := range map[string]string{
		"harness-bridge.ts":                   "local",
		"./plugins/x.js":                      "local",
		"opencode-model-router":               "npm",
		"@scope/pkg":                          "npm",
		"{env:HOME}/src/ocp/plugins/decision": "local",
		"{env:HOME}/elsewhere/plugin":         "path",
	} {
		if got := pluginEntry(plugin, attr, "/home/u/src/ocp").Kind; got != want {
			t.Errorf("pluginEntry(%q).Kind = %q, want %q", plugin, got, want)
		}
	}
}

func TestShowNeverPrintsCredentialsFromScalarSkillURLs(t *testing.T) {
	_, r, out := showSetup(t, "", map[string]string{
		"ocp.yaml": "version: 1\nskills:\n  - https://x-access-token:ghp_SECRET@github.com/org/private\n  - https://x-access-token:ghp_SECRET@github.com/org/pinned@v2\n",
	})
	for _, args := range [][]string{{"show"}, {"show", "default"}, {"show", "--json"}} {
		out.Reset()
		if err := r.Run(args); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "ghp_SECRET") || strings.Contains(out.String(), "x-access-token") {
			t.Errorf("%v leaks credentials:\n%s", args, out.String())
		}
	}
	out.Reset()
	if err := r.Run([]string{"show", "default"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "private  org/private") || !strings.Contains(out.String(), "pinned   org/pinned@v2") {
		t.Errorf("names and sources should parse correctly:\n%s", out.String())
	}
}

func TestShowFitAlwaysShowsAtLeastOneName(t *testing.T) {
	_, r, out := showSetup(t, "", map[string]string{
		"ocp.yaml":                "version: 1\n",
		"profiles/p/profile.yaml": "skills: [a-rather-long-skill-name-that-cannot-fit-anywhere]\n",
	})
	t.Setenv("COLUMNS", "30")
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "a-rather-long-skill-name-that-cannot-fit-anywhere") {
		t.Errorf("the only skill must still be shown:\n%s", out.String())
	}
}

func TestShowActiveLabel(t *testing.T) {
	p, r, out := showSetup(t, "", map[string]string{
		"ocp.yaml":                "version: 1\n",
		"profiles/p/profile.yaml": "{}\n",
	})
	// The active profile is the OpenCode config link pointing into Current/<name>.
	if err := os.MkdirAll(filepath.Join(p.Current, "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.OpenCode), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(p.Current, "p"), p.OpenCode); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if active, _ := ocp.ActiveProfile(p); active != "p" {
		t.Fatalf("test setup: active profile = %q, want p", active)
	}
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "p active\n") || strings.Contains(out.String(), "●") {
		t.Errorf("plain output marks the active profile with the word only:\n%s", out.String())
	}
	color.Force()
	t.Cleanup(color.Reset)
	out.Reset()
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[1;32m● active\x1b[0m") {
		t.Errorf("styled output uses the green dot:\n%q", out.String())
	}
}

func TestShowSanitizesControlCharactersInEveryField(t *testing.T) {
	_, r, out := showSetup(t, "", map[string]string{
		"ocp.yaml":                       "version: 1\nconfig:\n  model: \"m\\e[31model\"\n  mcp:\n    servers:\n      \"srv\\e[2J\": {type: remote}\n  providers:\n    \"prov\\u0007\": {}\n",
		"profiles/p/profile.yaml":        "{}\n",
		"profiles/p/files/f\x1bile.json": "{}",
	})
	for _, args := range [][]string{{"show", "p"}, {"show", "--json"}} {
		out.Reset()
		if err := r.Run(args); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(out.String(), "\x1b\x07") {
			t.Errorf("%v printed a control character:\n%q", args, out.String())
		}
	}
}

func TestShowSupportsLegacyMCPShapeAndOverlayInstructions(t *testing.T) {
	_, r, out := showSetup(t, "windy", map[string]string{
		"ocp.yaml":                "version: 1\nconfig:\n  mcp:\n    timeout: {startup: 5}\n    legacy: {type: local, command: [x]}\nmachines:\n  windy:\n    instructions: [hosts/windy-extra.md]\n",
		"hosts/windy-extra.md":    "x",
		"profiles/p/profile.yaml": "{}\n",
	})
	if err := r.Run([]string{"show", "p"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "legacy  stdio  global") || strings.Contains(got, "timeout") {
		t.Errorf("legacy mcp.<name> servers are listed, settings are not:\n%s", got)
	}
	if !strings.Contains(got, "hosts/windy-extra.md  windy") {
		t.Errorf("overlay instructions must be listed with their machine:\n%s", got)
	}
}

func TestShowOverviewIgnoresMachineOverlayValues(t *testing.T) {
	_, r, out := showSetup(t, "windy", map[string]string{
		"ocp.yaml":                "version: 1\nmachines:\n  windy:\n    config:\n      model: windy/model\n    skills: [machine-only]\n",
		"profiles/p/profile.yaml": "{}\n",
	})
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "windy/model") || strings.Contains(out.String(), "machine-only") {
		t.Errorf("overlay values belong to the detail view, not the tree:\n%s", out.String())
	}
	out.Reset()
	if err := r.Run([]string{"show", "p"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "windy/model  windy") || !strings.Contains(out.String(), "machine-only  local  windy") {
		t.Errorf("detail lists the overlay's model and skill:\n%s", out.String())
	}
}

func TestShowReportsMachinesThatDoNotResolveAsWarnings(t *testing.T) {
	_, r, out := showSetup(t, "windy", map[string]string{
		"ocp.yaml":                   "version: 1\n",
		"profiles/base/profile.yaml": "hosts: [windy]\n",
		"profiles/lab/profile.yaml":  "extends: base\nhosts: [cosmonaut]\n",
	})
	if err := r.Run([]string{"show"}); err != nil {
		t.Fatalf("one broken machine must not fail the command: %v", err)
	}
	if !strings.Contains(out.String(), "! machine cosmonaut does not resolve") {
		t.Errorf("expected a warning:\n%s", out.String())
	}
}

func TestShowContextEstimate(t *testing.T) {
	files := map[string]string{
		"ocp.yaml":            "version: 1\ninstructions: [AGENTS.md]\nskills: [big]\nconfig:\n  mcp:\n    servers:\n      pw: {type: local, command: [npx, pw]}\n",
		"AGENTS.md":           strings.Repeat("a", 4000),
		"skills/big/SKILL.md": "---\nname: big\ndescription: " + strings.Repeat("d", 400) + "\n---\n" + strings.Repeat("b", 8000),
	}
	_, r, out := showSetup(t, "windy", files)
	if err := r.Run([]string{"show", "default"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"context  ~1.1k tokens before the first prompt (estimate)\n",
		"  instructions   1  1k\n",
		"  skills         1  102\n",
		"  skill bodies ~2k load when used, 1 MCP server not measured\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	out.Reset()
	if err := r.Run([]string{"show", "--json", "default"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"tokens": 1102`) || !strings.Contains(out.String(), `"mcp_servers": 1`) {
		t.Errorf("json missing context:\n%s", out.String())
	}
}
