package ocp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nando/ocp/internal/config"
)

// mcpServer is an MCP server read from OpenCode configuration, in either the
// current `mcp.servers.<name>` shape or the legacy `mcp.<name>` shape.
type mcpServer struct {
	Name    string
	Local   bool
	Command []string
	Env     map[string]string
	URL     string
	Headers map[string]string
}

// substitution matches OpenCode's {env:NAME} and {file:path} placeholders.
var substitution = regexp.MustCompile(`\{(env|file):[^}]*\}`)

func hasSubstitution(s string) bool { return substitution.MatchString(s) }

// openCodeMCPServers returns enabled servers sorted by name. Settings that are
// not servers (such as mcp.timeout) are ignored; malformed servers are errors.
func openCodeMCPServers(config map[string]any) ([]mcpServer, error) {
	raw, ok := config["mcp"]
	if !ok {
		return nil, nil
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config.mcp must be an object")
	}
	entries := map[string]any{}
	for name, value := range root {
		if servers, ok := value.(map[string]any); ok && name == "servers" {
			for server, spec := range servers {
				entries[server] = spec
			}
			continue
		}
		if spec, ok := value.(map[string]any); ok {
			if _, typed := spec["type"]; typed {
				entries[name] = spec
			}
		}
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []mcpServer
	for _, name := range names {
		spec, ok := entries[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcp server %q must be an object", name)
		}
		if enabled, ok := spec["enabled"].(bool); ok && !enabled {
			continue
		}
		s := mcpServer{Name: name}
		switch spec["type"] {
		case "local":
			s.Local = true
			cmd, err := stringList(spec["command"])
			if err != nil || len(cmd) == 0 {
				return nil, fmt.Errorf("mcp server %q: command must be a non-empty list of strings", name)
			}
			s.Command = cmd
			env := spec["environment"]
			if env == nil {
				env = spec["env"]
			}
			if s.Env, err = stringMap(env); err != nil {
				return nil, fmt.Errorf("mcp server %q environment: %w", name, err)
			}
		case "remote":
			url, ok := spec["url"].(string)
			if !ok || url == "" {
				return nil, fmt.Errorf("mcp server %q: url must be a non-empty string", name)
			}
			s.URL = url
			var err error
			if s.Headers, err = stringMap(spec["headers"]); err != nil {
				return nil, fmt.Errorf("mcp server %q headers: %w", name, err)
			}
		default:
			return nil, fmt.Errorf("mcp server %q: type must be local or remote", name)
		}
		out = append(out, s)
	}
	return out, nil
}

func stringList(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("must be a list")
	}
	out := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("must contain only strings")
		}
		out[i] = s
	}
	return out, nil
}

func stringMap(v any) (map[string]string, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be an object of strings")
	}
	out := make(map[string]string, len(m))
	for k, value := range m {
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", k)
		}
		out[k] = s
	}
	return out, nil
}

// secretArgs encodes NAME=template pairs for the runtime `ocp secret` helper.
func secretArgs(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+values[k])
	}
	return out
}

// shellQuote quotes one word for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// exactEnv reports NAME when s is exactly {env:NAME}.
func exactEnv(s string) (string, bool) {
	name, ok := strings.CutPrefix(s, "{env:")
	if !ok {
		return "", false
	}
	name, ok = strings.CutSuffix(name, "}")
	return name, ok && name != "" && !strings.ContainsAny(name, "{}")
}

// envNames lists the variables a template reads.
func envNames(template string) []string {
	var out []string
	for _, m := range substitution.FindAllString(template, -1) {
		if name, ok := exactEnv(m); ok {
			out = append(out, name)
		}
	}
	return out
}

// absoluteFiles rewrites relative {file:path} placeholders against the OCP
// source, because a harness runs the helper from the project directory.
// `~/` stays for the helper to expand.
func absoluteFiles(template, source string) string {
	return substitution.ReplaceAllStringFunc(template, func(m string) string {
		path, ok := strings.CutPrefix(strings.TrimSuffix(m, "}"), "{file:")
		if !ok || strings.HasPrefix(path, "~/") || filepath.IsAbs(path) {
			return m
		}
		return "{file:" + filepath.Join(source, path) + "}"
	})
}

