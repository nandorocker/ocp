package ocp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

func claudeJSONPath(p Paths) string { return filepath.Join(p.Home, ".claude.json") }

// readClaudeServers returns the user-scope MCP servers in ~/.claude.json. It
// only reads: the file belongs to Claude Code, which rewrites it constantly.
func readClaudeServers(p Paths) (map[string]any, error) {
	b, exists, err := nativeRead(claudeJSONPath(p))
	if err != nil || !exists {
		return map[string]any{}, err
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

// planClaudeMCP works out the operations that turn the owned servers into
// the desired ones, and the ownership to record afterwards.
func planClaudeMCP(p Paths, owned map[string]nativeMCP, desired map[string]any) ([]nativeMCPOp, map[string]nativeMCP, error) {
	if len(owned) == 0 && len(desired) == 0 {
		return nil, nil, nil
	}
	current, err := readClaudeServers(p)
	if err != nil {
		return nil, nil, err
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
	next := map[string]nativeMCP{}
	for _, name := range sorted {
		cur, curExists := current[name]
		had, wasOwned := owned[name]
		want, wanted := desired[name]
		if wasOwned && (!curExists || !nativeEqual(cur, had.Installed)) {
			return nil, nil, fmt.Errorf("Claude Code MCP server %q was edited outside OCP; restore it or remove it from OCP's state with `ocp reset --harness claude`", name)
		}
		base, baseExists := cur, curExists
		if wasOwned {
			base, baseExists = had.Original, had.OriginalExists
		}
		if wanted {
			if !wasOwned && curExists && !nativeEqual(cur, want) {
				return nil, nil, fmt.Errorf("Claude Code MCP server %q already exists and is not owned by OCP; remove it (claude mcp remove -s user %s) or make it identical", name, name)
			}
			next[name] = nativeMCP{Installed: want, Original: base, OriginalExists: baseExists}
			if !curExists || !nativeEqual(cur, want) {
				ops = append(ops, nativeMCPOp{Name: name, Remove: curExists, Add: want})
			}
			continue
		}
		// Owned before, not wanted now: put back what was there before OCP.
		if baseExists {
			if !nativeEqual(cur, base) {
				ops = append(ops, nativeMCPOp{Name: name, Remove: true, Add: base})
			}
		} else {
			ops = append(ops, nativeMCPOp{Name: name, Remove: true})
		}
	}
	if len(ops) > 0 {
		if _, err := claudeCommand(); err != nil {
			return nil, nil, fmt.Errorf("changing Claude Code MCP servers needs the claude command: %w", err)
		}
	}
	if len(next) == 0 {
		next = nil
	}
	return ops, next, nil
}

// applyClaudeMCP performs one operation idempotently, so `ocp apply
// --recover` can replay it, and confirms the result by reading the file.
func applyClaudeMCP(p Paths, op nativeMCPOp) error {
	current, err := readClaudeServers(p)
	if err != nil {
		return err
	}
	cur, exists := current[op.Name]
	if op.Add != nil && exists && nativeEqual(cur, op.Add) {
		return nil
	}
	if exists {
		if !op.Remove {
			return errors.New("appeared during the update; refusing to replace it")
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
	case op.Add != nil && (!present || !nativeEqual(got, op.Add)):
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
	cmd := exec.Command(path, args...)
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
