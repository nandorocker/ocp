package ocp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

func claudeJSONPath(p Paths) string { return filepath.Join(p.Home, ".claude.json") }

// readClaudeServers returns the user-scope MCP servers in ~/.claude.json. It
// only reads: the file belongs to Claude Code, which rewrites it constantly.
func readClaudeServers(p Paths) (map[string]any, error) {
	b, err := os.ReadFile(claudeJSONPath(p))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", claudeJSONPath(p), err)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	return servers, nil
}

// claudeEquivalent compares a stored server with what OCP wrote, ignoring
// fields Claude adds with empty values (such as "args": []), so a newer
// Claude that fills in defaults does not look like an edit.
func claudeEquivalent(stored, want any) bool {
	s, ok1 := stored.(map[string]any)
	w, ok2 := want.(map[string]any)
	if !ok1 || !ok2 {
		return nativeEqual(stored, want)
	}
	trimmed := map[string]any{}
	for k, v := range s {
		if _, wanted := w[k]; !wanted && emptyValue(v) {
			continue
		}
		trimmed[k] = v
	}
	return nativeEqual(trimmed, w)
}

func emptyValue(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case bool:
		return !v
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// planClaudeMCP works out the operations that turn the owned servers into
// the desired ones, and the ownership to record afterwards. A server OCP
// owns that the user removed is reinstalled if still wanted and otherwise
// forgotten; one the user changed blocks an update but is released (with a
// warning) when no longer wanted, so reset always works.
func planClaudeMCP(p Paths, owned map[string]nativeMCP, desired map[string]any) ([]nativeMCPOp, map[string]nativeMCP, []string, error) {
	if len(owned) == 0 && len(desired) == 0 {
		return nil, nil, nil, nil
	}
	current, err := readClaudeServers(p)
	if err != nil {
		return nil, nil, nil, err
	}
	names := map[string]bool{}
	for name := range owned {
		names[name] = true
	}
	for name := range desired {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var ops []nativeMCPOp
	var warnings []string
	next := map[string]nativeMCP{}
	for _, name := range sorted {
		cur, curExists := current[name]
		had, wasOwned := owned[name]
		want, wanted := desired[name]
		edited := wasOwned && curExists && !claudeEquivalent(cur, had.Installed)
		base, baseExists := cur, curExists
		if wasOwned {
			base, baseExists = had.Original, had.OriginalExists
		}
		if wanted {
			switch {
			case edited:
				return nil, nil, nil, fmt.Errorf("Claude Code MCP server %q was changed outside OCP; remove it (claude mcp remove -s user %s) and OCP will install its own again", name, name)
			case !wasOwned && curExists && !claudeEquivalent(cur, want):
				return nil, nil, nil, fmt.Errorf("Claude Code MCP server %q already exists and is not owned by OCP; remove it (claude mcp remove -s user %s) or make it identical", name, name)
			}
			next[name] = nativeMCP{Installed: want, Original: base, OriginalExists: baseExists}
			if !curExists || !claudeEquivalent(cur, want) {
				ops = append(ops, nativeMCPOp{Name: name, Remove: curExists, Before: cur, Add: want})
			}
			continue
		}
		// Owned before, not wanted now: put back what was there before OCP.
		switch {
		case !curExists:
			// Already gone; nothing to undo.
		case edited:
			warnings = append(warnings, fmt.Sprintf("claude: MCP server %q was changed outside OCP; left as it is", name))
		case baseExists:
			if !claudeEquivalent(cur, base) {
				ops = append(ops, nativeMCPOp{Name: name, Remove: true, Before: cur, Add: base})
			}
		default:
			ops = append(ops, nativeMCPOp{Name: name, Remove: true, Before: cur})
		}
	}
	if len(ops) > 0 {
		if _, err := claudeCommand(); err != nil {
			return nil, nil, nil, fmt.Errorf("changing Claude Code MCP servers needs the claude command: %w", err)
		}
	}
	if len(next) == 0 {
		next = nil
	}
	return ops, next, warnings, nil
}

// applyClaudeMCP performs one operation idempotently, so `ocp apply
// --recover` can replay it, and confirms the result by reading the file.
func applyClaudeMCP(p Paths, op nativeMCPOp) error {
	current, err := readClaudeServers(p)
	if err != nil {
		return err
	}
	cur, exists := current[op.Name]
	if op.Add != nil && exists && claudeEquivalent(cur, op.Add) {
		return nil
	}
	if exists {
		if !op.Remove || !claudeEquivalent(cur, op.Before) {
			return errors.New("changed during the update; refusing to replace it")
		}
		if err := runClaude(p.Home, "mcp", "remove", "-s", "user", op.Name); err != nil {
			return err
		}
	}
	if op.Add != nil {
		b, err := json.Marshal(op.Add)
		if err != nil {
			return err
		}
		// The JSON is an argument, not stdin: Claude refuses stdin when OCP
		// runs inside a Claude Code session. It holds no secret values.
		if err := runClaude(p.Home, "mcp", "add-json", "-s", "user", op.Name, string(b)); err != nil {
			return err
		}
	}
	after, err := readClaudeServers(p)
	if err != nil {
		return err
	}
	got, present := after[op.Name]
	switch {
	case op.Add == nil && present:
		return errors.New("still present after removal")
	case op.Add != nil && (!present || !claudeEquivalent(got, op.Add)):
		return fmt.Errorf("Claude stored a different value than OCP wrote: %v", got)
	}
	return nil
}

// runClaude runs the claude CLI against the given home, with auto-updates
// off so a background update cannot interfere.
var runClaude = func(home string, args ...string) error {
	path, err := claudeCommand()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "HOME="+home, "DISABLE_AUTOUPDATER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("claude %s: %w: %s", args[1], err, out)
	}
	return nil
}

// MCPChanges describes the planned Claude Code MCP operations for previews.
func (plan NativeInstallPlan) MCPChanges() []string {
	out := make([]string, 0, len(plan.MCP))
	for _, op := range plan.MCP {
		switch {
		case op.Add == nil:
			out = append(out, op.Name+" (remove)")
		case op.Remove:
			out = append(out, op.Name+" (update)")
		default:
			out = append(out, op.Name+" (add)")
		}
	}
	return out
}
