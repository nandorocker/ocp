package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/ocp"
	"github.com/nando/ocp/internal/skills"
)

type harnessOptions struct {
	targets                 harnessFlags
	force, preview, recover bool
}

type harnessFlags []string

func (h *harnessFlags) String() string { return strings.Join(*h, ",") }
func (h *harnessFlags) Set(value string) error {
	if value != "opencode" && value != "claude" && value != "codex" {
		return fmt.Errorf("unknown harness %q", value)
	}
	for _, old := range *h {
		if old == value {
			return fmt.Errorf("duplicate harness %q", value)
		}
	}
	*h = append(*h, value)
	return nil
}

func configureHarness(f *flag.FlagSet, o *harnessOptions) {
	f.Var(&o.targets, "harness", "target harness: opencode, claude, codex (repeatable)")
	f.BoolVar(&o.preview, "preview", false, "preview compatibility and native file changes without writing")
}

func parseHarness(name string, args []string, use bool) (harnessOptions, string, error) {
	var o harnessOptions
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configureHarness(f, &o)
	f.BoolVar(&o.force, "force", false, "overwrite generated OpenCode drift (never native ownership conflicts)")
	if name == "apply" {
		f.BoolVar(&o.recover, "recover", false, "resume an interrupted native update; requires --harness")
	}
	if use && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(append([]string(nil), args[1:]...), args[0])
	}
	if err := f.Parse(args); err != nil {
		return o, "", commandUsageError(name, err)
	}
	if use {
		if f.NArg() != 1 {
			return o, "", commandUsageError(name, errors.New("use requires exactly one profile"))
		}
		return o, f.Arg(0), nil
	}
	if f.NArg() != 0 {
		return o, "", commandUsageError(name, errors.New("unexpected arguments"))
	}
	return o, "", nil
}

func sourceDocument(s *ocp.State) (*config.Document, error) {
	return config.Load(filepath.Join(s.Source, config.FileName))
}

func multiHarness(d *config.Document, o harnessOptions) bool {
	return d.Harnesses != nil || len(o.targets) > 0 || o.preview || o.recover
}

