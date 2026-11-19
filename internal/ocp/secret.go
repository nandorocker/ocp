package ocp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandSubstitutions evaluates OpenCode's {env:NAME} and {file:path}
// placeholders at the moment a secret is needed. File contents are trimmed of
// surrounding whitespace, as OpenCode does; `~/` means the home directory.
func ExpandSubstitutions(template string) (string, error) {
	var firstErr error
	out := substitution.ReplaceAllStringFunc(template, func(match string) string {
		kind, arg, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(match, "{"), "}"), ":")
		switch kind {
		case "env":
			value, ok := os.LookupEnv(arg)
			if !ok && firstErr == nil {
				firstErr = fmt.Errorf("environment variable %s is not set", arg)
			}
			return value
		case "file":
			path := arg
			if rest, ok := strings.CutPrefix(path, "~/"); ok {
				home, err := os.UserHomeDir()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					return ""
				}
				path = filepath.Join(home, rest)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("read secret file: %w", err)
				}
				return ""
			}
			return strings.TrimSpace(string(b))
		}
		return match
	})
	return out, firstErr
}
