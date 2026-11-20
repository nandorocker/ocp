package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nando/ocp/internal/color"
	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/ocp"
	"github.com/nando/ocp/internal/skills"
)

const (
	// collapseAt is the number of skills above which the detail view groups
	// skills by repository unless --all is given.
	collapseAt = 8
	// markOverride flags an entry that replaces a different inherited value.
	markOverride = "*"
)

type showOptions struct {
	json bool
	all  bool
}

func configureShow(f *flag.FlagSet, o *showOptions) {
	f.BoolVar(&o.json, "json", false, "print machine-readable JSON")
	f.BoolVar(&o.all, "all", false, "list every skill instead of grouping by repository")
}

// showEntry is one resolved entry of a section. Origin is "global", "profile"
// or "extends <profile>"; entries that come from a machine overlay have a
// Machine instead. Override means the entry replaces a different value that a
// lower layer defined.
type showEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`   // git, local, npm, path, remote, stdio
	Source   string `json:"source,omitempty"` // repository for Git skills
	Ref      string `json:"ref,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Machine  string `json:"machine,omitempty"`
	Override bool   `json:"override,omitempty"`
}

// label is the source column: the repository for Git skills (git is the
// default and stays unlabeled), otherwise the kind.
func (e showEntry) label() string {
	if e.Kind == "git" {
		if e.Ref != "" {
			return e.Source + "@" + e.Ref
		}
		return e.Source
	}
	return e.Kind
}

// own reports whether the profile itself declares the entry.
func (e showEntry) own() bool { return e.Machine == "" && e.Origin == config.OriginProfile }

type showProfile struct {
	Name         string      `json:"name"`
	Active       bool        `json:"active"`
	Extends      string      `json:"extends,omitempty"`
	Hosts        []string    `json:"hosts,omitempty"`
	Models       []showEntry `json:"models,omitempty"`
	Skills       []showEntry `json:"skills,omitempty"`
	Plugins      []showEntry `json:"plugins,omitempty"`
	MCP          []showEntry `json:"mcp,omitempty"`
	Providers    []showEntry `json:"providers,omitempty"`
	Agents       []showEntry `json:"agents,omitempty"`
	Instructions []showEntry `json:"instructions,omitempty"`
	Files        []showEntry `json:"files,omitempty"`
	// Disabled names skills and plugins switched off with `disabled:`.
	Disabled []string `json:"disabled,omitempty"`
	// Context is an estimate for this machine; host-limited profiles that
	// do not render here have none.
	Context *ocp.ContextEstimate `json:"context,omitempty"`
}

type showGlobal struct {
	Skills  []showEntry `json:"skills,omitempty"`
	Plugins []showEntry `json:"plugins,omitempty"`
}

type showDoc struct {
	Current  string        `json:"current_machine,omitempty"`
	Active   string        `json:"active,omitempty"`
	Machines []string      `json:"machines,omitempty"`
	Global   *showGlobal   `json:"global,omitempty"`
	Profiles []showProfile `json:"profiles"`
	Warnings []string      `json:"warnings,omitempty"`
}

