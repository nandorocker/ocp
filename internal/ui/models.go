package ui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type ModelOption struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

func discoverModels(ctx context.Context, executable string) ([]ModelOption, error) {
	if executable == "" {
		executable = "opencode"
	}
	output, err := exec.CommandContext(ctx, executable, "models", "--pure").Output()
	if err != nil {
		return nil, fmt.Errorf("list OpenCode models: %w", err)
	}
	return parseModels(output), nil
}

func parseModels(output []byte) []ModelOption {
	seen := map[string]bool{}
	var models []ModelOption
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		provider, _, ok := strings.Cut(id, "/")
		if !ok || provider == "" || strings.ContainsAny(id, " \t") || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, ModelOption{ID: id, Provider: provider})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func mergeModels(discovered []ModelOption, snapshot Snapshot) []ModelOption {
	byID := make(map[string]ModelOption, len(discovered))
	add := func(id string) {
		provider, _, ok := strings.Cut(id, "/")
		if id != "" && ok && provider != "" {
			byID[id] = ModelOption{ID: id, Provider: provider}
		}
	}
	for _, model := range discovered {
		add(model.ID)
	}
	for _, profile := range snapshot.Profiles {
		add(profile.Model)
		add(profile.DirectModel)
		for _, agent := range profile.Agents {
			add(agent.Model)
			add(agent.SourceModel)
		}
		for _, agent := range profile.DirectAgents {
			add(agent.Model)
		}
	}
	models := make([]ModelOption, 0, len(byID))
	for _, model := range byID {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}
