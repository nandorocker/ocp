package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnabledHarnesses returns the declared harnesses, defaulting to OpenCode.
func (d *Document) EnabledHarnesses() []string {
	if len(d.Harnesses) == 0 {
		return []string{"opencode"}
	}
	return append([]string(nil), d.Harnesses...)
}

func knownHarness(name string) bool {
	return name == "opencode" || name == "claude" || name == "codex"
}

func harnesses(node *yaml.Node) ([]string, error) {
	values, err := stringsList(node, "harnesses")
	if err != nil || node == nil {
		return values, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("harnesses must not be empty")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !knownHarness(value) {
			return nil, fmt.Errorf("unknown harness %q", value)
		}
		if seen[value] {
			return nil, fmt.Errorf("duplicate harness %q", value)
		}
		seen[value] = true
	}
	return values, nil
}

func validateModel(value any) error {
	switch value := value.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return nil
		}
	case map[string]any:
		if len(value) == 0 {
			break
		}
		for harness, model := range value {
			if !knownHarness(harness) {
				return fmt.Errorf("unknown model harness %q", harness)
			}
			text, ok := model.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("model for %s must be a non-empty string", harness)
			}
		}
		return nil
	}
	return fmt.Errorf("model must be a non-empty string or non-empty harness mapping")
}

// ValidateModel checks an explicit model declaration, including agent frontmatter.
func ValidateModel(value any) error { return validateModel(value) }

func modelValue(node *yaml.Node, label string) (any, error) {
	if node.Kind == yaml.MappingNode {
		if _, err := mapping(node, label, "opencode", "claude", "codex"); err != nil {
			return nil, err
		}
	}
	value, err := nativeValue(node)
	if err == nil {
		err = validateModel(value)
	}
	if err != nil {
		return nil, fmt.Errorf("%s %s", label, strings.TrimPrefix(err.Error(), "model "))
	}
	return value, nil
}