func (r *Runner) show(p ocp.Paths, args []string) error {
	var o showOptions
	profile, err := parseShow(args, &o)
	if err != nil {
		return err
	}
	state, err := sourceState(p)
	if err != nil {
		return err
	}
	source, err := canonical(state.Source)
	if err != nil {
		return err
	}
	doc, err := config.Load(filepath.Join(source, config.FileName))
	if err != nil {
		return err
	}
	ex, err := doc.ExplainFromDir(source)
	if err != nil {
		return err
	}
	active, _ := ocp.ActiveProfile(p)

	out := showDoc{Current: state.Machine, Active: active, Machines: ex.Machines, Profiles: []showProfile{}}
	estimates := contextEstimates(p, source, doc, state.Machine)
	for _, v := range ex.Profiles {
		if profile == "" || v.Profile.Name == profile {
			sp := buildShowProfile(v, source, v.Profile.Name == active)
			sp.Disabled = append([]string(nil), v.Profile.Disabled...)
			if est, ok := estimates[v.Profile.Name]; ok {
				sp.Context = &est
			}
			out.Profiles = append(out.Profiles, sp)
		}
	}
	if profile != "" && len(out.Profiles) == 0 {
		names := make([]string, 0, len(ex.Profiles))
		for _, v := range ex.Profiles {
			names = append(names, v.Profile.Name)
		}
		return fmt.Errorf("unknown profile %q; available: %s", profile, strings.Join(names, ", "))
	}
	if profile == "" {
		out.Global = buildShowGlobal(doc, source)
		out.Warnings = append(out.Warnings, ex.Warnings...)
		if files := unreferencedLocal(source, ex); len(files) > 0 {
			out.Warnings = append(out.Warnings, unreferencedText(files))
		}
	}

	if o.json {
		enc := json.NewEncoder(r.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	v := &showView{w: r.c, current: state.Machine, all: o.all}
	if profile == "" {
		v.overview(out)
	} else {
		v.detail(out.Profiles[0])
	}
	v.footnote()
	_, err = io.WriteString(r.Out, v.sb.String())
	return err
}

// parseShow accepts flags before or after the optional profile name.
func parseShow(args []string, o *showOptions) (string, error) {
	f := flag.NewFlagSet("show", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configureShow(f, o)
	var positional []string
	for {
		if err := f.Parse(args); err != nil {
			return "", commandUsageError("show", err)
		}
		if f.NArg() == 0 {
			break
		}
		positional = append(positional, f.Arg(0))
		args = f.Args()[1:]
	}
	if len(positional) > 1 {
		return "", commandUsageError("show", errors.New("show accepts at most one profile"))
	}
	if len(positional) == 1 {
		return positional[0], nil
	}
	return "", nil
}

// ---- building the display model -------------------------------------------

func buildShowGlobal(doc *config.Document, source string) *showGlobal {
	g := &showGlobal{}
	attr := config.Attribution{Origin: config.OriginGlobal}
	for _, s := range doc.Skills {
		g.Skills = append(g.Skills, skillEntry(s, attr))
	}
	for _, plugin := range doc.Plugins {
		g.Plugins = append(g.Plugins, pluginEntry(plugin, attr, source))
	}
	for _, plugin := range config.ConfigPluginNames(doc.Config) {
		if e := pluginEntry(plugin, attr, source); !hasEntry(g.Plugins, e) {
			g.Plugins = append(g.Plugins, e)
		}
	}
	return g
}

func hasEntry(es []showEntry, e showEntry) bool {
	for _, x := range es {
		if x.Name == e.Name && x.Machine == e.Machine && x.Origin == e.Origin {
			return true
		}
	}
	return false
}

func buildShowProfile(v config.ProfileView, source string, active bool) showProfile {
	sp := showProfile{Name: v.Profile.Name, Active: active, Extends: v.Extends, Hosts: v.Hosts}
	for _, s := range v.Skills {
		sp.Skills = append(sp.Skills, skillEntry(s.Skill, s.Attribution))
	}
	for _, pl := range v.Plugins {
		sp.Plugins = append(sp.Plugins, pluginEntry(pl.Plugin, pl.Attribution, source))
	}
	for _, c := range v.Config {
		switch {
		case c.Path == "model":
			if model, ok := c.Value.(string); ok && model != "" {
				sp.Models = append(sp.Models, attributed(showEntry{Name: clean(model)}, c.Attribution))
			}
		case strings.HasPrefix(c.Path, "mcp."):
			name := strings.TrimPrefix(strings.TrimPrefix(c.Path, "mcp."), "servers.")
			if c.Path == "mcp.timeout" || c.Path == "mcp.servers" {
				continue // settings, not servers
			}
			if _, isServer := c.Value.(map[string]any); !isServer {
				continue
			}
			sp.MCP = append(sp.MCP, attributed(showEntry{Name: clean(name), Kind: mcpKind(c.Value)}, c.Attribution))
		case strings.HasPrefix(c.Path, "providers."), strings.HasPrefix(c.Path, "provider."):
			name := c.Path[strings.Index(c.Path, ".")+1:]
			sp.Providers = append(sp.Providers, attributed(showEntry{Name: clean(name)}, c.Attribution))
		}
	}
	for _, a := range v.Agents {
		sp.Agents = append(sp.Agents, attributed(showEntry{Name: clean(a.Name)}, a.Attribution))
	}
	for _, in := range v.Instructions {
		sp.Instructions = append(sp.Instructions, attributed(showEntry{Name: clean(in.Name)}, in.Attribution))
	}
	for _, f := range v.Files {
		sp.Files = append(sp.Files, attributed(showEntry{Name: clean(f.Name)}, f.Attribution))
	}
	return sp
}

func attributed(e showEntry, a config.Attribution) showEntry {
	e.Origin, e.Machine, e.Override = a.Origin, clean(a.Machine), a.Override
	return e
}

func mcpKind(server any) string {
	m, _ := server.(map[string]any)
	switch t, _ := m["type"].(string); t {
	case "remote":
		return "remote"
	case "local", "":
		return "stdio"
	default:
		return clean(t)
	}
}

func skillEntry(s config.Skill, a config.Attribution) showEntry {
	if !s.IsGit() {
		return attributed(showEntry{Name: clean(s.Name()), Kind: "local"}, a)
	}
	return attributed(showEntry{Name: clean(s.Name()), Kind: "git", Source: repoLabel(s.Source), Ref: clean(s.Ref)}, a)
}

// stripUserinfo removes "user:password@" from the authority of a URL
// ("scheme://user:pw@host/..."), so credentials embedded in a source never
// reach output. Bare package specs such as "pkg@1.2.3" or "@scope/pkg" have no
// scheme and are left alone.
func stripUserinfo(s string) string {
	i := strings.Index(s, "://")
	if i < 0 {
		return s
	}
	scheme, rest := s[:i+3], s[i+3:]
	authority := rest
	if j := strings.Index(rest, "/"); j >= 0 {
		authority = rest[:j]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return scheme + rest
}

// clean replaces control characters, which could carry terminal escapes, in
// text that comes from the configuration.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}

// repoLabel shortens a Git URL to owner/repo for GitHub and host/path
// elsewhere, without credentials, query strings or fragments.
func repoLabel(source string) string {
	s := source
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = stripUserinfo(s)
		s = s[strings.Index(s, "://")+3:]
	} else if at := strings.LastIndex(strings.SplitN(s, "/", 2)[0], "@"); at >= 0 {
		s = s[at+1:] // scp-like user@host:path
		s = strings.Replace(s, ":", "/", 1)
	} else {
		s = strings.Replace(s, ":", "/", 1)
	}
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	return clean(strings.TrimPrefix(s, "github.com/"))
}

func pluginEntry(plugin string, a config.Attribution, source string) showEntry {
	if ocp.IsLocalPlugin(plugin) {
		return attributed(showEntry{Name: clean(path.Base(filepath.ToSlash(plugin))), Kind: "local"}, a)
	}
	if strings.HasPrefix(plugin, "/") || strings.HasPrefix(plugin, "~") || strings.HasPrefix(plugin, "{env:") {
		kind := "path"
		if within(source, expandPath(plugin)) {
			kind = "local"
		}
		return attributed(showEntry{Name: clean(path.Base(filepath.ToSlash(plugin))), Kind: kind}, a)
	}
	return attributed(showEntry{Name: clean(stripUserinfo(plugin)), Kind: "npm"}, a)
}

// expandPath resolves ~ and {env:NAME} the way plugin paths are written.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + strings.TrimPrefix(p, "~")
		}
	}
	for strings.HasPrefix(p, "{env:") {
		end := strings.Index(p, "}")
		if end < 0 {
			break
		}
		p = os.Getenv(p[len("{env:"):end]) + p[end+1:]
	}
	return filepath.Clean(p)
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// ---- unreferenced local files ---------------------------------------------

// unreferencedLocal lists local skills and plugins that no profile uses. Only
// things OCP can load are candidates: skill folders holding a SKILL.md, plugin
// script files, and plugin folders holding a script or package.json. Support
// files such as package.json or node_modules are never reported.
func unreferencedLocal(source string, ex config.Explanation) []string {
	skillsRoot := filepath.Join(source, "skills")
	usedSkills, usedPlugins := map[string]bool{}, map[string]bool{}
	for _, v := range ex.Profiles {
		for _, s := range v.Skills {
			if s.Skill.IsGit() {
				continue
			}
			src := s.Skill.Source
			if filepath.IsAbs(src) {
				if rel, err := filepath.Rel(skillsRoot, src); err == nil && !strings.HasPrefix(rel, "..") {
					usedSkills[filepath.ToSlash(rel)] = true
				}
				continue
			}
			usedSkills[path.Clean(strings.TrimPrefix(strings.TrimPrefix(src, "./"), "skills/"))] = true
		}
		for _, p := range v.Plugins {
			if top := localPluginTop(p.Plugin, source); top != "" {
				usedPlugins[top] = true
			}
		}
	}

	var out []string
	_ = filepath.WalkDir(skillsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == skillsRoot {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := os.Stat(p); err != nil || !info.IsDir() { // follows symlinked skill folders
			return nil
		}
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err != nil {
			return nil // a grouping folder such as skills/apple: keep looking inside
		}
		rel, _ := filepath.Rel(skillsRoot, p)
		if rel = filepath.ToSlash(rel); !usedSkills[rel] {
			out = append(out, "skills/"+rel)
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	entries, _ := os.ReadDir(filepath.Join(source, "plugins"))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || usedPlugins[name] {
			continue
		}
		if isPluginCandidate(filepath.Join(source, "plugins", name)) {
			out = append(out, "plugins/"+name)
		}
	}
	for i := range out {
		out[i] = clean(out[i])
	}
	sort.Strings(out)
	return out
}

// isPluginCandidate reports whether a path under plugins/ is something OCP can
// load: a script file, or a folder holding a script or a package.json.
func isPluginCandidate(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return isPluginScript(p)
	}
	entries, _ := os.ReadDir(p)
	for _, e := range entries {
		if e.Name() == "package.json" || (!e.IsDir() && isPluginScript(e.Name())) {
			return true
		}
	}
	return false
}

func isPluginScript(name string) bool {
	switch filepath.Ext(name) {
	case ".ts", ".js", ".mjs", ".cjs", ".mts", ".cts":
		return true
	}
	return false
}

// localPluginTop returns the top-level name under plugins/ that a local plugin
// declaration refers to, or "" when it is not local to the source.
func localPluginTop(plugin, source string) string {
	var rel string
	switch {
	case ocp.IsLocalPlugin(plugin):
		rel = strings.TrimPrefix(strings.TrimPrefix(filepath.ToSlash(plugin), "./"), "plugins/")
	case strings.HasPrefix(plugin, "/") || strings.HasPrefix(plugin, "~") || strings.HasPrefix(plugin, "{env:"):
		abs := expandPath(plugin)
		if !within(source, abs) {
			return ""
		}
		r, _ := filepath.Rel(filepath.Join(source, "plugins"), abs)
		rel = filepath.ToSlash(r)
	default:
		return ""
	}
	if rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.SplitN(rel, "/", 2)[0]
}

// ---- rendering --------------------------------------------------------------

// showView renders the agreed `ocp show` layout. The reference design and its
// fixtures live in tools/preview; keep the two in step.
type showView struct {
	w       *color.Writer
	current string
	all     bool

	sb           strings.Builder
	sawOverride  bool // an override mark was printed: explain it in the footnote
	sawCollapsed bool // a skill list was grouped: mention --all in the footnote
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visibleLen(s string) int { return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, "")) }

func pad(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func (v *showView) line(format string, a ...any) { fmt.Fprintf(&v.sb, format+"\n", a...) }

// gap starts a new block: a blank line unless one is already there.
func (v *showView) gap() {
	if s := v.sb.String(); s != "" && !strings.HasSuffix(s, "\n\n") {
		v.sb.WriteString("\n")
	}
}

func (v *showView) glyph(unicode, ascii string) string {
	if v.w.Styled() {
		return unicode
	}
	return ascii
}

func (v *showView) style(code, text string) string {
	if code == "" {
		return text
	}
	return v.w.Style(code, text)
}

func (v *showView) title(s string) string { return v.style(color.ANSIBold, s) }
func (v *showView) dim(s string) string   { return v.style(color.ANSIDim, s) }
func (v *showView) tree(s string) string  { return v.style(color.ANSIDim, s) }
func (v *showView) label(s string) string { return v.style(color.ANSIDim, s) }
func (v *showView) mark() string          { return v.style(color.ANSIYellow, markOverride) }
func (v *showView) markWithName(n string) string {
	return n + v.mark()
}

func (v *showView) machine(name string) string {
	if name == v.current {
		return v.style(color.ANSIBoldMagenta, name)
	}
	return v.style(color.ANSIMagenta, name)
}

func (v *showView) hosts(hosts []string) string {
	hs := make([]string, len(hosts))
	for i, h := range hosts {
		hs[i] = v.machine(h)
	}
	return strings.Join(hs, ", ")
}

// activeLabel marks the active profile. Plain output uses the word alone, so
// "*" stays free for the override mark.
func (v *showView) activeLabel() string {
	if v.w.Styled() {
		return v.style(color.ANSIBoldGreen, "● active")
	}
	return "active"
}

// origin renders the origin column: nothing for entries the profile declares,
// the machine name for overlay entries, otherwise where it is inherited from.
func (v *showView) origin(e showEntry) string {
	switch {
	case e.Machine != "":
		return v.machine(e.Machine)
	case e.Origin == config.OriginGlobal:
		return v.style(color.ANSICyan, e.Origin)
	case strings.HasPrefix(e.Origin, "extends"):
		return v.style(color.ANSIBlue, e.Origin)
	}
	return ""
}

func (v *showView) kindStyle(label string) string {
	switch label {
	case "local", "path":
		return color.ANSIYellow
	case "npm":
		return color.ANSIRed
	case "remote", "stdio":
		return color.ANSIGreen
	}
	return ""
}

// rank orders entries: shared first, then the current machine, then the others.
func (v *showView) rank(e showEntry) int {
	switch {
	case e.Machine == "":
		return 0
	case e.Machine == v.current:
		return 1
	}
	return 2
}

func (v *showView) overview(d showDoc) {
	v.line("%s", v.title("Global"))
	if len(d.Global.Skills) > 0 {
		v.line("%s  %s", v.label(pad("skills", 9)), v.fit(d.Global.Skills, v.w.Width()-11))
	}
	if len(d.Global.Plugins) > 0 {
		v.line("%s  %s", v.label(pad("plugins", 9)), v.fit(d.Global.Plugins, v.w.Width()-11))
	}
	if len(d.Machines) > 0 {
		ms := make([]string, len(d.Machines))
		for i, m := range d.Machines {
			ms[i] = v.machine(m)
		}
		v.line("%s  %s", v.label(pad("machines", 9)), strings.Join(ms, v.glyph(" · ", ", ")))
	}
	v.line("%s", v.tree(v.glyph("│", "|")))
	var tops []showProfile
	for _, p := range d.Profiles {
		if p.Extends == "" {
			tops = append(tops, p)
		}
	}
	for i, p := range tops {
		v.node(d.Profiles, p, "", i == len(tops)-1)
	}
	if len(d.Warnings) > 0 {
		v.gap()
		for _, w := range d.Warnings {
			v.line("%s %s", v.style(color.ANSIYellow, "!"), w)
		}
	}
}

func unreferencedText(files []string) string {
	noun := "files"
	if len(files) == 1 {
		noun = "file"
	}
	shown := files
	extra := ""
	if len(files) > 4 {
		shown, extra = files[:4], fmt.Sprintf(", + %d more", len(files)-4)
	}
	return fmt.Sprintf("%d unreferenced local %s: %s%s", len(files), noun, strings.Join(shown, ", "), extra)
}

// node draws a profile and, nested beneath it, the profiles that extend it.
// Rows list only what the profile adds: anything already shown under Global or
// on a parent is not repeated.
func (v *showView) node(all []showProfile, p showProfile, prefix string, last bool) {
	branch, cont := v.glyph("├── ", "|-- "), v.glyph("│   ", "|   ")
	if last {
		branch, cont = v.glyph("└── ", "`-- "), "    "
	}
	head := v.title(p.Name)
	if p.Active {
		head += " " + v.activeLabel()
	}
	if len(p.Hosts) > 0 {
		head += " " + v.dim("(") + v.hosts(p.Hosts) + v.dim(" only)")
	}
	if p.Context != nil && p.Context.Tokens > 0 {
		head += "  " + v.dim("~"+ocp.FormatTokens(p.Context.Tokens)+" tokens")
	}
	v.line("%s%s%s", prefix, v.tree(branch), head)

	var kids []showProfile
	for _, c := range all {
		if c.Extends == p.Name {
			kids = append(kids, c)
		}
	}
	bar := " "
	if len(kids) > 0 {
		bar = v.glyph("│", "|")
	}
	pre := prefix + v.tree(cont) + v.tree(bar) + " "
	row := func(label, value string) { v.line("%s%s %s", pre, v.label(pad(label, 9)), value) }
	list := func(label string, es []showEntry) {
		if own := ownEntries(es); len(own) > 0 {
			row(label, v.fit(own, v.w.Width()-visibleLen(pre)-10))
		}
	}

	if own := ownEntries(p.Models); len(own) > 0 {
		value := own[0].Name
		if own[0].Override {
			v.sawOverride = true
			value += " " + v.mark()
		}
		row("model", value)
	}
	list("skills", p.Skills)
	list("plugins", p.Plugins)
	list("files", p.Files)
	if len(p.Disabled) > 0 {
		row("disabled", v.dim(strings.Join(p.Disabled, "  ")))
	}
	for i, k := range kids {
		if i == 0 {
			v.line("%s%s", prefix+v.tree(cont), v.tree(bar))
		}
		v.node(all, k, prefix+v.tree(cont), i == len(kids)-1)
	}
	if !last {
		v.line("%s%s", prefix, v.tree(v.glyph("│", "|")))
	}
}

// ownEntries keeps entries the profile itself declares.
func ownEntries(es []showEntry) []showEntry {
	var out []showEntry
	for _, e := range es {
		if e.own() {
			out = append(out, e)
		}
	}
	return out
}

func (v *showView) nameList(es []showEntry) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Name
		if e.Override {
			v.sawOverride = true
			parts[i] = v.markWithName(e.Name)
		}
	}
	return strings.Join(parts, "  ")
}

