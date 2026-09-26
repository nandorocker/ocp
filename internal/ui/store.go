// Package ui provides the source-backed data model used by the web UI.
package ui

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nando/ocp/internal/config"
	ocpcore "github.com/nando/ocp/internal/ocp"
	"gopkg.in/yaml.v3"
)

type Store struct{ Source string }
type Snapshot struct {
	Revision      string        `json:"revision"`
	Editable      bool          `json:"editable"`
	MigrationHint string        `json:"migrationHint,omitempty"`
	Profiles      []ProfileView `json:"profiles"`
	AgentFiles    []AgentFile   `json:"agentFiles"`
}
type ProfileView struct {
	Name         string        `json:"name"`
	Extends      string        `json:"extends,omitempty"`
	Chain        []string      `json:"chain"`
	Model        string        `json:"model,omitempty"`
	ModelOrigin  string        `json:"modelOrigin,omitempty"`
	DirectModel  string        `json:"directModel,omitempty"`
	Agents       []AgentView   `json:"agents"`
	DirectAgents []DirectAgent `json:"directAgents"`
}
type AgentView struct {
	Name        string `json:"name"`
	File        string `json:"file"`
	FileOrigin  string `json:"fileOrigin"`
	Model       string `json:"model,omitempty"`
	ModelOrigin string `json:"modelOrigin,omitempty"`
	SourceModel string `json:"sourceModel,omitempty"`
}
type DirectAgent struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Model string `json:"model,omitempty"`
}
type AgentFile struct {
	Name    string   `json:"name"`
	Content string   `json:"content"`
	Usage   []string `json:"usage"`
}
type ProfileUpdate struct {
	Extends string
	Model   string
	Agents  []DirectAgent
}
type ConflictError struct{}

func (*ConflictError) Error() string { return "source has changed" }

type layout struct {
	source       string
	root         []byte
	profiles     map[string][]byte
	profileNames map[string]string
	agents       map[string][]byte
	inline       bool
}

func (s Store) Snapshot() (Snapshot, error) {
	l, err := s.load()
	if err != nil {
		return Snapshot{}, err
	}
	doc, err := config.Load(filepath.Join(s.Source, config.FileName))
	if err != nil {
		return Snapshot{}, err
	}
	resolved, err := doc.ResolveFromDir(s.Source)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Revision: revision(l), Editable: !l.inline}
	if l.inline {
		out.MigrationHint = "run ocp setup --migrate-profiles to edit profiles"
	}
	for _, p := range resolved {
		v, err := profileView(p, l, resolved)
		if err != nil {
			return Snapshot{}, err
		}
		out.Profiles = append(out.Profiles, v)
	}
	usage, err := usages(l)
	if err != nil {
		return Snapshot{}, err
	}
	for name, content := range l.agents {
		out.AgentFiles = append(out.AgentFiles, AgentFile{Name: name, Content: string(content), Usage: usage[name]})
	}
	sort.Slice(out.AgentFiles, func(i, j int) bool { return out.AgentFiles[i].Name < out.AgentFiles[j].Name })
	return out, nil
}

func (s Store) CreateProfile(rev, name, extends, duplicate string) (Snapshot, error) {
	if err := valid(name); err != nil {
		return Snapshot{}, err
	}
	l, err := s.checked(rev)
	if err != nil {
		return Snapshot{}, err
	}
	if l.inline {
		return Snapshot{}, errors.New("inline profiles are not editable")
	}
	if _, ok := l.profileNames[name]; ok {
		return Snapshot{}, fmt.Errorf("profile %q already exists", name)
	}
	var b []byte
	if duplicate != "" {
		f, ok := l.profileNames[duplicate]
		if !ok {
			return Snapshot{}, fmt.Errorf("duplicate profile %q is not explicit", duplicate)
		}
		b = append([]byte(nil), l.profiles[f]...)
	} else if extends != "" {
		if err := valid(extends); err != nil {
			return Snapshot{}, err
		}
		b = []byte("extends: " + extends + "\n")
	} else {
		b = []byte("{}\n")
	}
	if len(l.profiles) == 0 && name != "default" {
		l.profiles["default.yaml"] = []byte("{}\n")
	}
	l.profiles[name+".yaml"] = b
	if err = s.publishProfiles(l); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot()
}
func (s Store) UpdateProfile(rev, name string, u ProfileUpdate) (Snapshot, error) {
	l, err := s.checked(rev)
	if err != nil {
		return Snapshot{}, err
	}
	if err := applyProfileUpdate(&l, name, u); err != nil {
		return Snapshot{}, err
	}
	if err = s.publishProfiles(l); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot()
}

