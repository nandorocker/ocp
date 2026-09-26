package ui

import "testing"

func TestParseAndMergeModels(t *testing.T) {
	discovered := parseModels([]byte("openai/gpt-5\ninvalid\nopenai/gpt-5\nollama/qwen:latest\nlog line here\n"))
	if len(discovered) != 2 || discovered[0].ID != "ollama/qwen:latest" || discovered[1].ID != "openai/gpt-5" {
		t.Fatalf("discovered = %#v", discovered)
	}
	snapshot := Snapshot{Profiles: []ProfileView{{
		Model:        "custom/profile",
		DirectModel:  "custom/profile",
		Agents:       []AgentView{{Model: "source/model", SourceModel: "source/model"}},
		DirectAgents: []DirectAgent{{Model: "manual/agent"}},
	}}}
	models := mergeModels(discovered, snapshot)
	want := []string{"custom/profile", "manual/agent", "ollama/qwen:latest", "openai/gpt-5", "source/model"}
	if len(models) != len(want) {
		t.Fatalf("models = %#v", models)
	}
	for i := range want {
		if models[i].ID != want[i] {
			t.Fatalf("models[%d] = %q, want %q", i, models[i].ID, want[i])
		}
	}
}