// refreshHarnesses resolves and preflights all selected targets before native
// writes. Explicit target incompatibility aborts the entire invocation.
func (r *Runner) refreshHarnesses(p ocp.Paths, s *ocp.State, d *config.Document, o harnessOptions, wanted string, bootstrap bool) error {
	targets := d.EnabledHarnesses()
	explicit := len(o.targets) > 0
	if explicit {
		targets = append([]string(nil), o.targets...)
	}
	if err := nativeEnvironment(targets); err != nil {
		return err
	}
	if o.recover {
		return r.recoverHarnesses(p, o)
	}
	profiles, err := d.ResolveFromDirForMachine(s.Source, s.Machine)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("configuration resolves no profiles")
	}
	byName := map[string]config.Profile{}
	for _, profile := range profiles {
		byName[profile.Name] = profile
	}
	if wanted != "" {
		if _, ok := byName[wanted]; !ok {
			return fmt.Errorf("unknown profile %q", wanted)
		}
	}
	type selection struct {
		harness  string
		profile  config.Profile
		rendered ocp.NativeProfile
		plan     ocp.NativeInstallPlan
		openCode *ocp.OpenCodeInstallPlan
		skip     bool
	}
	var selected []selection
	var skipped []string
	compatible := func(profile config.Profile, h string) (ocp.NativeProfile, error) {
		if h == "opencode" {
			return ocp.NativeProfile{}, ocp.ValidateOpenCodeProfile(s.Source, profile)
		}
		// Check model/settings compatibility before Git skill materialization.
		profile.Skills = nil
		return ocp.PrepareNativeProfile(s.Source, profile, h, s.Machine, nil)
	}
	for _, h := range targets {
		name := wanted
		if name == "" {
			if h == "opencode" {
				name, err = ocp.ActiveProfile(p)
			} else {
				name, err = ocp.NativeActiveProfile(p, h)
			}
			if err != nil {
				return err
			}
		}
		if name == "" {
			if _, ok := byName["default"]; ok {
				name = "default"
			} else {
				for _, profile := range profiles {
					if _, err := compatible(profile, h); err == nil {
						name = profile.Name
						break
					}
				}
				if name == "" {
					name = profiles[0].Name
				}
			}
		}
		profile, exists := byName[name]
		var rendered ocp.NativeProfile
		if !exists {
			err = fmt.Errorf("selected profile %q is no longer available; select another with ocp use", name)
		} else {
			rendered, err = compatible(profile, h)
		}
		if err != nil {
			reason := fmt.Sprintf("%s: skipped — profile %q: %v; unchanged", h, name, err)
			if explicit {
				return errors.New(reason)
			}
			skipped = append(skipped, reason)
			continue
		}
		selected = append(selected, selection{harness: h, profile: profile, rendered: rendered})
	}
	for _, message := range skipped {
		fmt.Fprintln(r.Out, message)
	}
	if len(selected) == 0 {
		return errors.New("no compatible harnesses to update")
	}
	// Prepare only selected profiles' skills, never mutate canonical lock state in
	// preview mode. Git checkouts are populated only after compatibility preflight.
	var gitSkills []config.Skill
	for _, item := range selected {
		gitSkills = append(append(gitSkills, item.profile.Skills...), item.profile.DisabledSkills...)
		if item.harness == "opencode" {
			// Preserve the existing generated-profile/run surface, not just the
			// selected profile, when explicitly updating OpenCode.
			for _, profile := range profiles {
				if ocp.ValidateOpenCodeProfile(s.Source, profile) == nil {
					gitSkills = append(gitSkills, profile.Skills...)
				}
			}
		}
	}
	provider, _, err := skills.Prepare(s.Source, p.Data, gitSkills, bootstrap && !o.preview)
	if err != nil {
		return err
	}
	if o.preview {
		provider, err = skills.Cached(s.Source, p.Data, gitSkills)
		if err != nil {
			return err
		}
	}
	for i := range selected {
		item := &selected[i]
		if item.harness == "opencode" {
			item.openCode, err = ocp.PrepareOpenCodeInstall(p, s.Source, item.profile, provider, *s, o.force)
			if err != nil {
				return err
			}
			defer item.openCode.Close()
			continue
		}
		item.rendered, err = ocp.PrepareNativeProfile(s.Source, item.profile, item.harness, s.Machine, provider)
		if err != nil {
			if explicit {
				return fmt.Errorf("%s: %w", item.harness, err)
			}
			fmt.Fprintf(r.Out, "%s: skipped — profile %q: %v; unchanged\n", item.harness, item.profile.Name, err)
			item.skip = true
			continue
		}
		item.plan, err = ocp.PlanNativeInstall(p, item.harness, item.profile.Name, item.rendered)
		if err != nil {
			return fmt.Errorf("%s: %w", item.harness, err)
		}
	}
	available := 0
	for _, item := range selected {
		if !item.skip {
			available++
		}
	}
	if available == 0 {
		return errors.New("no compatible harnesses to update")
	}
	if bootstrap && !o.preview {
		// Register the source before the first native publication so an
		// interrupted first-time setup can use the ordinary recovery command.
		if existing, err := ocp.LoadState(p); err != nil {
			return err
		} else if existing == nil {
			if err := ocp.SaveState(p, *s); err != nil {
				return err
			}
		}
	}
	for _, item := range selected {
		if item.skip {
			continue
		}
		if o.preview {
			fmt.Fprintf(r.Out, "%s: would apply %s\n", item.harness, item.profile.Name)
			for _, change := range item.plan.Changes {
				fmt.Fprintf(r.Out, "  %s\n", change.Key)
			}
			for _, op := range item.plan.MCPChanges() {
				fmt.Fprintf(r.Out, "  mcp %s\n", op)
			}
			for _, warning := range item.rendered.Warnings {
				r.c.Warning(warning)
			}
			continue
		}
		if item.harness == "opencode" {
			err = ocp.ApplyOpenCodeInstall(item.openCode)
		} else {
			err = ocp.ApplyNativePlan(p, item.plan)
		}
		if err != nil {
			return fmt.Errorf("%s update failed (earlier targets may already be updated): %w", item.harness, err)
		}
		fmt.Fprintf(r.Out, "%s: active profile %s\n", item.harness, item.profile.Name)
		for _, warning := range item.rendered.Warnings {
			r.c.Warning(warning)
		}
	}
	if !o.preview {
		skills.PruneCache(s.Source, p.Data)
	}
	return nil
}