// PreviewProfile applies a profile update and resolves it without writing source files.
func (s Store) PreviewProfile(rev, name string, u ProfileUpdate) (ProfileView, error) {
	l, err := s.checked(rev)
	if err != nil {
		return ProfileView{}, err
	}
	if err := applyProfileUpdate(&l, name, u); err != nil {
		return ProfileView{}, err
	}
	resolved, err := resolveLayout(l)
	if err != nil {
		return ProfileView{}, err
	}
	for _, p := range resolved {
		if p.Name == name {
			return profileView(p, l, resolved)
		}
	}
	return ProfileView{}, fmt.Errorf("profile %q not found", name)
}

func applyProfileUpdate(l *layout, name string, u ProfileUpdate) error {
	if l.inline {
		return errors.New("inline profiles are not editable")
	}
	f, ok := l.profileNames[name]
	if !ok && (name != "default" || len(l.profiles) != 0) {
		return fmt.Errorf("profile %q is not explicit", name)
	}
	if !ok {
		f = "default.yaml"
		l.profiles[f] = []byte("{}\n")
		l.profileNames[name] = f
	}
	n, err := mappingNode(l.profiles[f])
	if err != nil {
		return err
	}
	if u.Extends != "" {
		if err := valid(u.Extends); err != nil {
			return err
		}
		set(n, "extends", scalar(u.Extends))
	} else {
		remove(n, "extends")
	}
	setModel(n, u.Model)
	for _, agent := range u.Agents {
		if agent.File != "" {
			if err := agentFile(agent.File); err == nil {
				if _, ok := l.agents[agent.File]; !ok {
					return fmt.Errorf("agent library file %q not found", agent.File)
				}
			} else if old := agentNode(n, agent.Name); old == nil || browserFile(valueOf(old, "file")) != agent.File {
				return err
			}
		}
	}
	if err := setAgents(n, u.Agents); err != nil {
		return err
	}
	b, err := encode(n)
	if err != nil {
		return err
	}
	l.profiles[f] = b
	return nil
}
func (s Store) DeleteProfile(rev, name string) (Snapshot, error) {
	l, err := s.checked(rev)
	if err != nil {
		return Snapshot{}, err
	}
	if l.inline {
		return Snapshot{}, errors.New("inline profiles are not editable")
	}
	f, ok := l.profileNames[name]
	if !ok {
		return Snapshot{}, fmt.Errorf("profile %q is not explicit", name)
	}
	if len(l.profiles) == 1 {
		return Snapshot{}, errors.New("cannot delete last profile")
	}
	for other, pf := range l.profileNames {
		if other == name {
			continue
		}
		n, e := mappingNode(l.profiles[pf])
		if e != nil {
			return Snapshot{}, e
		}
		if v := get(n, "extends"); v != nil && v.Value == name {
			return Snapshot{}, fmt.Errorf("profile %q extends %q", other, name)
		}
	}
	delete(l.profiles, f)
	if err = s.publishProfiles(l); err != nil {
		return Snapshot{}, err
	}
	if err = safeDir(filepath.Join(s.Source, "profiles")); err != nil {
		return Snapshot{}, err
	}
	if info, e := os.Lstat(filepath.Join(s.Source, "profiles", f)); e != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Snapshot{}, fmt.Errorf("unsafe profile destination %q", f)
	}
	if err = os.Remove(filepath.Join(s.Source, "profiles", f)); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot()
}
func (s Store) CreateAgent(rev, name, content string) (Snapshot, error) {
	if err := agentFile(name); err != nil {
		return Snapshot{}, err
	}
	l, err := s.checked(rev)
	if err != nil {
		return Snapshot{}, err
	}
	if l.inline {
		return Snapshot{}, errors.New("inline profiles are not editable")
	}
	if _, _, err := frontmatterModel([]byte(content)); err != nil {
		return Snapshot{}, err
	}
	if _, ok := l.agents[name]; ok {
		return Snapshot{}, fmt.Errorf("agent %q already exists", name)
	}
	l.agents[name] = []byte(content)
	if err = s.publishAgent(l, name); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot()
}
func (s Store) UpdateAgent(rev, name, content string) (Snapshot, error) {
	if err := agentFile(name); err != nil {
		return Snapshot{}, err
	}
	l, err := s.checked(rev)
	if err != nil {
		return Snapshot{}, err
	}
	if l.inline {
		return Snapshot{}, errors.New("inline profiles are not editable")
	}
	if _, _, err := frontmatterModel([]byte(content)); err != nil {
		return Snapshot{}, err
	}
	if _, ok := l.agents[name]; !ok {
		return Snapshot{}, fmt.Errorf("agent %q not found", name)
	}
	l.agents[name] = []byte(content)
	if err = s.publishAgent(l, name); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot()
}
func (s Store) DeleteAgent(rev, name string) (Snapshot, error) {
	if err := agentFile(name); err != nil {
		return Snapshot{}, err
	}
	l, err := s.checked(rev)
	if err != nil {
		return Snapshot{}, err
	}
	if l.inline {
		return Snapshot{}, errors.New("inline profiles are not editable")
	}
	if _, ok := l.agents[name]; !ok {
		return Snapshot{}, fmt.Errorf("agent %q not found", name)
	}
	u, err := usages(l)
	if err != nil {
		return Snapshot{}, err
	}
	if len(u[name]) > 0 {
		return Snapshot{}, fmt.Errorf("agent %q is referenced by %s", name, strings.Join(u[name], ", "))
	}
	if err := safeDir(filepath.Join(s.Source, "agents")); err != nil {
		return Snapshot{}, err
	}
	if info, e := os.Lstat(filepath.Join(s.Source, "agents", name)); e != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Snapshot{}, fmt.Errorf("unsafe agent destination %q", name)
	}
	if err := os.Remove(filepath.Join(s.Source, "agents", name)); err != nil {
		return Snapshot{}, err
	}
	return s.Snapshot()
}