// codexMCPServer renders one server for Codex. Environment references use
// Codex's own forwarding (env_vars, env_http_headers, bearer_token_env_var),
// so no secret is written. Values read from files go through the runtime
// `ocp secret exec` wrapper. A server Codex cannot express returns a warning
// and no table.
func codexMCPServer(s mcpServer, source string) (map[string]any, string, error) {
	out := map[string]any{}
	if s.Local {
		for _, word := range s.Command {
			if hasSubstitution(word) {
				return nil, fmt.Sprintf("MCP server %q skipped: {env:} and {file:} in its command are not supported", s.Name), nil
			}
		}
		literal := map[string]any{}
		secret := map[string]string{}
		var forward []string
		for _, k := range sortedKeys(s.Env) {
			v := s.Env[k]
			if strings.ContainsAny(v, "\n\x00") {
				return nil, "", fmt.Errorf("mcp server %q environment %s must not contain newlines", s.Name, k)
			}
			switch name, exact := exactEnv(v); {
			case !hasSubstitution(v):
				literal[k] = v
			case exact && name == k:
				forward = append(forward, name)
			default:
				secret[k] = absoluteFiles(v, source)
				forward = append(forward, envNames(v)...)
			}
		}
		command, args := s.Command[0], s.Command[1:]
		if len(secret) > 0 {
			helper, err := ocpExecutable()
			if err != nil {
				return nil, fmt.Sprintf("MCP server %q skipped: %v", s.Name, err), nil
			}
			wrapped := append([]string{"secret", "exec"}, secretArgs(secret)...)
			wrapped = append(append(wrapped, "--"), s.Command...)
			command, args = helper, wrapped
		}
		out["command"] = command
		if len(args) > 0 {
			list := make([]any, len(args))
			for i, a := range args {
				list[i] = a
			}
			out["args"] = list
		}
		if len(literal) > 0 {
			out["env"] = literal
		}
		if len(forward) > 0 {
			slices.Sort(forward)
			forward = slices.Compact(forward)
			list := make([]any, len(forward))
			for i, n := range forward {
				list[i] = n
			}
			out["env_vars"] = list
		}
		return out, "", nil
	}
	if hasSubstitution(s.URL) {
		return nil, fmt.Sprintf("MCP server %q skipped: {env:} and {file:} in its url are not supported", s.Name), nil
	}
	out["url"] = s.URL
	static := map[string]any{}
	fromEnv := map[string]any{}
	for _, h := range sortedKeys(s.Headers) {
		v := s.Headers[h]
		if !hasSubstitution(v) {
			static[h] = v
			continue
		}
		if name, ok := exactEnv(v); ok {
			fromEnv[h] = name
			continue
		}
		if rest, ok := strings.CutPrefix(v, "Bearer "); ok && strings.EqualFold(h, "Authorization") {
			if name, ok := exactEnv(rest); ok {
				out["bearer_token_env_var"] = name
				continue
			}
		}
		return nil, fmt.Sprintf("MCP server %q skipped: Codex cannot read header %s from a file; put the value in an environment variable and use {env:NAME}", s.Name, h), nil
	}
	if len(static) > 0 {
		out["http_headers"] = static
	}
	if len(fromEnv) > 0 {
		out["env_http_headers"] = fromEnv
	}
	return out, "", nil
}

// ocpExecutable is the absolute path harness helpers use to call back into
// OCP. It refuses a temporary build, which would vanish after `go run`.
var ocpExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if onPath, err := exec.LookPath("ocp"); err == nil {
		if a, e1 := os.Stat(onPath); e1 == nil {
			if b, e2 := os.Stat(exe); e2 == nil && os.SameFile(a, b) {
				if abs, err := filepath.Abs(onPath); err == nil {
					return abs, nil
				}
			}
		}
	}
	if strings.Contains(exe, "go-build") || strings.HasPrefix(exe, os.TempDir()+string(filepath.Separator)) {
		return "", fmt.Errorf("ocp is running from a temporary build (%s); install it so harnesses can call it", exe)
	}
	return exe, nil
}

// mergeHarnessBlock adds the profile's claude: or codex: block to the
// rendered settings. A key OCP already derived from config: is a collision.
func mergeHarnessBlock(settings map[string]any, harness string, p config.Profile) error {
	block := p.Claude
	if harness == "codex" {
		block = p.Codex
	}
	for _, key := range sortedKeys(block) {
		value := cloneMap(map[string]any{key: block[key]})[key]
		if _, exists := settings[key]; exists {
			return fmt.Errorf("%s.%s conflicts with the value OCP derives from config.%s; set it in one place", harness, key, key)
		}
		if key == "apiKeyHelper" {
			text := value.(string)
			if strings.Contains(text, "{ocp}") {
				helper, err := ocpExecutable()
				if err != nil {
					return fmt.Errorf("claude.apiKeyHelper: %w", err)
				}
				text = strings.ReplaceAll(text, "{ocp}", shellQuote(helper))
			}
			value = text
		}
		settings[key] = value
	}
	return nil
}
