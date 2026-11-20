package ocp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nando/ocp/internal/config"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// NativeProfile is a validated installation plan; preparing it does not write files.
// Files are relative to the native configuration directory. Skills point to
// canonical source directories which the installer must snapshot with copyTree.
type NativeProfile struct {
	Settings map[string]any
	Files    map[string][]byte
	Skills   map[string]string
	Guide    []byte
	Warnings []string
}

// PrepareNativeProfile translates only settings whose native meaning is known.
// Unsupported behavior is an error, not a silently weakened configuration.
func PrepareNativeProfile(source string, p config.Profile, harness, machine string, provider SkillProvider) (NativeProfile, error) {
	out := NativeProfile{Settings: map[string]any{}, Files: map[string][]byte{}, Skills: map[string]string{}}
	if harness != "claude" && harness != "codex" {
		return NativeProfile{}, fmt.Errorf("unsupported native harness %q", harness)
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return NativeProfile{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return NativeProfile{}, err
	}
	if machine != "" {
		if err := config.ValidMachineName(machine); err != nil {
			return NativeProfile{}, err
		}
	}
	for _, key := range nativeKeys(p.Config) {
		value := p.Config[key]
		switch key {
		case "model":
			model, err := config.ResolveModel(value, harness)
			if err != nil {
				return NativeProfile{}, fmt.Errorf("config.model: %w", err)
			}
			if model != "" {
				out.Settings[key] = model
			}
		case "agent": // Translated below, including config-only agents.
		case "mcp": // Translated below.
		case "plugin", "plugins", "$schema", "provider", "providers", "compaction", "theme", "autoupdate", "share", "keybinds", "tui", "formatter", "lsp", "small_model", "username", "layout", "snapshot", "watcher":
			// These only shape OpenCode itself. Providers for other harnesses
			// go in that harness's own block (claude: or codex:).
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: omitted OpenCode-only config.%s", harness, key))
		case "model_reasoning_effort":
			if harness != "codex" {
				return NativeProfile{}, fmt.Errorf("config.%s is unsupported for %s", key, harness)
			}
			effort, ok := value.(string)
			if !ok {
				return NativeProfile{}, fmt.Errorf("config.model_reasoning_effort must be a supported effort string")
			}
			switch effort {
			case "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
			default:
				return NativeProfile{}, fmt.Errorf("config.model_reasoning_effort must be a supported effort string")
			}
			out.Settings[key] = effort
		default:
			return NativeProfile{}, fmt.Errorf("config.%s is not supported by the %s adapter", key, harness)
		}
	}
	if len(p.Plugins) > 0 {
		out.Warnings = append(out.Warnings, harness+": omitted OpenCode plugins")
	}
	out.Warnings = append(out.Warnings, disabledWarnings(p)...)
	if len(p.FilesDirs) > 0 {
		out.Warnings = append(out.Warnings, harness+": omitted the profile's files/ directory, which is rendered for OpenCode only")
	}
	servers, err := openCodeMCPServers(p.Config)
	if err != nil {
		return NativeProfile{}, err
	}
	for _, server := range servers {
		if harness == "claude" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("claude: MCP server %q not installed; OCP does not manage Claude Code MCP servers yet", server.Name))
			continue
		}
		native, warning, err := codexMCPServer(server, source)
		if err != nil {
			return NativeProfile{}, err
		}
		if warning != "" {
			out.Warnings = append(out.Warnings, "codex: "+warning)
			continue
		}
		mcp, _ := out.Settings["mcp_servers"].(map[string]any)
		if mcp == nil {
			mcp = map[string]any{}
			out.Settings["mcp_servers"] = mcp
		}
		mcp[server.Name] = native
	}
	if err := mergeHarnessBlock(out.Settings, harness, p); err != nil {
		return NativeProfile{}, err
	}
	baseAgents := map[string]any{}
	if value, exists := p.Config["agent"]; exists {
		var ok bool
		baseAgents, ok = value.(map[string]any)
		if !ok {
			return NativeProfile{}, errors.New("config.agent must be an object")
		}
	}
	names := map[string]any{}
	for name := range baseAgents {
		names[name] = nil
	}
	for name := range p.Agents {
		names[name] = nil
	}
	for _, name := range nativeKeys(names) {
		if err := config.ValidMachineName(name); err != nil {
			return NativeProfile{}, fmt.Errorf("agent: %w", err)
		}
		base := map[string]any{}
		if value, exists := baseAgents[name]; exists {
			var ok bool
			base, ok = value.(map[string]any)
			if !ok {
				return NativeProfile{}, fmt.Errorf("config.agent.%s must be an object", name)
			}
		}
		a := p.Agents[name]
		var body []byte
		front := map[string]any{}
		if a.File != "" {
			data, err := readSource(source, a.File, false)
			if err != nil {
				return NativeProfile{}, fmt.Errorf("agent %q: %w", name, err)
			}
			front, body, err = nativeFrontmatter(data)
			if err != nil {
				return NativeProfile{}, fmt.Errorf("agent %q: %w", name, err)
			}
			if _, exists := front["prompt"]; exists {
				return NativeProfile{}, fmt.Errorf("agent %q: unsupported frontmatter option prompt; put instructions in the Markdown body", name)
			}
		}
		// Validate every layer's options even if another layer overrides them.
		for _, layer := range []map[string]any{base, front, a.Config} {
			if err := nativeAgentOptions(layer); err != nil {
				return NativeProfile{}, fmt.Errorf("agent %q: %w", name, err)
			}
		}
		values := mergeMap(mergeMap(base, front), a.Config)
		// Frontmatter and assignment models are defaults/overrides, not another
		// inheritance layer. A missing target in an assignment map cannot fall
		// back to the source model implicitly.
		for _, layer := range []map[string]any{base, front, a.Config} {
			if model, exists := layer["model"]; exists {
				values["model"] = model
			}
		}
		if prompt, exists := values["prompt"]; exists {
			if a.File != "" {
				return NativeProfile{}, fmt.Errorf("agent %q: prompt override with a source file is unsupported", name)
			}
			text := prompt.(string)
			if strings.Contains(text, "{file:") {
				return NativeProfile{}, fmt.Errorf("agent %q: prompt file references are unsupported", name)
			}
			body = []byte(text)
		}
		description, _ := values["description"].(string)
		if strings.TrimSpace(description) == "" {
			return NativeProfile{}, fmt.Errorf("agent %q requires a non-empty description for %s", name, harness)
		}
		native := map[string]any{"name": name, "description": description}
		if value, exists := values["model"]; exists {
			model, err := config.ResolveModel(value, harness)
			if err != nil {
				return NativeProfile{}, fmt.Errorf("agent %q model: %w", name, err)
			}
			if model != "" {
				native["model"] = model
			}
		}
		var data []byte
		if harness == "claude" {
			data, err = yaml.Marshal(native)
			data = append(append(append([]byte("---\n"), data...), []byte("---\n")...), body...)
			out.Files["agents/"+name+".md"] = data
		} else {
			native["developer_instructions"] = string(body)
			data, err = toml.Marshal(native)
			path := "agents/" + name + ".toml"
			out.Files[path] = data
		}
		if err != nil {
			return NativeProfile{}, fmt.Errorf("agent %q: %w", name, err)
		}
	}
	instructions := append([]string(nil), p.Instructions...)
	if machine != "" {
		instructions = append(instructions, filepath.Join("hosts", machine+".md"))
	}
	for _, name := range instructions {
		data, err := readSource(source, name, false)
		if err != nil {
			if machine != "" && name == filepath.Join("hosts", machine+".md") && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return NativeProfile{}, fmt.Errorf("instruction %q: %w", name, err)
		}
		out.Guide = append(out.Guide, data...)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			out.Guide = append(out.Guide, '\n')
		}
	}
	for _, skill := range p.Skills {
		var path string
		if isGit(skill.Source) {
			if provider == nil {
				return NativeProfile{}, fmt.Errorf("Git skill %q requires a SkillProvider", skill.Source)
			}
			path, err = provider.SkillDir(skill)
		} else {
			path, err = localSourcePath(source, "skills", skill.Source)
		}
		if err != nil {
			return NativeProfile{}, fmt.Errorf("skill %q: %w", skill.Source, err)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return NativeProfile{}, err
		}
		// Match copyTree's rejection of symlinks/special files without copying.
		if err := nativeSkillSafe(path, harness); err != nil {
			return NativeProfile{}, fmt.Errorf("skill %q: %w", skill.Source, err)
		}
		base := filepath.Base(path)
		if base == "." || base == string(filepath.Separator) || out.Skills[base] != "" {
			return NativeProfile{}, fmt.Errorf("skill destination collision: %q", base)
		}
		out.Skills[base] = path
	}
	return out, nil
}

func nativeKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nativeAgentOptions(values map[string]any) error {
	for _, key := range nativeKeys(values) {
		switch key {
		case "model": // Resolve only the effective model after precedence.
			if err := config.ValidateModel(values[key]); err != nil {
				return err
			}
		case "name", "description", "prompt":
			if _, ok := values[key].(string); !ok {
				return fmt.Errorf("%s must be a string", key)
			}
		case "mode":
			if values[key] != "subagent" && values[key] != "all" {
				return fmt.Errorf("mode must be subagent or all for native subagents")
			}
		default:
			return fmt.Errorf("unsupported option %q for native subagents", key)
		}
	}
	return nil
}

func nativeFrontmatter(data []byte) (map[string]any, []byte, error) {
	lines := bytes.Split(data, []byte("\n"))
	values := map[string]any{}
	if string(bytes.TrimSuffix(lines[0], []byte("\r"))) != "---" {
		return values, data, nil
	}
	for i := 1; i < len(lines); i++ {
		if string(bytes.TrimSuffix(lines[i], []byte("\r"))) == "---" {
			header := bytes.Join(lines[1:i], []byte("\n"))
			if len(bytes.TrimSpace(header)) == 0 {
				return values, bytes.Join(lines[i+1:], []byte("\n")), nil
			}
			if err := yaml.Unmarshal(header, &values); err != nil {
				return nil, nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
			}
			if values == nil {
				return nil, nil, errors.New("frontmatter must be a mapping")
			}
			return values, bytes.Join(lines[i+1:], []byte("\n")), nil
		}
	}
	return nil, nil, errors.New("unterminated YAML frontmatter")
}

func nativeSkillSafe(path, harness string) error {
	if err := sourcePathSafe(string(filepath.Separator), path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("skill must be a directory")
	}
	if err := filepath.WalkDir(path, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return fmt.Errorf("unsupported skill file (symlinks are not allowed): %s", name)
		}
		if entry.IsDir() && name == filepath.Join(path, ".git") {
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		return err
	}
	data, err := readSource(path, "SKILL.md", false)
	if err != nil {
		return err
	}
	values, _, err := nativeFrontmatter(data)
	if err != nil {
		return err
	}
	for _, key := range []string{"name", "description"} {
		value, ok := values[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("SKILL.md requires a non-empty %s", key)
		}
	}
	if harness == "codex" {
		for _, key := range nativeKeys(values) {
			switch key {
			case "name", "description", "license", "compatibility", "metadata":
			default:
				return fmt.Errorf("SKILL.md field %q is not portable to codex", key)
			}
		}
	}
	return nil
}