func validateNativeModels(node *yaml.Node, label string) error {
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if key == "model" {
			if _, err := modelValue(value, label+".model"); err != nil {
				return err
			}
		}
		if key == "agent" && value.Kind == yaml.MappingNode {
			for j := 0; j < len(value.Content); j += 2 {
				agent := value.Content[j+1]
				if agent.Kind == yaml.MappingNode {
					if err := validateNativeModels(agent, label+".agent."+value.Content[j].Value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// ResolveModel selects a harness-specific model without substituting providers.
// A nil value leaves the harness's default model unchanged.
func ResolveModel(value any, harness string) (string, error) {
	if !knownHarness(harness) {
		return "", fmt.Errorf("unsupported harness %q", harness)
	}
	if value == nil {
		return "", nil
	}
	if err := validateModel(value); err != nil {
		return "", err
	}
	explicit := false
	if models, ok := value.(map[string]any); ok {
		var exists bool
		value, exists = models[harness]
		if !exists {
			return "", fmt.Errorf("model mapping has no entry for %s", harness)
		}
		explicit = true
	}
	model := value.(string)
	if harness == "opencode" {
		return model, nil
	}
	if strings.ContainsAny(model, " \t\r\n#") {
		return "", fmt.Errorf("model %q contains non-native syntax for %s; declare a native harness model", model, harness)
	}
	provider := "openai"
	if harness == "claude" {
		provider = "anthropic"
	}
	if prefix, id, qualified := strings.Cut(model, "/"); qualified {
		if prefix == provider && strings.TrimSpace(id) != "" && !strings.Contains(id, "/") {
			other := "claude"
			if harness == "claude" {
				other = "codex"
			}
			if !nativeModel(id, other) {
				return id, nil
			}
		}
	} else {
		other := "claude"
		if harness == "claude" {
			other = "codex"
		}
		if !nativeModel(model, other) && (explicit || nativeModel(model, harness)) {
			return model, nil
		}
	}
	return "", fmt.Errorf("model %q is not compatible with %s; declare an explicit harness model", model, harness)
}

func nativeModel(model, harness string) bool {
	if harness == "codex" {
		return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4")
	}
	return model == "opus" || model == "sonnet" || model == "haiku" || strings.HasPrefix(model, "claude-")
}

// Native settings blocks. `claude:` and `codex:` carry settings in each
// harness's own format, for things that differ per harness, such as model
// providers. Only keys OCP knows how to own safely are accepted.
var harnessBlockKeys = map[string][]string{
	"claude": {"apiKeyHelper", "env", "model"},
	"codex":  {"model", "model_provider", "model_providers", "model_reasoning_effort"},
}

// codexBuiltinProviders cannot be redefined in Codex.
var codexBuiltinProviders = map[string]bool{"openai": true, "ollama": true, "lmstudio": true}

var substitutionSyntax = regexp.MustCompile(`\{(env|file):[^}]*\}`)

func harnessBlocks(values map[string]*yaml.Node, label string) (map[string]any, map[string]any, error) {
	claude, err := harnessBlock(values["claude"], label+"claude", "claude")
	if err != nil {
		return nil, nil, err
	}
	codex, err := harnessBlock(values["codex"], label+"codex", "codex")
	if err != nil {
		return nil, nil, err
	}
	return claude, codex, nil
}

func harnessBlock(node *yaml.Node, label, harness string) (map[string]any, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", label)
	}
	value, err := nativeValue(node)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	block := value.(map[string]any)
	allowed := harnessBlockKeys[harness]
	for _, key := range sortedKeys(block) {
		if !slices.Contains(allowed, key) {
			return nil, fmt.Errorf("%s: unsupported key %q; supported: %s", label, key, strings.Join(allowed, ", "))
		}
		v := block[key]
		if err := rejectNulls(v, label+"."+key); err != nil {
			return nil, err
		}
		switch key {
		case "apiKeyHelper":
			// A shell command; {ocp} and substitutions are evaluated by the
			// `ocp secret` helper it may call, at run time.
			if s, ok := v.(string); !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("%s.%s must be a non-empty string", label, key)
			}
		case "model", "model_provider", "model_reasoning_effort":
			if s, ok := v.(string); !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("%s.%s must be a non-empty string", label, key)
			}
		case "env":
			env, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s.env must be a mapping of strings", label)
			}
			for name, value := range env {
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("%s.env.%s must be a string", label, name)
				}
				if substitutionSyntax.MatchString(s) {
					return nil, fmt.Errorf("%s.env.%s: {env:} and {file:} are not evaluated here, so the secret would be written into settings.json; set apiKeyHelper: \"{ocp} secret print '%s'\" instead", label, name, s)
				}
			}
		case "model_providers":
			providers, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s.model_providers must be a mapping", label)
			}
			for id, spec := range providers {
				if codexBuiltinProviders[id] {
					return nil, fmt.Errorf("%s.model_providers.%s: Codex does not allow redefining the built-in %s provider; choose another id", label, id, id)
				}
				fields, ok := spec.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s.model_providers.%s must be a mapping", label, id)
				}
				if api, set := fields["wire_api"]; set && api != "responses" {
					return nil, fmt.Errorf("%s.model_providers.%s.wire_api: Codex only supports \"responses\"; put a proxy that speaks the Responses API in front of chat-only providers", label, id)
				}
				if substitutionSyntax.MatchString(fmt.Sprint(fields)) {
					return nil, fmt.Errorf("%s.model_providers.%s: {env:} and {file:} are not evaluated by Codex; use env_key: NAME to read the key from an environment variable", label, id)
				}
			}
		}
	}
	return block, nil
}

func rejectNulls(v any, label string) error {
	switch v := v.(type) {
	case nil:
		return fmt.Errorf("%s must not be null", label)
	case map[string]any:
		for k, e := range v {
			if err := rejectNulls(e, label+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range v {
			if err := rejectNulls(e, label); err != nil {
				return err
			}
		}
	}
	return nil
}
