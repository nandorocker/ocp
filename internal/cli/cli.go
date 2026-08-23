// Package cli implements the OCP command-line surface.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nando/ocp/internal/color"
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
	c        *color.Writer
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
	if r.c == nil {
		r.c = color.New(r.Out, r.Err)
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

// checkGitAvailable runs git --version with a short timeout and reports the result.
func (r *Runner) checkGitAvailable() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--version")
	out, err := cmd.Output()
	if err != nil {
		r.c.CheckErr("git not found or failed to run: " + err.Error())
		return ""
	}
	r.c.Check("git available — " + strings.TrimSpace(string(out)))
	return strings.TrimSpace(string(out))
}

// checkRepoAccess runs git ls-remote with a timeout to verify reachability and authentication.
func (r *Runner) checkRepoAccess(repoURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", repoURL)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			r.c.CheckWarn("repository access timed out")
			return errors.New("repository access timed out")
		}
		r.c.CheckErr("cannot reach repository: " + repoURL)
		return fmt.Errorf("cannot reach repository %q: %w", repoURL, err)
	}
	r.c.Check("repository reachable")
	return nil
}

func (r *Runner) setup(p ocp.Paths, args []string) error {
	if s, e := ocp.LoadState(p); e != nil {
		return e
	} else if s != nil {
		return errors.New("OCP is already set up; use apply, sync, or reset first")
	}
	autoFlag := flag.NewFlagSet("setup", flag.ContinueOnError)
	autoFlag.SetOutput(io.Discard)
	noAuto := autoFlag.Bool("no-auto-commit", false, "disable automatic commits")
	force := autoFlag.Bool("force", false, "overwrite generated drift")
	src := autoFlag.String("source", ".", "source directory")
	repo := autoFlag.String("repo", "", "repository URL")
	if err := autoFlag.Parse(args); err != nil {
		return err
	}
	flagArgs := autoFlag.Args()
	hasFlags := len(flagArgs) > 0 || *repo != "" || !r.tty() || *src != "."
	if hasFlags {
		return r.setupNonInteractive(p, *src, *repo, *noAuto, *force)
	}
	source, sourceExplicit := "", false
	repoVar, repoExplicit := "", false
	autoCommit := true
	fmt.Fprintln(r.Out)
	fmt.Fprintln(r.Out, "Welcome to OCP — a declarative configuration manager for OpenCode.")
	fmt.Fprintln(r.Out)
	r.checkGitAvailable()
	hasOPM := importer.HasOPMProfiles()
	method, e := r.promptMenuDynamic(r.Out, "How would you like to begin?", hasOPM)
	if e != nil {
		return e
	}
	activeProfile := ""
	switch method {
	case 0:
		e = r.setupNew(p, &source, &sourceExplicit, &repoVar, &repoExplicit, &autoCommit, force)
	case 1:
		e = r.setupImportOpenCode(p, &source, &sourceExplicit, &autoCommit, force)
	case 2:
		if hasOPM {
			e = r.setupImportOPM(p, &source, &autoCommit, force)
		} else {
			e = r.setupExistingRepo(p, &source, &sourceExplicit, &repoVar, &repoExplicit, &autoCommit, force)
		}
	case 3:
		if !hasOPM {
			return fmt.Errorf("invalid selection")
		}
		e = r.setupExistingRepo(p, &source, &sourceExplicit, &repoVar, &repoExplicit, &autoCommit, force)
	default:
		return fmt.Errorf("invalid selection")
	}
	if e != nil {
		return e
	}
	profiles, e := profileNames(source)
	if e != nil {
		return e
	}
	if selected, selErr := r.selectAllFirstOnly(p, profiles); selErr == nil && selected != "" {
		activeProfile = selected
	} else if selErr != nil && !strings.Contains(selErr.Error(), "EOF") {
		return selErr
	}
	return r.setupRun(p, source, sourceExplicit, repoVar, repoExplicit, autoCommit, *force, activeProfile)
}

func (r *Runner) setupNew(p ocp.Paths, source *string, sourceExplicit *bool, repo *string, repoExplicit *bool, autoCommit *bool, force *bool) error {
	*source = "."
	*sourceExplicit = true
	mode, e := r.promptMenu(r.Out, "Repository type?", []string{
		"Local only (no Git)",
		"Back it with a Git repository",
	})
	if e != nil {
		return e
	}
	if mode == 1 {
		rp, e := r.promptText(r.Out, "Git URL or SSH path (leave blank for local init)?")
		if e != nil {
			return e
		}
		if rp != "" {
			*repo = rp
			*repoExplicit = true
		} else {
			*autoCommit = true
		}
	}
	return r.setupRun(p, *source, *sourceExplicit, *repo, *repoExplicit, *autoCommit, *force, "")
}

