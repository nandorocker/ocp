package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nando/ocp/internal/color"
	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/ocp"
)

type adoptOptions struct {
	json  bool
	write string
}

func configureAdopt(f *flag.FlagSet, o *adoptOptions) {
	f.BoolVar(&o.json, "json", false, "print the inventory as JSON")
	f.StringVar(&o.write, "write", "", "create a new OCP source in this directory from the inventory")
}

// adopt inventories what Claude Code, Codex and OpenCode hold outside OCP
// and compares it with the source. It only reads, unless --write is given
// for a new source.
func (r *Runner) adopt(p ocp.Paths, args []string) error {
	var o adoptOptions
	if err := parse("adopt", args, func(f *flag.FlagSet) { configureAdopt(f, &o) }); err != nil {
		return err
	}
	source := ocp.AdoptSource{Skills: map[string]bool{}, MCP: map[string]bool{}, Instructions: map[string][]byte{}}
	state, err := ocp.LoadState(p)
	if err != nil {
		return err
	}
	haveSource := state != nil
	if haveSource {
		if err := loadAdoptSource(state, &source); err != nil {
			return err
		}
	}
	inv, err := ocp.Adopt(p, source)
	if err != nil {
		return err
	}
	if o.write != "" {
		if haveSource {
			return errors.New("OCP already has a source; add what you need to it instead (see `ocp adopt`), or run `ocp reset` first")
		}
		var harnesses []string
		seen := map[string]bool{}
		for _, in := range inv.Instructions {
			seen[in.Harness] = true
		}
		for _, items := range [][]ocp.AdoptItem{inv.Skills, inv.MCP} {
			for _, it := range items {
				for h := range it.Found {
					seen[h] = true
				}
			}
		}
		for _, h := range []string{"opencode", "claude", "codex"} {
			if seen[h] || h == "opencode" {
				harnesses = append(harnesses, h)
			}
		}
		dir, err := filepath.Abs(o.write)
		if err != nil {
			return err
		}
		warnings, err := ocp.WriteAdoption(dir, inv, harnesses)
		if err != nil {
			return err
		}
		r.c.Success("Created " + dir)
		for _, w := range warnings {
			r.c.Warning(w)
		}
		for _, s := range inv.Secrets {
			r.c.Warning(fmt.Sprintf("set %s: %s now reads it from the environment", s.Env, s.Where))
		}
		// Dry-run the install so conflicts with the hand-made originals show
		// now, not as a failed setup.
		for _, problem := range adoptionConflicts(p, dir, harnesses) {
			r.c.Warning("before setup: " + problem)
		}
		if len(inv.Instructions) > 0 {
			fmt.Fprintf(r.Out, "The instructions from %s are now the source's AGENTS.md; after setup, remove them from the original files so they do not load twice.\n", inv.Instructions[0].Path)
		}
		fmt.Fprintf(r.Out, "Review the copied files, then run: ocp setup --source %s\n", dir)
		return nil
	}
	if o.json {
		enc := json.NewEncoder(r.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(inv)
	}
	r.reportAdoption(inv, p.Home, haveSource)
	return nil
}

// adoptionConflicts plans, without writing, what setup would install into
// Claude Code and Codex from the new source, and reports what would stop it.
func adoptionConflicts(p ocp.Paths, dir string, harnesses []string) []string {
	doc, err := config.Load(filepath.Join(dir, config.FileName))
	if err != nil {
		return []string{err.Error()}
	}
	profiles, err := doc.ResolveFromDir(dir)
	if err != nil || len(profiles) == 0 {
		return nil
	}
	var out []string
	for _, h := range harnesses {
		if h == "opencode" {
			continue
		}
		rendered, err := ocp.PrepareNativeProfile(dir, profiles[0], h, "", nil)
		if err == nil {
			_, err = ocp.PlanNativeInstall(p, h, profiles[0].Name, rendered)
		}
		if err != nil {
			out = append(out, h+": "+err.Error())
		}
	}
	return out
}

// loadAdoptSource collects what the source declares, on every machine, so
// an item counts as "in source" even if this machine's overlay omits it.
func loadAdoptSource(s *ocp.State, out *ocp.AdoptSource) error {
	source, err := canonical(s.Source)
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
	for _, v := range ex.Profiles {
		profile := v.Profile
		for _, sk := range append(append([]config.Skill(nil), profile.Skills...), profile.DisabledSkills...) {
			out.Skills[sk.Name()] = true
		}
		for _, inst := range profile.Instructions {
			if b, err := os.ReadFile(filepath.Join(source, inst)); err == nil {
				out.Instructions[inst] = b
			}
		}
		for _, overlay := range append([]config.MachineOverlay{{Config: profile.Config}}, machineOverlays(profile)...) {
			if mcp, ok := overlay.Config["mcp"].(map[string]any); ok {
				for name, v := range mcp {
					if servers, ok := v.(map[string]any); ok && name == "servers" {
						for server := range servers {
							out.MCP[server] = true
						}
					} else if spec, ok := v.(map[string]any); ok && spec["type"] != nil {
						out.MCP[name] = true
					}
				}
			}
			for _, sk := range overlay.Skills {
				out.Skills[sk.Name()] = true
			}
		}
	}
	hosts, _ := filepath.Glob(filepath.Join(source, "hosts", "*.md"))
	for _, h := range hosts {
		if b, err := os.ReadFile(h); err == nil {
			out.Instructions[filepath.Join("hosts", filepath.Base(h))] = b
		}
	}
	return nil
}

func machineOverlays(p config.Profile) []config.MachineOverlay {
	names := make([]string, 0, len(p.Machines))
	for name := range p.Machines {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]config.MachineOverlay, 0, len(names))
	for _, name := range names {
		out = append(out, p.Machines[name])
	}
	return out
}

