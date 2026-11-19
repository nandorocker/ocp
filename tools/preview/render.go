package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type R struct {
	color, ascii bool
	sb           strings.Builder
	sawOverride  bool // an override mark was printed: explain it in the footnote
	sawCollapsed bool // a skill list was grouped: mention --all in the footnote
}

// mark is the override symbol. It is plain ASCII, so it needs no fallback.
// activeLabel marks the active profile. Plain output uses the word alone, so
// "*" stays free for the override mark.
func (r *R) activeLabel() string {
	if r.ascii {
		return "active"
	}
	return "● active"
}

func (r *R) mark() string { return "*" }

func (r *R) st(key, s string) string {
	code := theme[key]
	if !r.color || code == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
func (r *R) glyph(unicode, ascii string) string {
	if r.ascii {
		return ascii
	}
	return unicode
}

// gap starts a new block: a blank line unless one is already there.
func (r *R) gap() {
	s := r.sb.String()
	if s != "" && !strings.HasSuffix(s, "\n\n") {
		r.sb.WriteString("\n")
	}
}
func (r *R) line(f string, a ...any) { fmt.Fprintf(&r.sb, f+"\n", a...) }
func pad(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// originLabel is the text shown in the origin column: nothing for entries the
// profile declares itself, otherwise where the entry is inherited from.
func originLabel(o string) string {
	if o == "profile" {
		return ""
	}
	return o
}

func isMachine(o string) bool {
	for _, m := range machineNames {
		if o == m {
			return true
		}
	}
	return false
}

func (r *R) originKey(o string) string {
	switch {
	case o == "global":
		return "origin.global"
	case strings.HasPrefix(o, "extends"):
		return "origin.extends"
	case o == currentMachine:
		return "origin.machine.current"
	case isMachine(o):
		return "origin.machine"
	}
	return "dim"
}

// machineRank orders entries: shared first, then the current machine, then the
// other machines.
func machineRank(o string) int {
	switch {
	case o == currentMachine:
		return 1
	case isMachine(o):
		return 2
	}
	return 0
}

func (r *R) srcKey(s string) string {
	switch s {
	case "local":
		return "src.local"
	case "npm":
		return "src.npm"
	case "remote":
		return "src.remote"
	case "stdio":
		return "src.stdio"
	}
	return "src.repo"
}

type row struct {
	name, source, origin string
	override             bool
}

// collapse groups long skill lists by repository.
func collapse(es []Entry, limit int) (rows []row, keys [][2]string) {
	if len(es) <= limit {
		for _, e := range es {
			rows = append(rows, row{e.Name, e.Source, e.Origin, e.Override})
		}
		return
	}
	type g struct {
		n      int
		origin string
	}
	groups := map[string]*g{}
	var order []string
	for _, e := range es {
		k := e.Source + "|" + e.Origin
		if groups[k] == nil {
			groups[k] = &g{origin: e.Origin}
			order = append(order, k)
		}
		groups[k].n++
	}
	sort.SliceStable(order, func(i, j int) bool { return groups[order[i]].n > groups[order[j]].n })
	for _, k := range order {
		src := strings.SplitN(k, "|", 2)[0]
		label := fmt.Sprintf("%d skills", groups[k].n)
		if groups[k].n == 1 {
			label = "1 skill"
		}
		rows = append(rows, row{label, src, groups[k].origin, false})
	}
	return
}

func (r *R) table(kind string, es []Entry, indent string, collapseAt int) {
	if len(es) == 0 {
		return
	}
	es = append([]Entry(nil), es...)
	sort.SliceStable(es, func(i, j int) bool { return machineRank(es[i].Origin) < machineRank(es[j].Origin) })
	r.gap()
	r.line("%s%s", indent, r.st("kind", kind))
	rows, _ := collapse(es, collapseAt)
	if len(es) > collapseAt {
		r.sawCollapsed = true
	}
	w, sw := 0, 0
	for _, x := range rows {
		w = max(w, utf8.RuneCountInString(x.name))
		sw = max(sw, utf8.RuneCountInString(x.source))
	}
	for _, x := range rows {
		label := originLabel(x.origin)
		name := pad(x.name, w)
		if strings.Contains(x.name, " skill") {
			name = r.st("count", name)
		}
		line := indent + "  " + name
		if sw > 0 {
			src := x.source
			if label != "" || x.override {
				src = pad(src, sw) // align only when another column follows
			}
			line += "  " + r.st(r.srcKey(x.source), src)
		}
		if label != "" {
			line += "  " + r.st(r.originKey(x.origin), label)
		}
		if x.override {
			r.sawOverride = true
			sep := "  "
			if label != "" {
				sep = " "
			}
			line += sep + r.st("override", r.mark())
		}
		r.line("%s", strings.TrimRight(line, " "))
	}
}

func (r *R) overview() {
	ps := profiles()
	t := r.st("tree", r.glyph("│", "|"))
	// The shared block has no guide; the tree starts below it.
	r.line("%s", r.st("title", "Global"))
	r.line("%s  %s", r.st("label", pad("skills", 9)), r.fit(rootSkills, termWidth()-11))
	ms := make([]string, len(machineNames))
	for i, m := range machineNames {
		ms[i] = r.st(r.machineKey(m), m)
	}
	r.line("%s  %s", r.st("label", pad("machines", 9)), strings.Join(ms, r.glyph(" · ", ", ")))
	r.line("%s", t)
	var tops []Profile
	for _, p := range ps {
		if p.Extends == "" {
			tops = append(tops, p)
		}
	}
	for i, p := range tops {
		r.node(ps, p, "", i == len(tops)-1)
	}
	if len(warnings) > 0 {
		r.gap()
		for _, w := range warnings {
			r.line("%s %s", r.st("warn", "!"), w)
		}
	}
}

func (r *R) machineKey(m string) string {
	if m == currentMachine {
		return "machine.current"
	}
	return "machine"
}

// node draws a profile and, nested beneath it, the profiles that extend it.
// Rows list only what the profile adds: anything already shown at the root or
// on a parent is not repeated.
func (r *R) node(all []Profile, p Profile, prefix string, last bool) {
	branch, cont := r.glyph("├── ", "|-- "), r.glyph("│   ", "|   ")
	if last {
		branch, cont = r.glyph("└── ", "`-- "), "    "
	}
	head := r.st("title", p.Name)
	if p.Active {
		head += " " + r.st("active", r.activeLabel())
	}
	if len(p.Hosts) > 0 {
		head += " " + r.st("label", "(") + r.hostList(p.Hosts) + r.st("label", " only)")
	}
	r.line("%s%s%s", prefix, r.st("tree", branch), head)

	var kids []Profile
	for _, c := range all {
		if c.Extends == p.Name {
			kids = append(kids, c)
		}
	}
	bar := " "
	if len(kids) > 0 {
		bar = r.glyph("│", "|")
	}
	pre := prefix + r.st("tree", cont) + r.st("tree", bar) + " "
	row := func(label, value string) { r.line("%s%s %s", pre, r.st("label", pad(label, 9)), value) }
	// list fits a name list into what is left of the terminal line.
	list := func(label string, es []Entry) {
		row(label, r.fit(es, termWidth()-visibleLen(pre)-10))
	}

	if p.ModelOwn {
		v := p.Model
		if p.ModelOver {
			r.sawOverride = true
			v += " " + r.st("override", r.mark())
		}
		row("model", v)
	}
	if own := ownEntries(p.Skills); len(own) > 0 {
		list("skills", own)
	}
	if own := ownEntries(p.Plugins); len(own) > 0 {
		list("plugins", own)
	}
	if own := ownEntries(p.Files); len(own) > 0 {
		list("files", own)
	}
	for i, k := range kids {
		if i == 0 {
			r.line("%s%s", prefix+r.st("tree", cont), r.st("tree", bar))
		}
		r.node(all, k, prefix+r.st("tree", cont), i == len(kids)-1)
	}
	if !last {
		r.line("%s%s", prefix, r.st("tree", r.glyph("│", "|")))
	}
}

// ownEntries keeps entries declared by the profile itself.
func ownEntries(es []Entry) []Entry {
	var out []Entry
	for _, e := range es {
		if e.Origin == "profile" {
			out = append(out, e)
		}
	}
	return out
}

func (r *R) nameList(es []Entry) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Name
		if e.Override {
			r.sawOverride = true
			parts[i] += r.st("override", r.mark())
		}
	}
	return strings.Join(parts, "  ")
}