// fit lists as many names as fit in avail columns, then "+ N others". It never
// adds detail about the hidden ones and always shows at least one name.
func (v *showView) fit(es []showEntry, avail int) string {
	if len(es) == 0 {
		return ""
	}
	width := func(i int) int {
		n := utf8.RuneCountInString(es[i].Name)
		if es[i].Override {
			n += utf8.RuneCountInString(markOverride)
		}
		return n
	}
	used := func(n int) int {
		w := 0
		for i := 0; i < n; i++ {
			w += width(i)
			if i > 0 {
				w += 2
			}
		}
		return w
	}
	suffix := func(hidden int) string {
		if hidden == 1 {
			return " + 1 other"
		}
		return fmt.Sprintf(" + %d others", hidden)
	}
	shown := len(es)
	if used(shown) > avail {
		for shown = len(es) - 1; shown > 1; shown-- {
			if used(shown)+len(suffix(len(es)-shown)) <= avail {
				break
			}
		}
	}
	shown = max(shown, 1)
	out := v.nameList(es[:shown])
	if shown < len(es) {
		out += v.style(color.ANSICyan, suffix(len(es)-shown))
	}
	return out
}

type tableRow struct {
	name     string
	count    bool
	source   string
	entry    showEntry
	override bool
}

func (v *showView) detail(p showProfile) {
	head := v.title(p.Name)
	if p.Active {
		head += "  " + v.activeLabel()
	}
	v.line("%s", head)
	if p.Extends != "" {
		v.line("%s  %s", v.label(pad("extends", 8)), v.style(color.ANSIBlue, p.Extends))
	}
	if len(p.Hosts) > 0 {
		v.line("%s  %s", v.label(pad("hosts", 8)), v.hosts(p.Hosts))
	}
	models := append([]showEntry(nil), p.Models...)
	sort.SliceStable(models, func(i, j int) bool { return v.rank(models[i]) < v.rank(models[j]) })
	for _, m := range models {
		line := v.label(pad("model", 8)) + "  " + m.Name
		if o := v.origin(m); o != "" {
			line += "  " + o
		}
		if m.Override {
			v.sawOverride = true
			line += " " + v.mark()
		}
		v.line("%s", line)
	}
	v.context(p.Context)
	v.table("skills", p.Skills, true)
	v.table("plugins", p.Plugins, false)
	v.table("mcp", p.MCP, false)
	v.table("providers", p.Providers, false)
	v.table("agents", p.Agents, false)
	v.table("instructions", p.Instructions, false)
	v.table("files", p.Files, false)
	if len(p.Disabled) > 0 {
		v.gap()
		v.line("%s", v.style(color.ANSIBoldDim, "disabled"))
		for _, name := range p.Disabled {
			v.line("  %s", v.dim(name))
		}
	}
}

