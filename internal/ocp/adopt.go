package ocp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Adoption is a read-only inventory of what the harnesses on this machine
// hold outside OCP, compared with each other and with the OCP source.
type Adoption struct {
	Instructions []AdoptInstruction `json:"instructions"`
	Skills       []AdoptItem        `json:"skills"`
	MCP          []AdoptItem        `json:"mcp"`
	Secrets      []AdoptSecret      `json:"secrets,omitempty"`
}

// AdoptInstruction is one harness's global instruction file, without any
// OCP-managed block.
type AdoptInstruction struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
	Lines   int    `json:"lines"`
	// SameAs names another harness file or a source file with identical
	// text; CopyOf names a source file this text mostly repeats.
	SameAs string `json:"same_as,omitempty"`
	CopyOf string `json:"copy_of,omitempty"`
	text   []byte
}

// AdoptItem is a skill or MCP server found in at least one harness.
type AdoptItem struct {
	Name     string            `json:"name"`
	Found    map[string]string `json:"found"` // harness -> path or description
	Same     bool              `json:"same"`  // identical wherever found
	InSource bool              `json:"in_source"`
	// Value is the OpenCode form of an MCP server, secrets replaced.
	Value any    `json:"value,omitempty"`
	dir   string // a skill folder to copy
}

// AdoptSecret is a value that looks secret and would be replaced by an
// environment reference.
type AdoptSecret struct {
	Where string `json:"where"`
	Env   string `json:"env"`
}

// AdoptSource is what the OCP source already has, by name.
type AdoptSource struct {
	Skills       map[string]bool
	MCP          map[string]bool
	Instructions map[string][]byte // source-relative path -> text
}

var ocpBlock = regexp.MustCompile(`(?s)\n?<!-- BEGIN OCP MANAGED -->.*?<!-- END OCP MANAGED -->\n?`)

