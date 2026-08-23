// Package cli implements the OCP command-line surface.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/importer"
	"github.com/nando/ocp/internal/ocp"
	"github.com/nando/ocp/internal/repository"
	"github.com/nando/ocp/internal/skills"
)

// ExitError preserves a child process's exit status for the command entry point.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("child exited with status %d", e.Code) }

// Runner dependencies are injectable so command behavior can be tested without a real home or OpenCode.
type Runner struct {
	In       io.Reader
	Out, Err io.Writer
	OpenCode string
	Paths    func() (ocp.Paths, error)
	Getwd    func() (string, error)
	Exec     func(string, []string, []string) error
}

func (r *Runner) defaults() {
	if r.In == nil {
		r.In = os.Stdin
	}
	if r.Out == nil {
		r.Out = os.Stdout
	}
	if r.Err == nil {
		r.Err = os.Stderr
	}
	if r.OpenCode == "" {
		r.OpenCode = "opencode"
	}
	if r.Paths == nil {
		r.Paths = ocp.DefaultPaths
	}
	if r.Getwd == nil {
		r.Getwd = os.Getwd
	}
	if r.Exec == nil {
		r.Exec = func(name string, args, env []string) error {
			c := exec.Command(name, args...)
			c.Stdin, c.Stdout, c.Stderr, c.Env = r.In, r.Out, r.Err, env
			return c.Run()
		}
	}
}

// Run dispatches a single OCP invocation.
func (r *Runner) Run(args []string) error {
	r.defaults()
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		r.help()
		return nil
	}
	p, err := r.Paths()
	if err != nil {
		return err
	}
	switch args[0] {
	case "setup":
		return r.setup(p, args[1:])
	case "apply":
		return r.apply(p, args[1:])
	case "sync":
		return r.sync(p, args[1:])
	case "use":
		return r.use(p, args[1:])
	case "run":
		return r.run(p, args[1:])
	case "list":
		return r.list(p, args[1:])
	case "status":
		return r.status(p, args[1:])
	case "reset":
		return r.reset(p, args[1:])
	case "import":
		return r.importConfig(p, args[1:])
	case "upgrade":
		fmt.Fprintln(r.Err, "upgrade is deferred and not implemented in this MVP")
		return &ExitError{Code: 2}
	default:
		return fmt.Errorf("unknown command %q (run 'ocp --help')", args[0])
	}
}
func (r *Runner) help() {
	fmt.Fprint(r.Out, "Usage: ocp <command>\n\nCommands: setup, sync, apply, use <profile>, run <profile> [args...], list, status, import [...], upgrade [skill <name>], reset\n")
}
func parse(name string, args []string, configure func(*flag.FlagSet)) error {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	configure(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("%s: unexpected arguments: %s", name, strings.Join(f.Args(), " "))
	}
	return nil
}
func lock(p ocp.Paths) (func() error, error) { return ocp.AcquireLock(p) }
func canonical(path string) (string, error) {
	a, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(a)
}
func sourceState(p ocp.Paths) (*ocp.State, error) {
	s, e := ocp.LoadState(p)
	if e != nil {
		return nil, e
	}
	if s == nil {
		return nil, errors.New("OCP is not set up; run 'ocp setup'")
	}
	return s, nil
}

