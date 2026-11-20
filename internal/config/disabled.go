package config

import (
	"path"
	"strings"
)

// pluginName is how a plugin is named in `disabled:`: its file name without
// extension for local files and paths, or the package name without a
// version for npm packages.
func pluginName(entry string) string {
	local := strings.Contains(entry, "/") && !strings.HasPrefix(entry, "@")
	for _, ext := range pluginExtensions {
		local = local || strings.HasSuffix(entry, ext)
	}
	if !local {
		// npm package, possibly scoped and versioned: @scope/pkg@1.2.3
		if i := strings.LastIndex(entry, "@"); i > 0 {
			return entry[:i]
		}
		return entry
	}
	name := path.Base(strings.TrimRight(entry, "/"))
	for _, ext := range pluginExtensions {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// pluginExtensions are the local plugin file types OCP accepts.
var pluginExtensions = []string{".ts", ".mts", ".cts", ".js", ".mjs", ".cjs"}

// configPluginName names an item of config.plugin(s): a string, or the first
// element of a [name, options] pair.
func configPluginName(item any) (string, bool) {
	switch v := item.(type) {
	case string:
		return pluginName(v), true
	case []any:
		if len(v) > 0 {
			if name, ok := v[0].(string); ok {
				return pluginName(name), true
			}
		}
	}
	return "", false
}

// applyDisabled removes skills and plugins named in p.Disabled, from the
// profile's lists and from OpenCode's config.plugin(s), and records names
// that matched nothing.
func applyDisabled(p *Profile) {
	if len(p.Disabled) == 0 {
		return
	}
	off := map[string]bool{}
	for _, name := range p.Disabled {
		off[name] = true
	}
	matched := map[string]bool{}
	skills := p.Skills[:0:0]
	p.DisabledSkills = nil
	for _, s := range p.Skills {
		if name := s.Name(); off[name] {
			matched[name] = true
			p.DisabledSkills = append(p.DisabledSkills, s)
			continue
		}
		skills = append(skills, s)
	}
	p.Skills = skills
	plugins := p.Plugins[:0:0]
	for _, entry := range p.Plugins {
		if name := pluginName(entry); off[name] {
			matched[name] = true
			continue
		}
		plugins = append(plugins, entry)
	}
	p.Plugins = plugins
	for _, key := range []string{"plugin", "plugins"} {
		list, ok := p.Config[key].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(list))
		for _, item := range list {
			if name, ok := configPluginName(item); ok && off[name] {
				matched[name] = true
				continue
			}
			kept = append(kept, item)
		}
		p.Config[key] = kept
	}
	p.UnmatchedDisabled = nil
	for _, name := range p.Disabled {
		if !matched[name] {
			p.UnmatchedDisabled = append(p.UnmatchedDisabled, name)
		}
	}
}
