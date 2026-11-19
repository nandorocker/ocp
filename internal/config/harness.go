package config

import (
	"fmt"
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
