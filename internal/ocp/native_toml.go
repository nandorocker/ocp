package ocp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// nativeCollections are settings whose entries OCP owns one at a time, so
// entries the user added by hand survive. Everything else is owned whole.
var nativeCollections = map[string]map[string]bool{
	"claude": {"agents": true, "env": true},
	"codex":  {"agents": true, "mcp_servers": true, "model_providers": true},
}

// changedPaths lists the owned paths whose value differs between before and
// after: one path per collection entry, otherwise one per top-level key.
func changedPaths(harness string, before, after map[string]any) [][]string {
	var paths [][]string
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	for _, k := range sortedKeys(keys) {
		b, bok := before[k]
		a, aok := after[k]
		if nativeCollections[harness][k] {
			bm, bIsMap := b.(map[string]any)
			am, aIsMap := a.(map[string]any)
			if (bIsMap || !bok) && (aIsMap || !aok) {
				entries := map[string]bool{}
				for e := range bm {
					entries[e] = true
				}
				for e := range am {
					entries[e] = true
				}
				for _, e := range sortedKeys(entries) {
					bv, bin := bm[e]
					av, ain := am[e]
					if bin != ain || !nativeEqual(bv, av) {
						paths = append(paths, []string{k, e})
					}
				}
				continue
			}
		}
		if bok != aok || !nativeEqual(b, a) {
			paths = append(paths, []string{k})
		}
	}
	return paths
}

type tomlItem struct {
	path  []string
	kind  unstable.Kind
	start int // offset of the line the item starts on
	// For a root key/value with a single (non-dotted) key and a scalar
	// value, the value's byte range, so the value can change in place.
	valueStart, valueEnd int
}

func hasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func lineStart(data []byte, offset int) int {
	return bytes.LastIndexByte(data[:offset], '\n') + 1
}

// trimTrailingNotes moves end back over blank and comment lines, so notes
// written above the next table stay with that table.
func trimTrailingNotes(data []byte, start, end int) int {
	for end > start {
		prev := bytes.LastIndexByte(data[:end-1], '\n') + 1
		if prev < start {
			break
		}
		line := bytes.TrimSpace(data[prev:end])
		if len(line) != 0 && line[0] != '#' {
			break
		}
		end = prev
	}
	return end
}

func tomlItems(data []byte) ([]tomlItem, error) {
	var items []tomlItem
	var current []string
	var p unstable.Parser
	p.Reset(data)
	for p.NextExpression() {
		node := p.Expression()
		if node.Kind != unstable.KeyValue && node.Kind != unstable.Table && node.Kind != unstable.ArrayTable {
			continue
		}
		var keys []string
		first := -1
		it := node.Key()
		for it.Next() {
			k := it.Node()
			if first < 0 {
				first = int(k.Raw.Offset)
			}
			keys = append(keys, string(k.Data))
		}
		if first < 0 {
			return nil, errors.New("TOML expression without a key")
		}
		item := tomlItem{kind: node.Kind, start: lineStart(data, first)}
		if node.Kind == unstable.KeyValue {
			item.path = append(append([]string(nil), current...), keys...)
			if v := node.Value(); len(current) == 0 && len(keys) == 1 && v.Raw.Length > 0 {
				switch v.Kind {
				case unstable.String, unstable.Integer, unstable.Float, unstable.Bool:
					item.valueStart, item.valueEnd = int(v.Raw.Offset), int(v.Raw.Offset+v.Raw.Length)
				}
			}
		} else {
			current = keys
			item.path = keys
		}
		items = append(items, item)
	}
	if err := p.Error(); err != nil {
		return nil, err
	}
	return items, nil
}

// keyValueEnd finds where a key/value expression starting at start ends: the
// first line boundary at which the text parses as one complete expression.
func keyValueEnd(data []byte, start, limit int) (int, error) {
	for end := start; end < limit; {
		next := bytes.IndexByte(data[end:limit], '\n')
		if next < 0 {
			end = limit
		} else {
			end += next + 1
		}
		var probe map[string]any
		if toml.Unmarshal(data[start:end], &probe) == nil && len(probe) == 1 {
			return end, nil
		}
	}
	return 0, errors.New("cannot find the end of a TOML value")
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return tomlString(k)
}

func tomlString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	// JSON string escapes are a subset of TOML basic-string escapes. TOML
	// also forbids a raw DEL, which JSON leaves alone.
	return strings.ReplaceAll(strings.TrimSuffix(b.String(), "\n"), "\x7f", `\u007F`)
}