func (r *Runner) reportAdoption(inv ocp.Adoption, home string, haveSource bool) {
	short := func(path string) string {
		if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
		return path
	}
	heading := func(s string) { fmt.Fprintln(r.Out, r.c.Style(color.ANSIBold, s)) }
	status := func(it ocp.AdoptItem) string {
		switch {
		case it.InSource:
			return r.c.Style(color.ANSIDim, "in source")
		case !it.Same:
			return r.c.Style(color.ANSIYellow, "differs between harnesses")
		}
		return r.c.Style(color.ANSIGreen, "new")
	}
	list := func(items []ocp.AdoptItem, w io.Writer) {
		width := 0
		for _, it := range items {
			width = max(width, len(it.Name))
		}
		for _, it := range items {
			harnesses := make([]string, 0, len(it.Found))
			for h := range it.Found {
				harnesses = append(harnesses, h)
			}
			sort.Strings(harnesses)
			fmt.Fprintf(w, "  %-*s  %-22s %s\n", width, it.Name, strings.Join(harnesses, ", "), status(it))
		}
	}
	if len(inv.Instructions)+len(inv.Skills)+len(inv.MCP) == 0 {
		fmt.Fprintln(r.Out, "Nothing to adopt: the harnesses hold nothing outside OCP.")
		return
	}
	if len(inv.Instructions) > 0 {
		heading("Instructions")
		for _, in := range inv.Instructions {
			note := ""
			switch {
			case in.SameAs != "":
				note = "same as " + in.SameAs
			case in.CopyOf != "":
				note = r.c.Style(color.ANSIYellow, "older copy of "+in.CopyOf)
			}
			fmt.Fprintf(r.Out, "  %-8s  %-22s %3d lines  %s\n", in.Harness, short(in.Path), in.Lines, note)
		}
	}
	if len(inv.Skills) > 0 {
		heading("Skills")
		list(inv.Skills, r.Out)
	}
	if len(inv.MCP) > 0 {
		heading("MCP servers")
		list(inv.MCP, r.Out)
	}
	if len(inv.Secrets) > 0 {
		heading("Secrets")
		for _, s := range inv.Secrets {
			fmt.Fprintf(r.Out, "  %s would read {env:%s}\n", s.Where, s.Env)
		}
	}
	fmt.Fprintln(r.Out)
	if haveSource {
		fmt.Fprintln(r.Out, r.c.Style(color.ANSIDim, "Add what you want to keep to the source, then remove the hand-made copies; `ocp adopt --json` gives an agent with the OCP skill everything it needs."))
	} else {
		fmt.Fprintln(r.Out, r.c.Style(color.ANSIDim, "Create a source from this with: ocp adopt --write ~/.config/ocp"))
	}
}