func (r *Runner) setup(p ocp.Paths, args []string) error {
	wd, e := r.Getwd()
	if e != nil {
		return e
	}
	source, repo := wd, ""
	auto, force := true, false
	if e = parse("setup", args, func(f *flag.FlagSet) {
		f.StringVar(&source, "source", source, "source directory")
		f.StringVar(&repo, "repo", "", "repository URL")
		f.BoolVar(&auto, "no-auto-commit", false, "disable automatic commits")
		f.BoolVar(&force, "force", false, "overwrite generated drift")
	}); e != nil {
		return e
	}
	auto = !auto
	release, e := lock(p)
	if e != nil {
		return e
	}
	defer release()
	if s, e := ocp.LoadState(p); e != nil {
		return e
	} else if s != nil {
		return errors.New("OCP is already set up; use apply, sync, or reset first")
	}
	if repo != "" {
		if _, e := os.Stat(source); e == nil {
			entries, x := os.ReadDir(source)
			if x != nil {
				return x
			}
			if len(entries) != 0 {
				return fmt.Errorf("clone destination %s is not empty", source)
			}
		}
		clone := exec.Command("git", "clone", repo, source)
		clone.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if output, cloneErr := clone.CombinedOutput(); cloneErr != nil {
			return fmt.Errorf("clone repository %q: %w: %s", repo, cloneErr, strings.TrimSpace(string(output)))
		}
	}
	if e := os.MkdirAll(source, 0o700); e != nil {
		return e
	}
	file := filepath.Join(source, config.FileName)
	if _, e := os.Stat(file); errors.Is(e, os.ErrNotExist) {
		if e = os.WriteFile(file, []byte("version: 1\n"), 0o600); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	canonicalSource, e := canonical(source)
	if e != nil {
		return fmt.Errorf("canonical source: %w", e)
	}
	gs, e := gitSkills(canonicalSource)
	if e != nil {
		return e
	}
	_, lockChanged, e := skills.Prepare(canonicalSource, p.Data, gs, true)
	if e != nil {
		return e
	}
	if auto {
		repoResult, syncErr := repository.Sync(canonicalSource, true, "ocp: initialize configuration")
		if syncErr != nil {
			return fmt.Errorf("initialize repository: %w", syncErr)
		}
		r.reportRepository(repoResult)
	}
	if lockChanged {
		fmt.Fprintln(r.Out, "Initialized Git skill lock")
	}
	return r.install(p, source, auto, true, force, true)
}

func gitSkills(source string) ([]config.Skill, error) {
	d, e := config.Load(filepath.Join(source, config.FileName))
	if e != nil {
		return nil, e
	}
	ps, e := config.Resolve(d)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	var out []config.Skill
	for _, p := range ps {
		for _, s := range p.Skills {
			if strings.HasPrefix(s.Source, "http://") || strings.HasPrefix(s.Source, "https://") || strings.HasPrefix(s.Source, "git@") || strings.HasPrefix(s.Source, "ssh://") {
				k := s.Source + "\x00" + s.Ref
				if !seen[k] {
					seen[k] = true
					out = append(out, s)
				}
			}
		}
	}
	return out, nil
}
func profileNames(source string) ([]string, error) {
	d, e := config.Load(filepath.Join(source, config.FileName))
	if e != nil {
		return nil, e
	}
	ps, e := config.Resolve(d)
	if e != nil {
		return nil, e
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out, nil
}
func (r *Runner) install(p ocp.Paths, source string, auto, bootstrap, force, takeover bool) error {
	source, e := canonical(source)
	if e != nil {
		return fmt.Errorf("canonical source: %w", e)
	}
	gs, e := gitSkills(source)
	if e != nil {
		return e
	}
	provider, _, e := skills.Prepare(source, p.Data, gs, bootstrap)
	if e != nil {
		return e
	}
	result, e := ocp.Render(ocp.RenderOptions{Paths: p, Source: source, Force: force, SkillProvider: provider})
	if e != nil {
		return e
	}
	active := ""
	if takeover {
		names := result.Profiles
		active = names[0]
		for _, n := range names {
			if n == "default" {
				active = n
				break
			}
		}
		if e := ocp.TakeOver(p, active, ocp.State{Source: source, AutoCommit: auto}); e != nil {
			return e
		}
	} else {
		active, _ = ocp.ActiveProfile(p)
	}
	fmt.Fprintf(r.Out, "Source: %s\nProfiles: %s\n", source, strings.Join(result.Profiles, ", "))
	if active != "" {
		fmt.Fprintf(r.Out, "Active profile: %s\n", active)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(r.Out, "Warning: %s\n", w)
	}
	if result.Fallback.FellBack {
		fmt.Fprintf(r.Out, "Fell back to profile: %s\n", result.Fallback.Active)
	}
	if result.Fallback.NoActive && !takeover {
		fmt.Fprintln(r.Out, "No active profile")
	}
	return nil
}
func (r *Runner) apply(p ocp.Paths, args []string) error {
	force := false
	if e := parse("apply", args, func(f *flag.FlagSet) { f.BoolVar(&force, "force", false, "overwrite generated drift") }); e != nil {
		return e
	}
	rel, e := lock(p)
	if e != nil {
		return e
	}
	defer rel()
	s, e := sourceState(p)
	if e != nil {
		return e
	}
	e = r.install(p, s.Source, s.AutoCommit, false, force, false)
	if d := new(ocp.DriftError); errors.As(e, &d) && !force {
		return r.confirmDrift(p, s, d, "apply")
	}
	return e
}
func (r *Runner) confirmDrift(p ocp.Paths, s *ocp.State, d *ocp.DriftError, command string) error {
	if !r.tty() {
		return fmt.Errorf("%s refused: generated files have drifted (%s); rerun with --force", command, strings.Join(d.Paths, ", "))
	}
	fmt.Fprintf(r.Out, "Generated files will be overwritten:\n  %s\nContinue? [y/N] ", strings.Join(d.Paths, "\n  "))
	if r.confirm() {
		return r.install(p, s.Source, s.AutoCommit, false, true, false)
	}
	return errors.New("cancelled")
}
func (r *Runner) sync(p ocp.Paths, args []string) error {
	force := false
	if e := parse("sync", args, func(f *flag.FlagSet) { f.BoolVar(&force, "force", false, "overwrite generated drift") }); e != nil {
		return e
	}
	rel, e := lock(p)
	if e != nil {
		return e
	}
	defer rel()
	s, e := sourceState(p)
	if e != nil {
		return e
	}
	rr, e := repository.Sync(s.Source, s.AutoCommit, "ocp: synchronize configuration")
	if e != nil {
		return fmt.Errorf("sync repository: %w", e)
	}
	r.reportRepository(rr)
	source, e := canonical(s.Source)
	if e != nil {
		return e
	}
	gs, e := gitSkills(source)
	if e != nil {
		return e
	}
	provider, changed, e := skills.Prepare(source, p.Data, gs, s.AutoCommit || rr.NoGit)
	if e != nil {
		return e
	}
	if changed && !rr.NoGit {
		if !s.AutoCommit {
			return errors.New("Git skill lock changed but auto-commit is disabled; commit ocp.lock manually")
		}
		lockResult, e := repository.Sync(source, true, "ocp: lock skills")
		if e != nil {
			return fmt.Errorf("commit skill lock: %w", e)
		}
		r.reportRepository(lockResult)
	}
	result, e := ocp.Render(ocp.RenderOptions{Paths: p, Source: source, Force: force, SkillProvider: provider})
	if d := new(ocp.DriftError); errors.As(e, &d) && !force {
		return r.confirmDrift(p, s, d, "sync")
	}
	if e != nil {
		return e
	}
	fmt.Fprintf(r.Out, "Profiles: %s\n", strings.Join(result.Profiles, ", "))
	for _, w := range result.Warnings {
		fmt.Fprintf(r.Out, "Warning: %s\n", w)
	}
	if result.Fallback.FellBack {
		fmt.Fprintf(r.Out, "Fell back to profile: %s\n", result.Fallback.Active)
	}
	if result.Fallback.NoActive {
		fmt.Fprintln(r.Out, "No active profile")
	}
	return nil
}

func (r *Runner) reportRepository(result repository.Result) {
	if result.NoGit {
		fmt.Fprintln(r.Out, "Repository: local-only (not a Git worktree)")
		return
	}
	actions := make([]string, 0, 4)
	if result.Committed {
		actions = append(actions, "committed local changes")
	}
	if result.Fetched {
		actions = append(actions, "received remote changes")
	}
	if result.Merged {
		actions = append(actions, "merged divergent history")
	}
	if result.Pushed {
		actions = append(actions, "pushed local changes")
	}
	if len(actions) == 0 {
		fmt.Fprintln(r.Out, "Repository: current")
		return
	}
	fmt.Fprintf(r.Out, "Repository: %s\n", strings.Join(actions, ", "))
}
func (r *Runner) use(p ocp.Paths, args []string) error {
	if len(args) != 1 {
		return errors.New("use requires exactly one profile")
	}
	rel, e := lock(p)
	if e != nil {
		return e
	}
	defer rel()
	if _, e := sourceState(p); e != nil {
		return e
	}
	if e := ocp.Activate(p, args[0]); e != nil {
		return e
	}
	fmt.Fprintf(r.Out, "Active profile: %s\n", args[0])
	return nil
}
func (r *Runner) run(p ocp.Paths, args []string) error {
	if len(args) < 1 {
		return errors.New("run requires a profile")
	}
	if _, e := sourceState(p); e != nil {
		return e
	}
	dir, e := ocp.ConfigPath(p, args[0])
	if e != nil {
		return e
	}
	abs, e := filepath.Abs(dir)
	if e != nil {
		return e
	}
	env := setEnv(os.Environ(), "XDG_CONFIG_HOME", ocp.OneOffConfigHome(p))
	env = setEnv(env, "OPENCODE_CONFIG_DIR", abs)
	if e = r.Exec(r.OpenCode, args[1:], env); e != nil {
		var x *exec.ExitError
		if errors.As(e, &x) {
			return &ExitError{Code: x.ExitCode()}
		}
		var coded interface{ ExitCode() int }
		if errors.As(e, &coded) {
			return &ExitError{Code: coded.ExitCode()}
		}
		return fmt.Errorf("run %s: %w", r.OpenCode, e)
	}
	return nil
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}
func generated(p ocp.Paths) ([]string, error) {
	target, e := filepath.EvalSymlinks(p.Current)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	es, e := os.ReadDir(target)
	if e != nil {
		return nil, e
	}
	var out []string
	for _, x := range es {
		if x.IsDir() {
			out = append(out, x.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
func (r *Runner) list(p ocp.Paths, args []string) error {
	if e := parse("list", args, func(*flag.FlagSet) {}); e != nil {
		return e
	}
	names, e := generated(p)
	if e != nil {
		return e
	}
	active, _ := ocp.ActiveProfile(p)
	for _, n := range names {
		mark := " "
		if n == active {
			mark = "*"
		}
		fmt.Fprintf(r.Out, "%s %s\n", mark, n)
	}
	return nil
}
func (r *Runner) status(p ocp.Paths, args []string) error {
	if e := parse("status", args, func(*flag.FlagSet) {}); e != nil {
		return e
	}
	s, e := sourceState(p)
	if e != nil {
		return e
	}
	source, e := canonical(s.Source)
	if e != nil {
		return e
	}
	names, e := generated(p)
	if e != nil {
		return e
	}
	active, _ := ocp.ActiveProfile(p)
	if active == "" {
		active = "none"
	}
	fmt.Fprintf(r.Out, "Source: %s\nAuto-commit: %t\nActive profile: %s\nGenerated profiles: %s\n", source, s.AutoCommit, active, strings.Join(names, ", "))
	return nil
}
func (r *Runner) reset(p ocp.Paths, args []string) error {
	force := false
	if e := parse("reset", args, func(f *flag.FlagSet) { f.BoolVar(&force, "force", false, "replace user-owned OpenCode config") }); e != nil {
		return e
	}
	rel, e := lock(p)
	if e != nil {
		return e
	}
	defer rel()
	source, e := ocp.Reset(p, force)
	if e != nil && strings.Contains(e.Error(), "user-owned") && !force && r.tty() {
		fmt.Fprint(r.Out, "A user-owned OpenCode configuration will be replaced. Continue? [y/N] ")
		if r.confirm() {
			source, e = ocp.Reset(p, true)
		} else {
			return errors.New("cancelled")
		}
	}
	if e != nil {
		return e
	}
	fmt.Fprintf(r.Out, "OCP detached. Canonical source remains: %s\nDelete that directory manually to remove it.\n", source)
	return nil
}
func (r *Runner) tty() bool {
	f, ok := r.In.(*os.File)
	if !ok {
		return false
	}
	i, e := f.Stat()
	return e == nil && i.Mode()&os.ModeCharDevice != 0
}
func (r *Runner) confirm() bool {
	line, _ := bufio.NewReader(r.In).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
func (r *Runner) importConfig(p ocp.Paths, args []string) error {
	wd, e := r.Getwd()
	if e != nil {
		return e
	}
	source := wd
	force := false
	f := flag.NewFlagSet("import", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&source, "source", source, "source directory")
	f.BoolVar(&force, "force", false, "overwrite target files")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() > 1 {
		return errors.New("import accepts at most one input path")
	}
	input := p.OpenCode
	if f.NArg() == 1 {
		input = f.Arg(0)
	}
	rel, e := lock(p)
	if e != nil {
		return e
	}
	defer rel()
	if e := importer.Import(input, source, force); e != nil {
		if !force && strings.Contains(e.Error(), "refusing to overwrite") && r.tty() {
			fmt.Fprint(r.Out, "Existing source files will be overwritten. Continue? [y/N] ")
			if r.confirm() {
				if retry := importer.Import(input, source, true); retry == nil {
					fmt.Fprintf(r.Out, "Imported OpenCode configuration into %s\n", source)
					return nil
				} else {
					return retry
				}
			}
			return errors.New("cancelled")
		}
		return e
	}
	fmt.Fprintf(r.Out, "Imported OpenCode configuration into %s\n", source)
	return nil
}