// groupSkills groups skills by source, origin and machine, largest group first.
func groupSkills(es []showEntry) []tableRow {
	type group struct {
		row tableRow
		n   int
	}
	var groups []*group
	index := map[string]*group{}
	for _, e := range es {
		k := e.label() + "\x00" + e.Origin + "\x00" + e.Machine
		g := index[k]
		if g == nil {
			g = &group{row: tableRow{source: e.label(), entry: e}}
			index[k] = g
			groups = append(groups, g)
		}
		g.n++
		g.row.override = g.row.override || e.Override
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a].n > groups[b].n })
	rows := make([]tableRow, len(groups))
	for i, g := range groups {
		g.row.name, g.row.count = fmt.Sprintf("%d skills", g.n), true
		if g.n == 1 {
			g.row.name = "1 skill"
		}
		rows[i] = g.row
	}
	return rows
}

func (v *showView) table(kind string, es []showEntry, skills bool) {
	if len(es) == 0 {
		return
	}
	es = append([]showEntry(nil), es...)
	sort.SliceStable(es, func(i, j int) bool { return v.rank(es[i]) < v.rank(es[j]) })

	var rows []tableRow
	if skills && len(es) > collapseAt && !v.all {
		v.sawCollapsed = true
		rows = groupSkills(es)
	} else {
		for _, e := range es {
			rows = append(rows, tableRow{name: e.Name, source: e.label(), entry: e, override: e.Override})
		}
	}
	v.gap()
	v.line("%s", v.style(color.ANSIBoldDim, kind))
	w, sw := 0, 0
	for _, r := range rows {
		w, sw = max(w, utf8.RuneCountInString(r.name)), max(sw, utf8.RuneCountInString(r.source))
	}
	for _, r := range rows {
		origin := v.origin(r.entry)
		name := pad(r.name, w)
		if r.count {
			name = v.style(color.ANSICyan, name)
		}
		line := "  " + name
		if sw > 0 {
			src := r.source
			if origin != "" || r.override {
				src = pad(src, sw) // align only when another column follows
			}
			line += "  " + v.style(v.kindStyle(r.source), src)
		}
		if origin != "" {
			line += "  " + origin
		}
		if r.override {
			v.sawOverride = true
			sep := "  "
			if origin != "" {
				sep = " "
			}
			line += sep + v.mark()
		}
		v.line("%s", strings.TrimRight(line, " "))
	}
}

