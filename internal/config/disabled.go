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
	for _, ext := range []string{".ts", ".js", ".mjs"} {
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
	for _, ext := range []string{".ts", ".js", ".mjs"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
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
	for _, s := range p.Skills {
		if name := s.Name(); off[name] {
			matched[name] = true
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
			if entry, ok := item.(string); ok && off[pluginName(entry)] {
				matched[pluginName(entry)] = true
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