func (s Store) checked(rev string) (layout, error) {
	l, e := s.load()
	if e != nil {
		return l, e
	}
	if revision(l) != rev {
		return l, &ConflictError{}
	}
	return l, nil
}
func (s Store) load() (layout, error) {
	l := layout{source: s.Source, profiles: map[string][]byte{}, profileNames: map[string]string{}, agents: map[string][]byte{}}
	var err error
	l.root, err = os.ReadFile(filepath.Join(s.Source, config.FileName))
	if err != nil {
		return l, err
	}
	n, err := mappingNode(l.root)
	if err != nil {
		return l, err
	}
	l.inline = get(n, "profiles") != nil
	if err = readFlat(filepath.Join(s.Source, "profiles"), func(name string) bool { return filepath.Ext(name) == ".yaml" || filepath.Ext(name) == ".yml" }, func(name string, b []byte) {
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		if err == nil {
			if e := valid(base); e != nil {
				err = e
			} else if old, ok := l.profileNames[base]; ok {
				err = fmt.Errorf("duplicate profile %q from %q and %q", base, old, name)
			} else {
				l.profiles[name] = b
				l.profileNames[base] = name
			}
		}
	}); err != nil {
		return l, err
	}
	err = readFlat(filepath.Join(s.Source, "agents"), func(name string) bool { return strings.HasSuffix(name, ".md") }, func(name string, b []byte) {
		if err == nil {
			if e := agentFile(name); e != nil {
				err = e
			} else {
				l.agents[name] = b
			}
		}
	})
	return l, err
}
func readFlat(dir string, keep func(string) bool, add func(string, []byte)) error {
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%q must be a directory, not a symlink", dir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	es, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range es {
		if !keep(e.Name()) {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q must be a regular file", e.Name())
		}
		i, x := e.Info()
		if x != nil {
			return x
		}
		if !i.Mode().IsRegular() {
			return fmt.Errorf("%q must be a regular file", e.Name())
		}
		b, x := os.ReadFile(filepath.Join(dir, e.Name()))
		if x != nil {
			return x
		}
		add(e.Name(), b)
	}
	return nil
}
func revision(l layout) string {
	h := sha256.New()
	write := func(n string, b []byte) { io.WriteString(h, n+"\x00"); h.Write(b); h.Write([]byte{0}) }
	write(config.FileName, l.root)
	ns := make([]string, 0, len(l.profiles))
	for n := range l.profiles {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	for _, n := range ns {
		write("profiles/"+n, l.profiles[n])
	}
	ns = ns[:0]
	for n := range l.agents {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	for _, n := range ns {
		write("agents/"+n, l.agents[n])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
func valid(n string) error {
	if n == "" || n == "." || n == ".." {
		return fmt.Errorf("invalid name %q", n)
	}
	for _, r := range n {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("invalid name %q", n)
		}
	}
	return nil
}
func agentFile(n string) error {
	if filepath.Base(n) != n || !strings.HasSuffix(n, ".md") || len(n) == 3 {
		return fmt.Errorf("invalid agent filename %q", n)
	}
	return valid(strings.TrimSuffix(n, ".md"))
}

func (s Store) publishProfiles(l layout) error {
	if err := validate(l); err != nil {
		return err
	}
	dir := filepath.Join(s.Source, "profiles")
	if err := safeDir(dir); err != nil {
		return err
	}
	for n, b := range l.profiles {
		if err := atomic(filepath.Join(dir, n), b); err != nil {
			return err
		}
	}
	return nil
}
func (s Store) publishAgent(l layout, name string) error {
	if err := safeDir(filepath.Join(s.Source, "agents")); err != nil {
		return err
	}
	return atomic(filepath.Join(s.Source, "agents", name), l.agents[name])
}
func safeDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dir, 0700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%q must be a directory, not a symlink", dir)
	}
	return nil
}
func atomic(path string, b []byte) error {
	if err := safeDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ocp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Dir(path)); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("unsafe destination directory %q", filepath.Dir(path))
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink %q", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, path)
}
func validate(l layout) error {
	_, err := resolveLayout(l)
	return err
}