// footnote explains marks that were actually printed, once, at the bottom.
func (v *showView) footnote() {
	var notes []string
	if v.sawOverride {
		notes = append(notes, markOverride+" overrides an inherited value")
	}
	if v.sawCollapsed {
		notes = append(notes, "--all lists every skill")
	}
	if len(notes) > 0 {
		v.gap()
		v.line("%s", v.dim(strings.Join(notes, v.glyph(" · ", ", "))))
	}
}

// contextEstimates measures every profile that renders on this machine. It is
// best effort: a configuration that does not resolve here shows no estimate.
func contextEstimates(p ocp.Paths, source string, doc *config.Document, machine string) map[string]ocp.ContextEstimate {
	out := map[string]ocp.ContextEstimate{}
	profiles, err := doc.ResolveFromDirForMachine(source, machine)
	if err != nil {
		return out
	}
	var provider ocp.SkillProvider
	if gs, err := gitSkills(source); err == nil {
		if cached, err := skills.Cached(source, p.Data, gs); err == nil {
			provider = &memoProvider{inner: cached, dirs: map[string]memoDir{}}
		}
	}
	for _, profile := range profiles {
		if est, err := ocp.EstimateContext(source, profile, machine, provider); err == nil {
			out[profile.Name] = est
		}
	}
	return out
}