func (r *Runner) recoverHarnesses(p ocp.Paths, o harnessOptions) error {
	if len(o.targets) == 0 || o.preview {
		return errors.New("--recover requires explicit --harness targets and cannot be combined with --preview")
	}
	if err := nativeEnvironment(o.targets); err != nil {
		return err
	}
	for _, h := range o.targets {
		if h == "opencode" {
			return errors.New("--recover is for Claude/Codex native updates")
		}
	}
	for _, h := range o.targets {
		if err := ocp.RecoverNative(p, h); err != nil {
			return err
		}
		fmt.Fprintf(r.Out, "%s: recovery complete\n", h)
	}
	return nil
}

func (r *Runner) nativeSelections(p ocp.Paths) error {
	for _, h := range []string{"claude", "codex"} {
		name, err := ocp.NativeActiveProfile(p, h)
		if err != nil {
			return err
		}
		if name != "" {
			fmt.Fprintf(r.Out, "%s: %s\n", h, name)
		}
	}
	return nil
}

func (r *Runner) resetHarnesses(p ocp.Paths, targets []string, preview bool) error {
	var plans []ocp.NativeInstallPlan
	for _, h := range targets {
		if h == "opencode" {
			continue
		}
		name, err := ocp.NativeActiveProfile(p, h)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		plan, err := ocp.PlanNativeReset(p, h)
		if err != nil {
			return err
		}
		plans = append(plans, plan)
	}
	for _, plan := range plans {
		if preview {
			fmt.Fprintf(r.Out, "%s: would detach\n", plan.Harness)
			continue
		}
		if err := ocp.ApplyNativePlan(p, plan); err != nil {
			return err
		}
		fmt.Fprintf(r.Out, "%s: detached\n", plan.Harness)
	}
	return nil
}

func (r *Runner) harnessStatus(p ocp.Paths, s *ocp.State, doc *config.Document) error {
	fmt.Fprintf(r.Out, "Source: %s\nAuto-commit: %t\n", s.Source, s.AutoCommit)
	if s.Machine != "" {
		fmt.Fprintf(r.Out, "Machine: %s\n", s.Machine)
	}
	enabled := map[string]bool{}
	for _, h := range doc.EnabledHarnesses() {
		enabled[h] = true
	}
	for _, h := range []string{"opencode", "claude", "codex"} {
		var name string
		var err error
		if h == "opencode" {
			name, err = ocp.ActiveProfile(p)
		} else {
			name, err = ocp.NativeActiveProfile(p, h)
		}
		if err != nil {
			return err
		}
		if !enabled[h] && name == "" {
			continue
		}
		if name == "" {
			name = "none"
		}
		if !enabled[h] {
			name += " (disabled; retained)"
		}
		fmt.Fprintf(r.Out, "%s: %s\n", h, name)
	}
	profiles, err := doc.ResolveFromDirForMachine(s.Source, s.Machine)
	if err != nil {
		return err
	}
	var names []string
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	fmt.Fprintf(r.Out, "Profiles: %s\n", strings.Join(names, ", "))
	return nil
}

// The native harness APIs intentionally do not redirect these homes. Respect
// an explicit user override by refusing to install into an unused default path.
func nativeEnvironment(targets []string) error {
	for _, h := range targets {
		key := ""
		if h == "claude" {
			key = "CLAUDE_CONFIG_DIR"
		}
		if h == "codex" {
			key = "CODEX_HOME"
		}
		if key != "" && os.Getenv(key) != "" {
			return fmt.Errorf("%s is set; custom native homes are not supported yet (no files changed)", key)
		}
	}
	return nil
}