func (r *R) hostList(hosts []string) string {
	hs := make([]string, len(hosts))
	for i, h := range hosts {
		hs[i] = r.st(r.machineKey(h), h)
	}
	return strings.Join(hs, ", ")
}

// termWidth is the width available for output: WIDTH (for testing), then
// COLUMNS, then the controlling terminal, then 80.
func termWidth() int {
	for _, env := range []string{"WIDTH", "COLUMNS"} {
		if n, err := strconv.Atoi(os.Getenv(env)); err == nil && n > 0 {
			return n
		}
	}
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	if out, err := cmd.Output(); err == nil {
		var rows, cols int
		if _, err := fmt.Sscanf(string(out), "%d %d", &rows, &cols); err == nil && cols > 0 {
			return cols
		}
	}
	return 80
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visibleLen(s string) int { return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, "")) }

// fit lists as many names as fit in avail columns, then "+ N others". It never
// adds detail about the hidden ones. At least one name is always shown.
func (r *R) fit(es []Entry, avail int) string {
	if len(es) == 0 {
		return ""
	}
	width := func(i int) int {
		n := utf8.RuneCountInString(es[i].Name)
		if es[i].Override {
			n++ // the override mark
		}
		return n
	}
	// used is the width of the first n names joined by two spaces.
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
	out := r.nameList(es[:shown])
	if shown < len(es) {
		out += r.st("count", suffix(len(es)-shown))
	}
	return out
}

