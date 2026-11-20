package cli

import (
	"encoding/json"
	"flag"
	"path/filepath"

	"github.com/nando/ocp/internal/config"
	"github.com/nando/ocp/internal/ocp"
)

func configureStatus(f *flag.FlagSet, asJSON *bool) {
	f.BoolVar(asJSON, "json", false, "print status as JSON for scripts and agents")
}

type statusDoc struct {
	Source     string            `json:"source"`
	ConfigFile string            `json:"config_file"`
	AutoCommit bool              `json:"auto_commit"`
	Machine    string            `json:"machine,omitempty"`
	Harnesses  []string          `json:"harnesses"`
	Active     map[string]string `json:"active"`
	Profiles   []string          `json:"profiles"`
	Error      string            `json:"error,omitempty"`
}

// statusJSON reports where the source lives and what each harness runs, so
// an agent can find the file to edit. A source that does not parse is
// reported, not fatal: fixing it is often the reason to ask.
func (r *Runner) statusJSON(p ocp.Paths, s *ocp.State) error {
	source := s.Source
	if c, err := canonical(s.Source); err == nil {
		source = c
	}
	out := statusDoc{Source: source, ConfigFile: filepath.Join(source, config.FileName), AutoCommit: s.AutoCommit, Machine: s.Machine, Harnesses: []string{"opencode"}, Active: map[string]string{}, Profiles: []string{}}
	if active, err := ocp.ActiveProfile(p); err == nil && active != "" {
		out.Active["opencode"] = active
	}
	for _, h := range []string{"claude", "codex"} {
		if active, err := ocp.NativeActiveProfile(p, h); err == nil && active != "" {
			out.Active[h] = active
		}
	}
	if doc, err := sourceDocument(s); err != nil {
		out.Error = err.Error()
	} else {
		out.Harnesses = doc.EnabledHarnesses()
		if profiles, err := doc.ResolveFromDirForMachine(source, s.Machine); err != nil {
			out.Error = err.Error()
		} else {
			for _, profile := range profiles {
				out.Profiles = append(out.Profiles, profile.Name)
			}
		}
	}
	enc := json.NewEncoder(r.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