func (r *Runner) setupImportOpenCode(p ocp.Paths, source *string, sourceExplicit *bool, autoCommit *bool, force *bool) error {
	fmt.Fprintln(r.Out)
	fmt.Fprint(r.Out, "Default OpenCode config directory detected:")
	def := filepath.Join(os.Getenv("HOME"), ".config", "opencode")
	fmt.Fprintf(r.Out, "  %s\n\n", def)
	importPath, e := r.promptTextWithDefault(r.Out, "Input path (blank to skip)", def)
	if e != nil {
		return e
	}
	if importPath == "" {
		fmt.Fprintln(r.Out, "Skipping import.")
		*source = "."
		*sourceExplicit = true
		return r.setupRun(p, *source, *sourceExplicit, "", false, *autoCommit, *force, "")
	}
	*source = "."
	*sourceExplicit = true
	if e := importer.Import(importPath, *source, *force); e != nil {
		return fmt.Errorf("import: %w", e)
	}
	fmt.Fprintln(r.Out, "Imported OpenCode configuration.")
	return r.setupRun(p, *source, *sourceExplicit, "", false, *autoCommit, *force, "")
}

func (r *Runner) setupExistingRepo(p ocp.Paths, source *string, sourceExplicit *bool, repo *string, repoExplicit *bool, autoCommit *bool, force *bool) error {
	cloneDest := filepath.Join(os.Getenv("HOME"), ".config", "opencode-config")
	url, e := r.promptText(r.Out, "Git repository URL or path:")
	if e != nil {
		return e
	}
	if url == "" {
		return errors.New("a repository URL or path is required")
	}
	*repo = url
	*repoExplicit = true
	dest, e := r.promptTextWithDefault(r.Out, "Clone destination (blank to skip)", cloneDest)
	if e != nil {
		return e
	}
	if dest != "" {
		*source = dest
		*sourceExplicit = true
	}
	return r.setupRun(p, *source, *sourceExplicit, *repo, *repoExplicit, true, *force, "")
}