func tomlInline(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return tomlString(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			s, err := tomlInline(e)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case []string:
		parts := make([]any, len(v))
		for i, s := range v {
			parts[i] = s
		}
		return tomlInline(parts)
	case map[string]any:
		parts := make([]string, 0, len(v))
		for _, k := range sortedKeys(v) {
			s, err := tomlInline(v[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey(k)+" = "+s)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	case map[string]string:
		m := make(map[string]any, len(v))
		for k, s := range v {
			m[k] = s
		}
		return tomlInline(m)
	}
	return "", fmt.Errorf("unsupported TOML value %T", v)
}

func tomlTable(path []string, value map[string]any) (string, error) {
	keys := make([]string, len(path))
	for i, k := range path {
		keys[i] = tomlKey(k)
	}
	var b strings.Builder
	b.WriteString("[" + strings.Join(keys, ".") + "]\n")
	for _, k := range sortedKeys(value) {
		s, err := tomlInline(value[k])
		if err != nil {
			return "", fmt.Errorf("%s.%s: %w", strings.Join(path, "."), k, err)
		}
		b.WriteString(tomlKey(k) + " = " + s + "\n")
	}
	return b.String(), nil
}

// withoutEmptyMaps drops empty tables, which an emptied collection leaves
// behind in the intended settings but which may not exist in the file.
func withoutEmptyMaps(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, e := range m {
		e = withoutEmptyMaps(e)
		if sub, ok := e.(map[string]any); ok && len(sub) == 0 {
			continue
		}
		out[k] = e
	}
	return out
}

// patchNativeTOML rewrites only the owned paths that changed between before
// and after. Comments, ordering and every other table stay byte for byte.
// The result is re-parsed and must equal after exactly.
func patchNativeTOML(data []byte, before, after map[string]any) ([]byte, error) {
	paths := changedPaths("codex", before, after)
	items, err := tomlItems(data)
	if err != nil {
		return nil, err
	}
	type edit struct {
		start, end int
		text       string
	}
	var cuts []edit
	// A root scalar that stays a scalar changes in place, keeping its
	// position and trailing comment, so reset restores the bytes exactly.
	inPlace := map[string]bool{}
	for _, path := range paths {
		if len(path) != 1 {
			continue
		}
		b, bok := before[path[0]]
		a, aok := after[path[0]]
		_, bTable := b.(map[string]any)
		_, aTable := a.(map[string]any)
		if !bok || !aok || bTable || aTable {
			continue
		}
		for _, it := range items {
			if it.kind == unstable.KeyValue && it.valueEnd > 0 && len(it.path) == 1 && it.path[0] == path[0] {
				text, err := tomlInline(a)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path[0], err)
				}
				cuts = append(cuts, edit{start: it.valueStart, end: it.valueEnd, text: text})
				inPlace[path[0]] = true
				break
			}
		}
	}
	firstTable := len(data)
	for _, it := range items {
		if it.kind != unstable.KeyValue {
			firstTable = it.start
			break
		}
	}
	for _, path := range paths {
		if len(path) == 1 && inPlace[path[0]] {
			continue
		}
		for i, it := range items {
			limit := len(data)
			if i+1 < len(items) {
				limit = items[i+1].start
			}
			switch {
			case it.kind != unstable.KeyValue && hasPrefix(it.path, path):
				if it.kind == unstable.ArrayTable {
					return nil, fmt.Errorf("cannot edit %s: it is an array of tables", strings.Join(path, "."))
				}
				end := len(data)
				for _, next := range items[i+1:] {
					if next.kind != unstable.KeyValue {
						end = next.start
						break
					}
				}
				start := it.start
				// Take one blank separator line with the table, as appending
				// a table adds one.
				if start >= 2 && data[start-1] == '\n' && data[start-2] == '\n' {
					start--
				}
				cuts = append(cuts, edit{start: start, end: trimTrailingNotes(data, it.start, end)})
			case it.kind == unstable.KeyValue && hasPrefix(it.path, path):
				end, err := keyValueEnd(data, it.start, limit)
				if err != nil {
					return nil, err
				}
				cuts = append(cuts, edit{start: it.start, end: end})
			case it.kind == unstable.KeyValue && len(it.path) < len(path) && hasPrefix(path, it.path):
				return nil, fmt.Errorf("cannot edit %s: it is written inline in %s; move it to its own table", strings.Join(path, "."), strings.Join(it.path, "."))
			}
		}
	}
	// Drop cuts nested in a removed table section.
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start < cuts[j].start })
	var edits []edit
	for _, c := range cuts {
		if n := len(edits); n > 0 && c.start < edits[n-1].end {
			edits[n-1].end = max(edits[n-1].end, c.end)
			continue
		}
		edits = append(edits, c)
	}
	var top, tables strings.Builder
	for _, path := range paths {
		value, exists := keyValue(after, path)
		if !exists || len(path) == 1 && inPlace[path[0]] {
			continue
		}
		if m, ok := value.(map[string]any); ok {
			text, err := tomlTable(path, m)
			if err != nil {
				return nil, err
			}
			tables.WriteString("\n" + text)
			continue
		}
		if len(path) != 1 {
			return nil, fmt.Errorf("cannot write %s: entries of %s must be tables", strings.Join(path, "."), path[0])
		}
		text, err := tomlInline(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path[0], err)
		}
		top.WriteString(tomlKey(path[0]) + " = " + text + "\n")
	}
	if top.Len() > 0 {
		// New top-level keys go after the existing ones, above any notes
		// that introduce the first table.
		at := trimTrailingNotes(data, 0, firstTable)
		text := top.String()
		if at > 0 && data[at-1] != '\n' {
			text = "\n" + text
		}
		edits = append(edits, edit{start: at, end: at, text: text})
	}
	// Apply from the end; at equal offsets remove before inserting.
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	out := append([]byte(nil), data...)
	for _, e := range edits {
		out = append(out[:e.start:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	if tables.Len() > 0 {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		text := tables.String()
		if len(bytes.TrimSpace(out)) == 0 {
			out = out[:0]
			text = strings.TrimPrefix(text, "\n")
		}
		out = append(out, text...)
	}
	parsed, err := nativeSettings(out, "codex")
	if err != nil {
		return nil, fmt.Errorf("native TOML edit produced invalid TOML: %w", err)
	}
	if !nativeEqual(withoutEmptyMaps(parsed), withoutEmptyMaps(after)) {
		return nil, errors.New("native TOML edit could not preserve configuration")
	}
	return out, nil
}