// reachable drops machine-overlay entries that can never apply to a profile
// limited to other machines with hosts.
func reachable(p Profile, es []Entry) []Entry {
	if len(p.Hosts) == 0 {
		return es
	}
	var out []Entry
	for _, e := range es {
		keep := !isMachine(e.Origin)
		for _, h := range p.Hosts {
			keep = keep || e.Origin == h
		}
		if keep {
			out = append(out, e)
		}
	}
	return out
}

func (r *R) detail(name string) {
	for _, p := range profiles() {
		if p.Name != name {
			continue
		}
		head := r.st("title", p.Name)
		if p.Active {
			head += "  " + r.st("active", r.activeLabel())
		}
		r.line("%s", head)
		if p.Extends != "" {
			r.line("%s  %s", r.st("label", pad("extends", 8)), r.st("extends", p.Extends))
		}
		if len(p.Hosts) > 0 {
			r.line("%s  %s", r.st("label", pad("hosts", 8)), r.hostList(p.Hosts))
		}
		if p.Model != "" {
			line := r.st("label", pad("model", 8)) + "  " + p.Model
			if !p.ModelOwn {
				line += "  " + r.st("origin.extends", "extends "+p.Extends)
			}
			if p.ModelOver {
				r.sawOverride = true
				line += " " + r.st("override", r.mark())
			}
			r.line("%s", line)
		}
		r.table("skills", reachable(p, p.Skills), "", 8)
		r.table("plugins", reachable(p, p.Plugins), "", 8)
		r.table("mcp", reachable(p, p.MCP), "", 8)
		r.table("files", reachable(p, p.Files), "", 8)
		if len(p.Disabled) > 0 {
			r.line("%s", r.st("kind", "disabled"))
			for _, d := range p.Disabled {
				r.line("  %s", r.st("muted", pad(d.Name, 20)+" "+d.Source+"  "+d.Origin))
			}
		}
	}
}

// footnote explains marks that were actually printed, once, at the bottom.
func (r *R) footnote() {
	var notes []string
	if r.sawOverride {
		notes = append(notes, r.mark()+" overrides an inherited value")
	}
	if r.sawCollapsed {
		notes = append(notes, "--all lists every skill")
	}
	if len(notes) > 0 {
		r.gap()
		r.line("%s", r.st("dim", strings.Join(notes, " · ")))
	}
}

func (r *R) notices() {
	for _, w := range warnings {
		r.line("%s %s", r.st("warn", "!"), w)
	}
}

func scenario(name string, color, ascii bool) string {
	r := &R{color: color, ascii: ascii}
	switch name {
	case "overview":
		r.overview()
	case "router":
		r.detail("router")
	case "apple":
		r.detail("apple")
	case "mixed":
		r.detail("mixed")
	case "lab":
		r.detail("lab")
	case "notices":
		r.notices()
	}
	r.footnote()
	return r.sb.String()
}

var _ = os.Stdout