// Adopt inventories the harnesses under p.Home, skipping everything OCP
// already manages. It never writes.
func Adopt(p Paths, source AdoptSource) (Adoption, error) {
	var out Adoption
	owned := map[string]map[string]bool{"claude": {}, "codex": {}}
	ownedMCP := map[string]map[string]bool{"claude": {}, "codex": {}}
	for _, h := range []string{"claude", "codex"} {
		s, err := loadNativeState(p, h)
		if err != nil {
			return out, err
		}
		for rel := range s.Files {
			if name, ok := skillOf(rel); ok {
				owned[h][name] = true
			}
		}
		for name := range s.MCP {
			ownedMCP[h][name] = true
		}
		for _, k := range s.Keys {
			if len(k.Path) == 2 && k.Path[0] == "mcp_servers" {
				ownedMCP[h][k.Path[1]] = true
			}
		}
	}
	openCodeManaged := managedTarget(p, p.OpenCode) != ""

	// Instructions.
	files := []struct{ harness, path string }{
		{"claude", filepath.Join(p.Home, ".claude", "CLAUDE.md")},
		{"codex", filepath.Join(p.Home, ".codex", "AGENTS.md")},
	}
	if !openCodeManaged {
		files = append(files, struct{ harness, path string }{"opencode", filepath.Join(p.OpenCode, "AGENTS.md")})
	}
	for _, f := range files {
		b, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		text := bytes.TrimSpace(ocpBlock.ReplaceAll(b, []byte("\n")))
		if len(text) == 0 {
			continue
		}
		in := AdoptInstruction{Harness: f.harness, Path: f.path, Lines: bytes.Count(text, []byte("\n")) + 1, text: text}
		for _, prev := range out.Instructions {
			if bytes.Equal(prev.text, text) {
				in.SameAs = prev.Harness
				break
			}
		}
		for _, rel := range sortedKeys(source.Instructions) {
			src := bytes.TrimSpace(source.Instructions[rel])
			if bytes.Equal(src, text) {
				in.SameAs = rel
				break
			}
			if in.CopyOf == "" && similar(text, src) {
				in.CopyOf = rel
			}
		}
		out.Instructions = append(out.Instructions, in)
	}

	// Skills.
	skillDirs := map[string]map[string]string{} // name -> harness -> dir
	add := func(harness, root string, skip map[string]bool) {
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		for _, e := range entries {
			if skip[e.Name()] {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if e.Type()&os.ModeSymlink != 0 {
				// Installers often keep one copy and link it into each
				// harness; adopt the folder the link points to.
				resolved, err := filepath.EvalSymlinks(dir)
				if err != nil {
					continue
				}
				dir = resolved
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			if info, err := os.Lstat(filepath.Join(dir, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
				continue
			}
			if skillDirs[e.Name()] == nil {
				skillDirs[e.Name()] = map[string]string{}
			}
			skillDirs[e.Name()][harness] = dir
		}
	}
	add("claude", filepath.Join(p.Home, ".claude", "skills"), owned["claude"])
	add("codex", filepath.Join(p.Home, ".agents", "skills"), owned["codex"])
	if !openCodeManaged {
		add("opencode", filepath.Join(p.OpenCode, "skills"), nil)
	}
	for _, name := range sortedKeys(skillDirs) {
		item := AdoptItem{Name: name, Found: map[string]string{}, Same: true, InSource: source.Skills[name]}
		first := ""
		for _, h := range sortedKeys(skillDirs[name]) {
			dir := skillDirs[name][h]
			item.Found[h] = dir
			sum := treeHash(dir)
			if first == "" {
				first, item.dir = sum, dir
			} else if sum != first {
				item.Same = false
			}
		}
		out.Skills = append(out.Skills, item)
	}

	// MCP servers, compared in OpenCode's form.
	servers := map[string]map[string]any{} // name -> harness -> server
	if claude, err := readClaudeServers(p); err == nil {
		for name, v := range claude {
			if !ownedMCP["claude"][name] {
				if s, ok := fromClaudeMCP(v); ok {
					put(servers, name, "claude", s)
				}
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(p.Home, ".codex", "config.toml")); err == nil {
		if settings, err := nativeSettings(b, "codex"); err == nil {
			table, _ := settings["mcp_servers"].(map[string]any)
			for name, v := range table {
				if !ownedMCP["codex"][name] {
					if s, ok := fromCodexMCP(v); ok {
						put(servers, name, "codex", s)
					}
				}
			}
		}
	}
	if !openCodeManaged {
		if b, err := os.ReadFile(filepath.Join(p.OpenCode, "opencode.json")); err == nil {
			var cfg map[string]any
			if json.Unmarshal(b, &cfg) == nil {
				if list, err := openCodeMCPServers(cfg); err == nil {
					for _, s := range list {
						put(servers, s.Name, "opencode", openCodeForm(s))
					}
				}
			}
		}
	}
	for _, name := range sortedKeys(servers) {
		item := AdoptItem{Name: name, Found: map[string]string{}, Same: true, InSource: source.MCP[name]}
		var first any
		for _, h := range sortedKeys(servers[name]) {
			v := servers[name][h]
			redacted, _ := redactServer(name, v)
			item.Found[h] = describeServer(redacted)
			if first == nil {
				first = v
			} else if !nativeEqual(first, v) {
				item.Same = false
			}
		}
		value, secrets := redactServer(name, first)
		item.Value = value
		out.Secrets = append(out.Secrets, secrets...)
		out.MCP = append(out.MCP, item)
	}
	return out, nil
}

func skillOf(rel string) (string, bool) {
	rest, ok := strings.CutPrefix(rel, "skills/")
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(rest, "/")
	return name, name != ""
}

func put(m map[string]map[string]any, name, harness string, v any) {
	if m[name] == nil {
		m[name] = map[string]any{}
	}
	m[name][harness] = v
}

// similar reports whether two texts share most of their vocabulary, the
// sign of a hand-made copy that has since been reworded or extended.
func similar(a, b []byte) bool {
	words := func(t []byte) map[string]bool {
		out := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(string(t)), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			if len(w) >= 4 {
				out[w] = true
			}
		}
		return out
	}
	wa, wb := words(a), words(b)
	if len(wa) < 10 || len(wb) < 10 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	return shared*10 >= 6*min(len(wa), len(wb))
}

func treeHash(dir string) string {
	h := sha256.New()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() || d.Name() == ".DS_Store" {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

func openCodeForm(s mcpServer) map[string]any {
	if s.Local {
		cmd := make([]any, len(s.Command))
		for i, w := range s.Command {
			cmd[i] = w
		}
		out := map[string]any{"type": "local", "command": cmd}
		if len(s.Env) > 0 {
			out["environment"] = stringsToAny(s.Env)
		}
		return out
	}
	out := map[string]any{"type": "remote", "url": s.URL}
	if len(s.Headers) > 0 {
		out["headers"] = stringsToAny(s.Headers)
	}
	return out
}

func stringsToAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var dollarEnv = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// fromClaudeMCP converts a Claude Code server to OpenCode's form; Claude's
// ${NAME} becomes {env:NAME}.
func fromClaudeMCP(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	conv := func(s string) string { return dollarEnv.ReplaceAllString(s, "{env:$1}") }
	s := mcpServer{}
	switch m["type"] {
	case "http", "sse", "streamable-http":
		url, _ := m["url"].(string)
		if url == "" {
			return nil, false
		}
		s.URL = conv(url)
		s.Headers = map[string]string{}
		if h, ok := m["headers"].(map[string]any); ok {
			for k, v := range h {
				if text, ok := v.(string); ok {
					s.Headers[k] = conv(text)
				}
			}
		}
	default:
		command, _ := m["command"].(string)
		if command == "" {
			return nil, false
		}
		s.Local = true
		s.Command = []string{conv(command)}
		if args, ok := m["args"].([]any); ok {
			for _, a := range args {
				if text, ok := a.(string); ok {
					s.Command = append(s.Command, conv(text))
				}
			}
		}
		s.Env = map[string]string{}
		if env, ok := m["env"].(map[string]any); ok {
			for k, v := range env {
				if text, ok := v.(string); ok {
					s.Env[k] = conv(text)
				}
			}
		}
	}
	return openCodeForm(s), true
}

// fromCodexMCP converts a Codex [mcp_servers.<name>] table.
func fromCodexMCP(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if url, ok := m["url"].(string); ok && url != "" {
		s := mcpServer{URL: url, Headers: map[string]string{}}
		if h, ok := m["http_headers"].(map[string]any); ok {
			for k, v := range h {
				if text, ok := v.(string); ok {
					s.Headers[k] = text
				}
			}
		}
		if h, ok := m["env_http_headers"].(map[string]any); ok {
			for k, v := range h {
				if name, ok := v.(string); ok {
					s.Headers[k] = "{env:" + name + "}"
				}
			}
		}
		if name, ok := m["bearer_token_env_var"].(string); ok && name != "" {
			s.Headers["Authorization"] = "Bearer {env:" + name + "}"
		}
		return openCodeForm(s), true
	}
	command, _ := m["command"].(string)
	if command == "" {
		return nil, false
	}
	s := mcpServer{Local: true, Command: []string{command}, Env: map[string]string{}}
	if args, ok := m["args"].([]any); ok {
		for _, a := range args {
			if text, ok := a.(string); ok {
				s.Command = append(s.Command, text)
			}
		}
	}
	if env, ok := m["env"].(map[string]any); ok {
		for k, v := range env {
			if text, ok := v.(string); ok {
				s.Env[k] = text
			}
		}
	}
	if names, ok := m["env_vars"].([]any); ok {
		for _, n := range names {
			if name, ok := n.(string); ok {
				s.Env[name] = "{env:" + name + "}"
			}
		}
	}
	return openCodeForm(s), true
}

func describeServer(v any) string {
	m, _ := v.(map[string]any)
	if url, ok := m["url"].(string); ok {
		return url
	}
	if cmd, ok := m["command"].([]any); ok && len(cmd) > 0 {
		words := make([]string, 0, len(cmd))
		for _, w := range cmd {
			words = append(words, w.(string))
		}
		return strings.Join(words, " ")
	}
	return ""
}

var secretName = regexp.MustCompile(`(?i)(token|secret|pass|pwd|pat\b|api[_-]?key|apikey|\bkey\b|_key|key_|auth|bearer|credential|private|dsn|cookie|session|signature|sig\b)`)
var secretValue = regexp.MustCompile(`[A-Za-z0-9_\-./+=]{24,}`)
var nonEnvChars = regexp.MustCompile(`[^A-Z0-9_]+`)

func envName(parts ...string) string {
	return strings.Trim(nonEnvChars.ReplaceAllString(strings.ToUpper(strings.Join(parts, "_")), "_"), "_")
}

func looksSecret(name, value string) bool {
	return value != "" && !hasSubstitution(value) && (secretName.MatchString(name) || secretValue.MatchString(value))
}

// redactServer replaces values that look secret with {env:NAME} references
// and reports each one, so no secret is copied into the source: environment
// values, headers, URL credentials and query values, and command flags.
func redactServer(name string, v any) (any, []AdoptSecret) {
	m, ok := v.(map[string]any)
	if !ok {
		return v, nil
	}
	out := cloneMap(m)
	var secrets []AdoptSecret
	note := func(where, env string) {
		secrets = append(secrets, AdoptSecret{Where: "mcp " + name + " " + where, Env: env})
	}
	for _, field := range []string{"environment", "headers"} {
		values, _ := out[field].(map[string]any)
		for _, k := range sortedKeys(values) {
			text, _ := values[k].(string)
			if !looksSecret(k, text) {
				continue
			}
			env := envName(name, k)
			if field == "environment" {
				env = envName(k)
			}
			replacement := "{env:" + env + "}"
			if rest, ok := strings.CutPrefix(text, "Bearer "); ok && rest != "" {
				replacement = "Bearer " + replacement
			}
			values[k] = replacement
			note(strings.TrimSuffix(field, "s")+" "+k, env)
		}
	}
	if raw, ok := out["url"].(string); ok && !hasSubstitution(raw) {
		if u, err := url.Parse(raw); err == nil {
			changed := false
			if u.User != nil {
				env := envName(name, "URL_CREDENTIALS")
				u.User = url.User("{env:" + env + "}")
				note("url credentials", env)
				changed = true
			}
			q := u.Query()
			for _, k := range sortedKeys(q) {
				if looksSecret(k, q.Get(k)) {
					env := envName(name, k)
					q.Set(k, "{env:"+env+"}")
					note("url parameter "+k, env)
					changed = true
				}
			}
			if changed {
				u.RawQuery = q.Encode()
				text := u.String()
				// url.String escapes the braces of the placeholders.
				text = strings.NewReplacer("%7B", "{", "%7D", "}", "%3A", ":").Replace(text)
				out["url"] = text
			}
		}
	}
	if cmd, ok := out["command"].([]any); ok {
		words := make([]any, len(cmd))
		copy(words, cmd)
		for i := 1; i < len(words); i++ {
			word, _ := words[i].(string)
			if flag, value, ok := strings.Cut(word, "="); ok && strings.HasPrefix(flag, "-") {
				if looksSecret(flag, value) {
					env := envName(name, strings.TrimLeft(flag, "-"))
					words[i] = flag + "={env:" + env + "}"
					note("argument "+flag, env)
				}
				continue
			}
			if strings.HasPrefix(word, "-") && secretName.MatchString(word) && i+1 < len(words) {
				if next, _ := words[i+1].(string); next != "" && !strings.HasPrefix(next, "-") && !hasSubstitution(next) {
					env := envName(name, strings.TrimLeft(word, "-"))
					words[i+1] = "{env:" + env + "}"
					note("argument "+word, env)
					i++
				}
				continue
			}
			if looksSecret("", word) && !strings.ContainsAny(word, "/@") {
				env := envName(name, "ARG", strconv.Itoa(i))
				words[i] = "{env:" + env + "}"
				note("argument "+strconv.Itoa(i), env)
			}
		}
		out["command"] = words
	}
	return out, secrets
}

// WriteAdoption creates a new OCP source at dir from an inventory: one
// ocp.yaml, the skills it found, and the first instruction text. dir must
// not exist or be empty.
func WriteAdoption(dir string, a Adoption, harnesses []string) ([]string, error) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, errors.New("target directory is not empty")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(filepath.Clean(dir)), ".ocp-adopt-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	doc := map[string]any{"version": 1}
	if len(harnesses) > 1 {
		doc["harnesses"] = harnesses
	}
	if len(a.Instructions) > 0 {
		if err := atomicFile(filepath.Join(tmp, "AGENTS.md"), append(a.Instructions[0].text, '\n'), 0o600); err != nil {
			return nil, err
		}
		doc["instructions"] = []string{"AGENTS.md"}
	}
	var skills, warnings []string
	for _, s := range a.Skills {
		if s.dir == "" {
			continue
		}
		skipped, err := copySkill(filepath.Join(tmp, "skills", s.Name), s.dir)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skill %s not copied: %v", s.Name, err))
			_ = os.RemoveAll(filepath.Join(tmp, "skills", s.Name))
			continue
		}
		for _, f := range skipped {
			warnings = append(warnings, fmt.Sprintf("skill %s: left out %s, which may hold a secret", s.Name, f))
		}
		skills = append(skills, s.Name)
	}
	sort.Strings(skills)
	if len(skills) > 0 {
		doc["skills"] = skills
	}
	if len(a.MCP) > 0 {
		servers := map[string]any{}
		for _, s := range a.MCP {
			servers[s.Name] = s.Value
		}
		doc["config"] = map[string]any{"mcp": map[string]any{"servers": servers}}
	}
	b, err := marshalYAML(doc)
	if err != nil {
		return nil, err
	}
	if err := atomicFile(filepath.Join(tmp, "ocp.yaml"), b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return warnings, os.Rename(tmp, dir)
}

var secretFile = regexp.MustCompile(`(?i)^(\.env.*|.*\.pem|.*\.key|.*credential.*|.*secret.*|id_rsa.*|id_ed25519.*)$`)

// copySkill copies a skill folder with private permissions, leaving out
// files that commonly hold secrets and dependency folders. Symlinks are
// refused, as the renderer would.
func copySkill(dest, src string) ([]string, error) {
	var skipped []string
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("contains a symlink: %s", rel)
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dest, rel), 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("contains a special file: %s", rel)
		}
		if secretFile.MatchString(d.Name()) {
			skipped = append(skipped, rel)
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o700
		}
		return os.WriteFile(filepath.Join(dest, rel), b, mode)
	})
	return skipped, err
}

// marshalYAML writes the document's keys in a readable order.
func marshalYAML(doc map[string]any) ([]byte, error) {
	var out []byte
	for _, k := range []string{"version", "harnesses", "instructions", "skills", "config"} {
		v, ok := doc[k]
		if !ok {
			continue
		}
		b, err := yaml.Marshal(map[string]any{k: v})
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}
