package ocp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nando/ocp/internal/config"
)

// ContextPart is one contributor to a profile's always-loaded context.
type ContextPart struct {
	Kind   string `json:"kind"` // instructions, skills, agents
	Count  int    `json:"count,omitempty"`
	Tokens int    `json:"tokens"`
}

// ContextEstimate approximates what a profile loads before the first prompt.
// Tokens are characters divided by four: a deliberately simple, documented
// heuristic, not any one model's tokenizer.
type ContextEstimate struct {
	Tokens int           `json:"tokens"`
	Parts  []ContextPart `json:"parts"`
	// OnDemand is the size of skill bodies, which harnesses load only when a
	// skill is used.
	OnDemand int `json:"on_demand_tokens"`
	// MCPServers are counted but not measured: their tool definitions come
	// from running each server.
	MCPServers int `json:"mcp_servers,omitempty"`
	// Unmeasured lists skills that could not be read, such as Git skills
	// that are not cached yet.
	Unmeasured []string `json:"unmeasured,omitempty"`
}

func estimateTokens(chars int) int { return (chars + 3) / 4 }

// maxSkillFile bounds what the estimate reads from one SKILL.md.
const maxSkillFile = 1 << 20

// readSkillFile reads a SKILL.md only if it is a regular file, as the
// renderer requires, so a symlink or device in a skill repository cannot
// make an estimate read something else or hang.
func readSkillFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("SKILL.md is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxSkillFile))
}

// EstimateContext measures a resolved profile from its source files. It
// reads only; a Git skill without a cached checkout is listed as unmeasured.
func EstimateContext(source string, p config.Profile, machine string, provider SkillProvider) (ContextEstimate, error) {
	est := ContextEstimate{Parts: []ContextPart{}}
	chars := 0
	names := append([]string(nil), p.Instructions...)
	if machine != "" {
		names = append(names, filepath.Join("hosts", machine+".md"))
	}
	files := 0
	for _, name := range names {
		b, err := readSource(source, name, false)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return est, err
		}
		chars += len(b)
		files++
	}
	if chars > 0 {
		est.Parts = append(est.Parts, ContextPart{Kind: "instructions", Count: files, Tokens: estimateTokens(chars)})
	}

	skillChars, bodyChars, measured := 0, 0, 0
	for _, skill := range p.Skills {
		var dir string
		var err error
		if isGit(skill.Source) {
			if provider == nil {
				err = errors.New("no provider")
			} else {
				dir, err = provider.SkillDir(skill)
			}
		} else {
			dir, err = localSourcePath(source, "skills", skill.Source)
		}
		var b []byte
		if err == nil {
			b, err = readSkillFile(filepath.Join(dir, "SKILL.md"))
		}
		if err != nil {
			est.Unmeasured = append(est.Unmeasured, skill.Name())
			continue
		}
		front, body, err := nativeFrontmatter(b)
		if err != nil {
			est.Unmeasured = append(est.Unmeasured, skill.Name())
			continue
		}
		name, _ := front["name"].(string)
		if name == "" {
			name = skill.Name()
		}
		description, _ := front["description"].(string)
		skillChars += len(name) + len(description) + 4
		bodyChars += len(body)
		measured++
	}
	if measured > 0 {
		est.Parts = append(est.Parts, ContextPart{Kind: "skills", Count: measured, Tokens: estimateTokens(skillChars)})
	}
	est.OnDemand = estimateTokens(bodyChars)

	// Agents come from agents: and from native config.agent; one that is
	// disabled is never offered, so it costs nothing.
	native, _ := p.Config["agent"].(map[string]any)
	agentChars := 0
	agentNames := make([]string, 0, len(p.Agents)+len(native))
	for name := range p.Agents {
		agentNames = append(agentNames, name)
	}
	for name := range native {
		if _, dup := p.Agents[name]; !dup {
			agentNames = append(agentNames, name)
		}
	}
	sort.Strings(agentNames)
	counted := 0
	for _, name := range agentNames {
		a := p.Agents[name]
		base, _ := native[name].(map[string]any)
		if disabled, _ := base["disable"].(bool); disabled {
			continue
		}
		description, _ := a.Config["description"].(string)
		if description == "" {
			description, _ = base["description"].(string)
		}
		if description == "" && a.File != "" {
			if b, err := readSource(source, a.File, false); err == nil {
				if front, _, err := nativeFrontmatter(b); err == nil {
					description, _ = front["description"].(string)
				}
			}
		}
		agentChars += len(name) + len(description) + 4
		counted++
	}
	if counted > 0 {
		est.Parts = append(est.Parts, ContextPart{Kind: "agents", Count: counted, Tokens: estimateTokens(agentChars)})
	}
	if servers, err := openCodeMCPServers(p.Config); err == nil {
		est.MCPServers = len(servers)
	}
	sort.SliceStable(est.Parts, func(i, j int) bool { return est.Parts[i].Tokens > est.Parts[j].Tokens })
	for _, part := range est.Parts {
		est.Tokens += part.Tokens
	}
	sort.Strings(est.Unmeasured)
	return est, nil
}

// FormatTokens prints a token count compactly: 840, 9.1k, 12k.
func FormatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 9950:
		return strings.TrimSuffix(strconv.FormatFloat(float64(n)/1000, 'f', 1, 64), ".0") + "k"
	}
	return strconv.Itoa((n+500)/1000) + "k"
}
