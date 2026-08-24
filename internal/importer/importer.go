// Package importer creates a compact starter OCP source from an OpenCode directory.
package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type document struct {
	Version      int              `yaml:"version"`
	Config       map[string]any   `yaml:"config,omitempty"`
	Instructions []string         `yaml:"instructions,omitempty"`
	Skills       []string         `yaml:"skills,omitempty"`
	Agents       map[string]agent `yaml:"agents,omitempty"`
}
type agent struct {
	File string `yaml:"file"`
}

// Import reads input (a directory or opencode.json file) and writes a starter source.
func Import(input, source string, force bool) error {
	info, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("inspect import input: %w", err)
	}
	root := input
	if !info.IsDir() {
		if filepath.Base(input) != "opencode.json" {
			return errors.New("import file must be named opencode.json")
		}
		root = filepath.Dir(input)
	}
	jsonPath := filepath.Join(root, "opencode.json")
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("read OpenCode config: %w", err)
	}
	var native map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&native); err != nil {
		return fmt.Errorf("parse %s: JSONC is unsupported; provide strict JSON: %w", jsonPath, err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("parse %s: JSONC is unsupported; provide one strict JSON value", jsonPath)
	}
	native, err = normalizeNumbers(native)
	if err != nil {
		return fmt.Errorf("parse %s: %w", jsonPath, err)
	}
	if err := os.MkdirAll(source, 0o700); err != nil {
		return err
	}
	if samePath(root, source) {
		return errors.New("import source must differ from the OpenCode input directory")
	}
	if err := preflight(root, source, force); err != nil {
		return err
	}
	doc := document{Version: 1, Config: native}
	if exists(filepath.Join(root, "AGENTS.md")) {
		if err := copyFile(filepath.Join(source, "AGENTS.md"), filepath.Join(root, "AGENTS.md"), force); err != nil {
			return err
		}
		doc.Instructions = []string{"./AGENTS.md"}
	}
	agentDirName := agentDir(root)
	if agentDirName != "" {
		if entries, err := os.ReadDir(filepath.Join(root, agentDirName)); err == nil {
			doc.Agents = map[string]agent{}
			for _, e := range entries {
				if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
					continue
				}
				name := e.Name()[:len(e.Name())-3]
				dst := filepath.Join(source, "agents", e.Name())
				if err := copyFile(dst, filepath.Join(root, agentDirName, e.Name()), force); err != nil {
					return err
				}
				doc.Agents[name] = agent{File: "./agents/" + e.Name()}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if entries, err := os.ReadDir(filepath.Join(root, "skills")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dst := filepath.Join(source, "skills", e.Name())
			if err := copyTree(dst, filepath.Join(root, "skills", e.Name()), force); err != nil {
				return err
			}
			doc.Skills = append(doc.Skills, "./skills/"+e.Name())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	y, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(source, "ocp.yaml"), y, 0o600)
}

func normalizeNumbers(value map[string]any) (map[string]any, error) {
	for key, item := range value {
		normalized, err := normalizeNumber(item)
		if err != nil {
			return nil, err
		}
		value[key] = normalized
	}
	return value, nil
}

func normalizeNumber(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			return integer, nil
		}
		number, err := value.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, fmt.Errorf("invalid JSON number %q", value)
		}
		return number, nil
	case map[string]any:
		return normalizeNumbers(value)
	case []any:
		for index, item := range value {
			normalized, err := normalizeNumber(item)
			if err != nil {
				return nil, err
			}
			value[index] = normalized
		}
	}
	return value, nil
}

func preflight(root, source string, force bool) error {
	if err := collision(filepath.Join(source, "ocp.yaml"), force); err != nil {
		return err
	}
	for _, name := range []string{"AGENTS.md", "agents", "skills"} {
		if exists(filepath.Join(root, name)) {
			if err := collision(filepath.Join(source, name), force); err != nil {
				return err
			}
		}
	}
	return nil
}
func samePath(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo) {
		return true
	}
	a, _ = filepath.EvalSymlinks(a)
	b, _ = filepath.EvalSymlinks(b)
	return a != "" && b != "" && filepath.Clean(a) == filepath.Clean(b)
}
func exists(path string) bool { _, e := os.Lstat(path); return e == nil }
func collision(path string, force bool) error {
	if exists(path) && !force {
		return fmt.Errorf("refusing to overwrite %s (rerun with --force)", path)
	}
	return nil
}
func copyFile(dst, src string, force bool) error {
	if err := collision(dst, force); err != nil {
		return err
	}
	b, e := os.ReadFile(src)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dst), 0o700); e != nil {
		return e
	}
	return os.WriteFile(dst, b, 0o600)
}
func copyTree(dst, src string, force bool) error {
	if exists(dst) {
		if !force {
			return fmt.Errorf("refusing to overwrite %s (rerun with --force)", dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	return filepath.WalkDir(src, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFile(target, path, true)
	})
}