// context prints what the profile loads before the first prompt.
func (v *showView) context(est *ocp.ContextEstimate) {
	if est == nil || est.Tokens == 0 && est.OnDemand == 0 && est.MCPServers == 0 && len(est.Unmeasured) == 0 {
		return
	}
	v.gap()
	v.line("%s  %s", v.style(color.ANSIBoldDim, "context"), v.dim("~"+ocp.FormatTokens(est.Tokens)+" tokens before the first prompt (estimate)"))
	w := 0
	for _, part := range est.Parts {
		w = max(w, len(part.Kind))
	}
	for _, part := range est.Parts {
		count := ""
		if part.Count > 0 {
			count = fmt.Sprintf("%3d", part.Count)
		}
		v.line("%s", strings.TrimRight(fmt.Sprintf("  %s %s  %s", pad(part.Kind, w), count, ocp.FormatTokens(part.Tokens)), " "))
	}
	var notes []string
	if est.OnDemand > 0 {
		notes = append(notes, "skill bodies ~"+ocp.FormatTokens(est.OnDemand)+" load when used")
	}
	if est.MCPServers > 0 {
		noun := "servers"
		if est.MCPServers == 1 {
			noun = "server"
		}
		notes = append(notes, fmt.Sprintf("%d MCP %s not measured", est.MCPServers, noun))
	}
	if n := len(est.Unmeasured); n == 1 {
		notes = append(notes, "1 skill not measured (not cached yet, or unreadable)")
	} else if n > 1 {
		notes = append(notes, fmt.Sprintf("%d skills not measured (not cached yet, or unreadable)", n))
	}
	if len(notes) > 0 {
		v.line("  %s", v.dim(strings.Join(notes, v.glyph(" · ", ", "))))
	}
}

type memoDir struct {
	dir string
	err error
}

// memoProvider resolves each Git skill once per command, since profiles
// share skills and each lookup runs git.
type memoProvider struct {
	inner ocp.SkillProvider
	dirs  map[string]memoDir
}

func (m *memoProvider) SkillDir(s config.Skill) (string, error) {
	key := s.Source + "\x00" + s.Path + "\x00" + s.Ref
	if d, ok := m.dirs[key]; ok {
		return d.dir, d.err
	}
	dir, err := m.inner.SkillDir(s)
	m.dirs[key] = memoDir{dir, err}
	return dir, err
}