func resolveLayout(l layout) ([]config.Profile, error) {
	d, err := os.MkdirTemp("", "ocp-ui-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(d)
	return resolveLayoutAt(d, l)
}
func resolveLayoutAt(d string, l layout) ([]config.Profile, error) {
	if err := atomic(filepath.Join(d, config.FileName), l.root); err != nil {
		return nil, err
	}
	for n, b := range l.profiles {
		if err := atomic(filepath.Join(d, "profiles", n), b); err != nil {
			return nil, err
		}
	}
	doc, err := config.Load(filepath.Join(d, config.FileName))
	if err != nil {
		return nil, err
	}
	return doc.ResolveFromDir(d)
}

func mappingNode(b []byte) (*yaml.Node, error) {
	var d yaml.Node
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if len(d.Content) != 1 || d.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("YAML must be a mapping")
	}
	return d.Content[0], nil
}
func encode(n *yaml.Node) ([]byte, error) { return yaml.Marshal(n) }
func get(n *yaml.Node, k string) *yaml.Node {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == k {
			return n.Content[i+1]
		}
	}
	return nil
}
func set(n *yaml.Node, k string, v *yaml.Node) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == k {
			n.Content[i+1] = v
			return
		}
	}
	n.Content = append(n.Content, scalar(k), v)
}
func remove(n *yaml.Node, k string) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == k {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
	}
}
func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
func setModel(n *yaml.Node, m string) {
	c := get(n, "config")
	if c == nil && m != "" {
		c = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		set(n, "config", c)
	}
	if c != nil {
		if m == "" {
			remove(c, "model")
		} else {
			set(c, "model", scalar(m))
		}
	}
}
func setAgents(n *yaml.Node, ds []DirectAgent) error {
	old := get(n, "agents")
	if len(ds) == 0 {
		remove(n, "agents")
		return nil
	}
	if old == nil {
		old = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		set(n, "agents", old)
	}
	if old.Kind != yaml.MappingNode {
		return errors.New("agents must be a mapping")
	}
	want := map[string]DirectAgent{}
	for _, d := range ds {
		if err := valid(d.Name); err != nil {
			return err
		}
		want[d.Name] = d
	}
	var out []*yaml.Node
	names := make([]string, 0, len(want))
	for x := range want {
		names = append(names, x)
	}
	sort.Strings(names)
	for _, name := range names {
		d := want[name]
		v := get(old, name)
		if v == nil {
			v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		if v.Kind != yaml.MappingNode {
			return fmt.Errorf("agent %q must be a mapping", name)
		}
		if d.File == "" {
			remove(v, "file")
		} else if err := agentFile(d.File); err == nil {
			set(v, "file", scalar("./agents/"+d.File))
		} else if browserFile(valueOf(v, "file")) != d.File {
			return err
		}
		remove(v, "model")
		setModel(v, d.Model)
		out = append(out, scalar(name), v)
	}
	old.Content = out
	return nil
}

func profileView(p config.Profile, l layout, _ []config.Profile) (ProfileView, error) {
	chain, nodes, err := profileChain(p.Name, l)
	if err != nil {
		return ProfileView{}, err
	}
	v := ProfileView{Name: p.Name, Chain: chain, Agents: []AgentView{}, DirectAgents: []DirectAgent{}}
	if len(chain) > 0 {
		v.DirectAgents = directAgents(nodes[len(nodes)-1])
	}
	if len(chain) > 0 {
		n := nodes[len(nodes)-1]
		if x := get(n, "extends"); x != nil {
			v.Extends = x.Value
		}
		v.DirectModel = modelOf(n)
	}
	for i, n := range nodes {
		if m := modelOf(n); m != "" {
			v.Model = m
			if i == 0 {
				v.ModelOrigin = "shared"
			} else {
				v.ModelOrigin = chain[i-1]
			}
		}
	}
	for name, a := range p.Agents {
		av := AgentView{Name: name, File: browserFile(a.File), Model: v.Model, ModelOrigin: v.ModelOrigin}
		for i, n := range nodes {
			if an := agentNode(n, name); an != nil {
				if x := get(an, "file"); x != nil {
					av.File = browserFile(x.Value)
					if i == 0 {
						av.FileOrigin = "shared"
					} else {
						av.FileOrigin = chain[i-1]
					}
				}
			}
		}
		if av.FileOrigin == "" {
			av.FileOrigin = "shared"
		}
		if file, ok := sourceAgentFile(a.File); ok {
			b, exists := l.agents[file]
			if exists {
				m, has, e := frontmatterModel(b)
				if e != nil {
					return ProfileView{}, fmt.Errorf("agent %q: %w", av.File, e)
				}
				if has {
					av.Model = m
					av.ModelOrigin = filepath.Base(av.File)
					av.SourceModel = m
				}
			}
		} else if a.File != "" {
			m, has, e := ocpcore.AgentSourceModel(l.source, a.File)
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				return ProfileView{}, fmt.Errorf("agent %q: %w", av.File, e)
			}
			if has {
				av.SourceModel = m
				av.Model = m
				av.ModelOrigin = filepath.Base(av.File)
			}
		}
		for i, n := range nodes {
			if an := agentNode(n, name); an != nil {
				if m := agentModelOf(an); m != "" {
					av.Model = m
					if i == 0 {
						av.ModelOrigin = "shared"
					} else {
						av.ModelOrigin = chain[i-1]
					}
				}
			}
		}
		v.Agents = append(v.Agents, av)
	}
	sort.Slice(v.Agents, func(i, j int) bool { return v.Agents[i].Name < v.Agents[j].Name })
	return v, nil
}
func profileChain(name string, l layout) ([]string, []*yaml.Node, error) {
	root, e := mappingNode(l.root)
	if e != nil {
		return nil, nil, e
	}
	if _, ok := l.profileNames[name]; !ok {
		return []string{}, []*yaml.Node{root}, nil
	}
	var names []string
	seen := map[string]bool{}
	for n := name; n != ""; {
		if seen[n] {
			return nil, nil, errors.New("profile inheritance cycle")
		}
		seen[n] = true
		names = append([]string{n}, names...)
		f := l.profileNames[n]
		x, e := mappingNode(l.profiles[f])
		if e != nil {
			return nil, nil, e
		}
		q := get(x, "extends")
		if q == nil {
			break
		}
		n = q.Value
	}
	nodes := []*yaml.Node{root}
	for _, n := range names {
		x, e := mappingNode(l.profiles[l.profileNames[n]])
		if e != nil {
			return nil, nil, e
		}
		nodes = append(nodes, x)
	}
	return names, nodes, nil
}
func modelOf(n *yaml.Node) string {
	c := get(n, "config")
	if c == nil || c.Kind != yaml.MappingNode {
		return ""
	}
	m := get(c, "model")
	if m == nil || m.Tag != "!!str" {
		return ""
	}
	return m.Value
}
func agentNode(n *yaml.Node, name string) *yaml.Node {
	a := get(n, "agents")
	if a == nil || a.Kind != yaml.MappingNode {
		return nil
	}
	return get(a, name)
}
func directAgents(n *yaml.Node) []DirectAgent {
	a := get(n, "agents")
	if a == nil || a.Kind != yaml.MappingNode {
		return []DirectAgent{}
	}
	var out []DirectAgent
	for i := 0; i < len(a.Content); i += 2 {
		v := a.Content[i+1]
		out = append(out, DirectAgent{Name: a.Content[i].Value, File: browserFile(valueOf(v, "file")), Model: agentModelOf(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func agentModelOf(n *yaml.Node) string {
	if m := get(n, "model"); m != nil && m.Tag == "!!str" {
		return m.Value
	}
	return modelOf(n)
}
func sourceAgentFile(path string) (string, bool) {
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != "agents" {
		return "", false
	}
	name := filepath.Base(path)
	if err := agentFile(name); err != nil {
		return "", false
	}
	return name, true
}
func browserFile(path string) string {
	if name, ok := sourceAgentFile(path); ok {
		return name
	}
	return path
}
func valueOf(n *yaml.Node, k string) string {
	if n == nil {
		return ""
	}
	v := get(n, k)
	if v == nil {
		return ""
	}
	return v.Value
}
func frontmatterModel(b []byte) (string, bool, error) {
	lines := bytes.Split(b, []byte("\n"))
	if len(lines) == 0 || string(bytes.TrimSuffix(lines[0], []byte("\r"))) != "---" {
		return "", false, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if string(bytes.TrimSuffix(lines[i], []byte("\r"))) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", false, errors.New("unterminated YAML frontmatter")
	}
	var m map[string]any
	if err := yaml.Unmarshal(bytes.Join(lines[1:end], []byte("\n")), &m); err != nil {
		return "", false, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	v, ok := m["model"]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false, errors.New("frontmatter model must be a non-empty string")
	}
	return s, true, nil
}
func usages(l layout) (map[string][]string, error) {
	out := map[string][]string{}
	root, e := mappingNode(l.root)
	if e != nil {
		return nil, e
	}
	scan := func(n *yaml.Node, label string) {
		a := get(n, "agents")
		if a == nil || a.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i < len(a.Content); i += 2 {
			if f, ok := sourceAgentFile(valueOf(a.Content[i+1], "file")); ok {
				out[f] = append(out[f], label)
			}
		}
	}
	scan(root, "shared")
	for name, f := range l.profileNames {
		n, e := mappingNode(l.profiles[f])
		if e != nil {
			return nil, e
		}
		scan(n, name)
	}
	for _, u := range out {
		sort.Strings(u)
	}
	return out, nil
}