func (r *Runner) setupRun(p ocp.Paths, source string, sourceExplicit bool, repo string, repoExplicit bool, autoCommit bool, force bool, activeProfile string) error {
	if sourceExplicit && !filepath.IsAbs(source) {
		var err error
		source, err = canonical(source)
		if err != nil {
			return fmt.Errorf("canonical source: %w", err)
		}
	} else if !sourceExplicit {
		wd, err := r.Getwd()
		if err != nil {
			return err
		}
		source = wd
	}
	if repoExplicit && repo != "" {
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
		r.c.Print("Cloned " + repo)
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
	if e := r.install(p, source, autoCommit, true, force, true); e != nil {
		return e
	}
	if activeProfile != "" {
		if e := ocp.Activate(p, activeProfile); e != nil {
			return e
		}
	}
	return nil
}

func (r *Runner) setupNonInteractive(p ocp.Paths, source string, repo string, noAuto bool, force bool) error {
	auto := !noAuto
	if !filepath.IsAbs(source) {
		source = filepath.Clean(source)
	}
	return r.setupRun(p, source, true, repo, repo != "", auto, force, "")
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
	r.c.Print("Active profile: " + active)
	for _, w := range result.Warnings {
		r.c.Warning(w)
	}
	if result.Fallback.FellBack {
		r.c.Important("Fell back to profile: " + result.Fallback.Active)
	}
	if result.Fallback.NoActive && !takeover {
		r.c.Muted("No active profile")
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
	r.c.Printf("Profiles: %s", strings.Join(result.Profiles, ", "))
	for _, w := range result.Warnings {
		r.c.Warning(w)
	}
	if result.Fallback.FellBack {
		r.c.Important("Fell back to profile: " + result.Fallback.Active)
	}
	if result.Fallback.NoActive {
		r.c.Muted("No active profile")
	}
	return nil
}

func (r *Runner) reportRepository(res repository.Result) {
	if res.NoGit {
		r.c.Muted("Repository: local-only (not a Git worktree)")
		return
	}
	actions := make([]string, 0, 4)
	if res.Committed {
		actions = append(actions, "committed local changes")
	}
	if res.Fetched {
		actions = append(actions, "received remote changes")
	}
	if res.Merged {
		actions = append(actions, "merged divergent history")
	}
	if res.Pushed {
		actions = append(actions, "pushed local changes")
	}
	if len(actions) == 0 {
		r.c.Muted("Repository: current")
		return
	}
	r.c.Printf("Repository: %s", strings.Join(actions, ", "))
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
	r.c.Success("Active profile: " + args[0])
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
	fmt.Fprintf(r.Out, "Source: %s\n", source)
	r.c.Printf("Auto-commit: %t", s.AutoCommit)
	if active == "none" {
		r.c.Muted("Active profile: none")
	} else {
		r.c.Print("Active profile: " + active)
	}
	r.c.Printf("Generated profiles: %s", strings.Join(names, ", "))
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
	r.c.Success("OCP detached.")
	r.c.Muted("Canonical source remains: " + source)
	r.c.Muted("Delete that directory manually to remove it.")
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

func (r *Runner) promptMenu(out io.Writer, title string, options []string) (int, error) {
	fmt.Fprintln(out, "")
	fmt.Fprint(out, title+"\n")
	for i, opt := range options {
		fmt.Fprintf(out, "  %d) %s\n", i+1, opt)
	}
	fmt.Fprint(out, "\nSelection: ")
	line, e := bufio.NewReader(r.In).ReadString('\n')
	if e != nil {
		return 0, e
	}
	line = strings.TrimSpace(line)
	n := len(options)
	for i := 0; i < n; i++ {
		if fmt.Sprint(i+1) == line {
			return i, nil
		}
	}
	fmt.Fprintf(r.Err, "Invalid selection: %q\n", line)
	return r.promptMenu(out, title, options)
}

func (r *Runner) promptText(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt+" ")
	line, e := bufio.NewReader(r.In).ReadString('\n')
	if e != nil {
		return "", e
	}
	return strings.TrimSpace(line), nil
}

func (r *Runner) promptTextWithDefault(out io.Writer, prompt, def string) (string, error) {
	fmt.Fprintf(out, "%s [%s] ", prompt, def)
	line, e := bufio.NewReader(r.In).ReadString('\n')
	if e != nil {
		return "", e
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func (r *Runner) promptMenuDynamic(out io.Writer, title string, hasOPM bool) (int, error) {
	options := []string{
		"Create a new local setup",
		"Import from an existing OpenCode directory",
	}
	if hasOPM {
		options = append(options, "Import from an existing OPM profile")
	}
	options = append(options, "Use an existing OCP repository")
	fmt.Fprintln(out, "")
	fmt.Fprint(out, title+"\n")
	for i, opt := range options {
		fmt.Fprintln(out, r.c.Prompt(i+1, opt))
	}
	fmt.Fprint(out, "\nSelection: ")
	line, e := bufio.NewReader(r.In).ReadString('\n')
	if e != nil {
		return 0, e
	}
	line = strings.TrimSpace(line)
	n := len(options)
	for i := 0; i < n; i++ {
		if fmt.Sprint(i+1) == line {
			return i, nil
		}
	}
	fmt.Fprintf(r.Err, "Invalid selection: %q\n", line)
	return r.promptMenuDynamic(out, title, hasOPM)
}

func (r *Runner) setupImportOPM(p ocp.Paths, source *string, autoCommit *bool, force *bool) error {
	opmDir := importer.DefaultOPMProfileDir()
	profiles, err := importer.ListOPMProfiles(opmDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		r.c.Muted("No OPM profiles found.")
		*source = "."
		return r.setupRun(p, ".", true, "", false, *autoCommit, *force, "")
	}
	selected, err := r.selectProfiles(profiles, "Choose profiles to import:")
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		r.c.Muted("Skipping OPM import.")
		*source = "."
		return r.setupRun(p, ".", true, "", false, *autoCommit, *force, "")
	}
	for _, name := range selected {
		inputPath := filepath.Join(opmDir, name)
		if e := importer.Import(inputPath, ".", *force); e != nil {
			return fmt.Errorf("import OPM profile %q: %w", name, e)
		}
		r.c.Success("Imported OPM profile: " + name)
	}
	*source = "."
	return r.setupRun(p, ".", true, "", false, *autoCommit, *force, "")
}

func selectedMarker(idx int, sel map[int]bool) string {
	if sel[idx] {
		return applyColor(color.ANSIGreen, "[x]")
	}
	return "[ ]"
}

func (r *Runner) selectProfiles(profiles []string, title string) ([]string, error) {
	sel := make(map[int]bool)
	reader := bufio.NewReader(r.In)
	frameCount := 0

	for {
		if frameCount > 0 {
			fmt.Fprint(r.Out, "\033[H\033[2J")
		}
		frameCount++

		fmt.Fprintln(r.Out, title)
		fmt.Fprintln(r.Out)
		for i, p := range profiles {
			fmt.Fprintf(r.Out, "  %d) %-25s%s\n", i+1, p, selectedMarker(i, sel))
		}
		fmt.Fprintln(r.Out)
		fmt.Fprint(r.Out, "Toggle: [number] + Enter • Select all: [a] + Enter • Continue: Enter")
		fmt.Print("> ")

		line, e := reader.ReadString('\n')
		if e != nil {
			return nil, e
		}
		tokens := strings.Fields(strings.TrimSpace(line))

		if len(tokens) == 0 {
			result := make([]string, 0, len(sel))
			for i, p := range profiles {
				if sel[i] {
					result = append(result, p)
				}
			}
			return result, nil
		}

		hasAll := false
		for _, tok := range tokens {
			if strings.ToLower(tok) == "a" {
				hasAll = true
				continue
			}
			var n int
			if _, err := fmt.Sscanf(tok, "%d", &n); err != nil || n < 1 || n > len(profiles) {
				fmt.Fprintf(r.Err, "Invalid input: %q\n", strings.TrimSpace(line))
				continue
			}
			if sel[n-1] {
				delete(sel, n-1)
			} else {
				sel[n-1] = true
			}
		}
		if hasAll {
			allSelected := true
			for i := range profiles {
				if !sel[i] {
					allSelected = false
					break
				}
			}
			if allSelected {
				sel = make(map[int]bool)
			} else {
				for i := range profiles {
					sel[i] = true
				}
			}
		}
	}
}

// selectAllFirstOnly presents a numbered list and returns the first selected profile.
func (r *Runner) selectAllFirstOnly(p ocp.Paths, profiles []string) (string, error) {
	if len(profiles) == 0 {
		return "", nil
	}
	name, err := r.selectSingle(profiles, "Select active profile:")
	if err != nil {
		return "", err
	}
	if name == "" {
		return profiles[0], nil
	}
	return name, nil
}

// selectSingle presents a numbered list where pressing a number selects that item.
// Pressing Enter alone submits the current selection or an empty string if none.
func (r *Runner) selectSingle(profiles []string, title string) (string, error) {
	selected := -1
	reader := bufio.NewReader(r.In)
	frameCount := 0

	for {
		if frameCount > 0 {
			fmt.Fprint(r.Out, "\033[H\033[2J")
		}
		frameCount++

		fmt.Fprintln(r.Out, title)
		fmt.Fprintln(r.Out)
		for i, p := range profiles {
			marker := "[ ]"
			if i == selected {
				marker = applyColor(color.ANSIGreen, "[x]")
			}
			fmt.Fprintf(r.Out, "  %d) %-25s%s\n", i+1, p, marker)
		}
		fmt.Fprintln(r.Out)
		if selected >= 0 {
			fmt.Fprintln(r.Out, "Press Enter to confirm "+profiles[selected])
		} else {
			fmt.Fprint(r.Out, "Enter a number to select, or Enter alone to skip:")
		}

		line, e := reader.ReadString('\n')
		if e != nil {
			return "", e
		}
		line = strings.TrimSpace(line)

		if line == "" {
			if selected < 0 {
				return "", nil
			}
			return profiles[selected], nil
		}

		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err != nil || n < 1 || n > len(profiles) {
			fmt.Fprintf(r.Err, "Invalid selection: %q\n", line)
			continue
		}
		selected = n - 1
	}
}

// applyColor wraps text with ANSI escape codes when colors are enabled.
func applyColor(code, text string) string {
	return color.ApplyColor(code, text)
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
					r.c.Success("Imported OpenCode configuration into " + source)
					return nil
				} else {
					return retry
				}
			}
			return errors.New("cancelled")
		}
		return e
	}
	r.c.Success("Imported OpenCode configuration into " + source)
	return nil
}
