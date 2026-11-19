package ocp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nando/ocp/internal/config"
)

// OpenCodeProfile selects model maps without changing the resolved source.
func openCodeProfile(source string, p config.Profile) (config.Profile, error) {
	p.Config = cloneMap(p.Config)
	selectModel := func(values map[string]any, label string) error {
		if value, ok := values["model"]; ok {
			model, err := config.ResolveModel(value, "opencode")
			if err != nil {
				return fmt.Errorf("%s.model: %w", label, err)
			}
			values["model"] = model
		}
		return nil
	}
	if err := selectModel(p.Config, "config"); err != nil {
		return p, err
	}
	if native, ok := p.Config["agent"].(map[string]any); ok {
		for _, name := range sortedKeys(native) {
			if a, ok := native[name].(map[string]any); ok {
				assignment := p.Agents[name]
				if _, assigned := assignment.Config["model"]; assigned {
					delete(a, "model")
				} else if assignment.File != "" {
					data, err := readSource(source, assignment.File, false)
					if err != nil {
						return p, err
					}
					front, _, err := nativeFrontmatter(data)
					if err != nil {
						return p, err
					}
					if _, declared := front["model"]; declared {
						delete(a, "model")
					}
				}
				if err := selectModel(a, "config.agent."+name); err != nil {
					return p, err
				}
			}
		}
	}
	agents := make(map[string]config.Agent, len(p.Agents))
	for _, name := range sortedKeys(p.Agents) {
		a := p.Agents[name]
		a.Config = cloneMap(a.Config)
		if err := selectModel(a.Config, "agents."+name); err != nil {
			return p, err
		}
		agents[name] = a
	}
	p.Agents = agents
	return p, nil
}

// ValidateOpenCodeProfile also checks effective source-frontmatter assignments.
func ValidateOpenCodeProfile(source string, p config.Profile) error {
	selected, err := openCodeProfile(source, p)
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(selected.Agents) {
		a := selected.Agents[name]
		if a.File == "" {
			continue
		}
		data, err := readSource(source, a.File, false)
		if err != nil {
			return err
		}
		front, _, err := nativeFrontmatter(data)
		if err != nil {
			return err
		}
		if value, ok := front["model"]; ok {
			if err := config.ValidateModel(value); err != nil {
				return fmt.Errorf("agent %s model: %w", name, err)
			}
		}
		if modelFromConfig(a.Config) != "" {
			continue
		}
		if value, ok := front["model"]; ok {
			if _, err := config.ResolveModel(value, "opencode"); err != nil {
				return fmt.Errorf("agent %s model: %w", name, err)
			}
		}
	}
	return nil
}

// OpenCodeInstallPlan stages a complete release without activating it.
type OpenCodeInstallPlan struct {
	paths   Paths
	profile string
	state   State
	temp    string
}

func (plan *OpenCodeInstallPlan) Close() { _ = os.RemoveAll(plan.temp) }

// PrepareOpenCodeInstall uses the existing renderer, validating assets and drift
// before any selected harness is activated.
func PrepareOpenCodeInstall(p Paths, source string, profile config.Profile, provider SkillProvider, state State, force bool) (*OpenCodeInstallPlan, error) {
	if err := ValidateOpenCodeProfile(source, profile); err != nil {
		return nil, err
	}
	if !force {
		var paths []string
		var err error
		if target := managedTarget(p, p.OpenCode); target != "" {
			if resolved, e := filepath.EvalSymlinks(target); e == nil {
				paths, err = driftRoot(filepath.Dir(resolved))
			} else {
				return nil, e
			}
		}
		if err != nil {
			return nil, err
		} else if len(paths) > 0 {
			return nil, &DriftError{Paths: paths}
		}
	}
	tmp, err := os.MkdirTemp("", "ocp-preflight-")
	if err != nil {
		return nil, err
	}
	doc, err := config.Load(filepath.Join(source, config.FileName))
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	profiles, err := doc.ResolveFromDirForMachine(source, state.Machine)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	for _, candidate := range profiles {
		if err := ValidateOpenCodeProfile(source, candidate); err != nil {
			continue
		}
		if _, err := renderProfile(filepath.Join(tmp, candidate.Name), source, candidate, provider, state.Machine); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
	}
	return &OpenCodeInstallPlan{p, profile.Name, state, tmp}, nil
}

// ApplyOpenCodeInstall pins activation to the immutable release, so updates of
// other targets cannot change a skipped OpenCode configuration indirectly.
func ApplyOpenCodeInstall(plan *OpenCodeInstallPlan) error {
	p := plan.paths
	if err := os.MkdirAll(p.Releases, 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(p.Releases, ".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(stage, plan.temp); err != nil {
		return err
	}
	release := filepath.Join(p.Releases, fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.Rename(stage, release); err != nil {
		return err
	}
	if err := replaceSymlink(p.Current, release); err != nil {
		return err
	}
	if err := TakeOver(p, plan.profile, plan.state); err != nil {
		return err
	}
	return replaceSymlink(p.OpenCode, filepath.Join(release, plan.profile))
}

func pinnedProfile(p Paths, target string) string {
	rel, err := filepath.Rel(p.Releases, target)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 || parts[0] == "." || parts[0] == ".." || parts[1] == "" {
		return ""
	}
	return parts[1]
}
