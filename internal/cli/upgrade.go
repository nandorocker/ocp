package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/nando/ocp/internal/ocp"
	"github.com/nando/ocp/internal/skills"
)

type upgradeOptions struct {
	preview bool
	force   bool
}

func configureUpgrade(f *flag.FlagSet, o *upgradeOptions) {
	f.BoolVar(&o.preview, "preview", false, "show what would change without writing anything")
	f.BoolVar(&o.force, "force", false, "overwrite generated drift when re-rendering")
}

// parseUpgrade accepts flags before or after the optional `skill <name>`.
func parseUpgrade(args []string, o *upgradeOptions) (string, error) {
	f := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configureUpgrade(f, o)
	var positional []string
	for {
		if err := f.Parse(args); err != nil {
			return "", commandUsageError("upgrade", err)
		}
		if f.NArg() == 0 {
			break
		}
		positional = append(positional, f.Arg(0))
		args = f.Args()[1:]
	}
	switch {
	case len(positional) == 0:
		return "", nil
	case len(positional) == 2 && positional[0] == "skill" && positional[1] != "":
		return positional[1], nil
	case len(positional) == 1 && positional[0] == "skill":
		return "", commandUsageError("upgrade", errors.New("upgrade skill requires a skill name"))
	}
	return "", commandUsageError("upgrade", fmt.Errorf("upgrade: unexpected arguments: %s", strings.Join(positional, " ")))
}

func (r *Runner) upgrade(p ocp.Paths, args []string) error {
	var o upgradeOptions
	name, err := parseUpgrade(args, &o)
	if err != nil {
		return err
	}
	release, err := lock(p)
	if err != nil {
		return err
	}
	defer release()
	state, err := sourceState(p)
	if err != nil {
		return err
	}
	source, err := canonical(state.Source)
	if err != nil {
		return err
	}
	gs, err := gitSkills(source)
	if err != nil {
		return err
	}
	plan, err := skills.PlanUpgrade(source, p.Data, gs, name)
	if err != nil {
		return err
	}
	if len(plan.Results) == 0 {
		r.c.Muted("No Git skills to upgrade")
		return nil
	}
	r.reportUpgrade(plan.Results)
	if o.preview {
		r.c.Muted("Preview only; nothing was written")
		return nil
	}
	if !plan.Changed() {
		return nil
	}
	if err := plan.Fetch(); err != nil {
		return fmt.Errorf("upgrade: %w; ocp.lock was not changed", err)
	}
	if err := plan.Write(); err != nil {
		return err
	}
	res, err := r.install(p, source, state.AutoCommit, false, o.force, false, state.Machine)
	if err != nil {
		if restoreErr := plan.Restore(); restoreErr != nil {
			return fmt.Errorf("upgrade: %w; restoring ocp.lock also failed: %v", err, restoreErr)
		}
		if d := new(ocp.DriftError); errors.As(err, &d) {
			return fmt.Errorf("upgrade refused: generated files have drifted (%s); ocp.lock was not changed; rerun with --force", strings.Join(d.Paths, ", "))
		}
		return fmt.Errorf("upgrade: %w; ocp.lock was not changed", err)
	}
	fmt.Fprintf(r.Out, "Applied profile: %s\n", res.Active)
	for _, w := range res.Warnings {
		r.c.Warning(w)
	}
	if state.AutoCommit {
		r.c.Muted("Run `ocp sync` to commit ocp.lock and share it with your other machines")
	} else {
		r.c.Muted("Commit ocp.lock in your source to share it with your other machines")
	}
	return nil
}

func (r *Runner) reportUpgrade(results []skills.UpgradeResult) {
	width := 0
	for _, res := range results {
		width = max(width, len(res.Name))
	}
	arrow := "->"
	if r.c.Styled() {
		arrow = "→"
	}
	for _, res := range results {
		label := fmt.Sprintf("%-*s  ", width, res.Name)
		switch res.Status {
		case skills.Advanced:
			r.c.Printf("%s%s %s %s", label, short(res.Old), arrow, short(res.New))
		case skills.Current:
			r.c.Muted(label + "up to date")
		case skills.Pinned:
			r.c.Muted(fmt.Sprintf("%spinned at %s", label, res.Ref))
		case skills.Skipped:
			r.c.Warning(label + "skipped: " + res.Note)
		}
	}
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
