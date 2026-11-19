package main

// currentMachine is the machine `ocp show` runs on. Machine overlays are shown
// for every machine in the config; the current one is emphasised.
const currentMachine = "windy"

var machineNames = []string{"windy", "cosmonaut"}

// Entry is one resolved item. Origin is where it came from; Override marks an
// entry that replaces a value inherited from a lower layer.
type Entry struct {
	Name, Source, Origin string // Source: "owner/repo", "local", "npm", "remote", "stdio"
	Override             bool
}

type Profile struct {
	Name, Extends, Model string
	Hosts                []string // set only when the profile restricts the machines it renders on
	ModelOwn, ModelOver  bool     // model declared here / replaces an inherited one
	Active               bool
	Skills, Plugins, MCP []Entry
	Files                []Entry
	Disabled             []Entry
}

var rootSkills = []Entry{
	{"frontend-design", "anthropics/skills", "global", false},
	{"stop-slop", "hardikpandya/stop-slop", "global", false},
	{"grilling", "mattpocock/skills", "global", false},
	{"grill-me", "mattpocock/skills", "global", false},
}

// Machine overlays: the origin of these entries is the machine's name.
var machineMCP = []Entry{
	{"figma-console", "remote", "windy", false},
	{"mobbin", "remote", "windy", false},
	{"chrome-devtools", "stdio", "cosmonaut", false},
	{"figma-console", "stdio", "cosmonaut", false},
	{"playwright", "stdio", "cosmonaut", false},
}

// Warnings shown under the overview (only problems are ever shown).
var warnings = []string{
	"2 unreferenced local files: plugins/harness-bridge.ts, skills/apple/staging-exposure",
	"drift: router has pending changes — run ocp apply",
}

func apple() []Entry {
	names := []string{"swiftui-patterns", "swiftdata", "swift-concurrency", "healthkit", "widgetkit", "storekit", "mapkit", "core-data", "swift-testing", "app-intents"}
	var out []Entry
	for i := 0; i < 84; i++ {
		n := "skill"
		if i < len(names) {
			n = names[i]
		}
		out = append(out, Entry{n, "dpearson2699/swift-ios-skills", "profile", false})
	}
	return out
}

func profiles() []Profile {
	return []Profile{
		{Name: "apple", Extends: "default", Model: "openai/gpt-6-sol", Hosts: []string{"windy"},
			Skills:  append(append([]Entry{}, rootSkills...), apple()...),
			Plugins: []Entry{{"decision", "local", "extends default", false}},
			MCP:     machineMCP},
		{Name: "lab", Hosts: []string{"cosmonaut"}, Model: "openai/gpt-6-sol", ModelOwn: true,
			Skills: append(append([]Entry{}, rootSkills...), Entry{"mobile-testing", "local", "profile", false}),
			// lab's own cosmonaut overlay replaces the shared overlay's playwright
			MCP: withOverride(machineMCP, "playwright", "cosmonaut")},
		{Name: "default", Model: "openai/gpt-6-sol", ModelOwn: true,
			Skills:  rootSkills,
			Plugins: []Entry{{"decision", "local", "profile", false}},
			MCP:     machineMCP},
		{Name: "mixed", Extends: "default", Model: "anthropic/claude-opus", ModelOwn: true, ModelOver: true,
			Skills: []Entry{
				rootSkills[0],
				{"stop-slop", "hardikpandya/stop-slop@v2", "profile", true},
				rootSkills[2], rootSkills[3],
				{"pdf", "anthropics/skills", "profile", false},
				{"docx", "anthropics/skills", "profile", false},
				{"tdd", "mattpocock/skills", "profile", false},
				{"emil-design-eng", "emilkowalski/skills", "profile", false}},
			Plugins: []Entry{{"decision", "local", "extends default", false}},
			MCP:     machineMCP},
		{Name: "router", Active: true, Model: "openai/gpt-6.1-sol", ModelOwn: true,
			Skills:  rootSkills,
			Plugins: []Entry{{"opencode-model-router", "npm", "profile", false}},
			MCP:     machineMCP,
			Files:   []Entry{{"opencode-model-router.overrides.jsonc", "", "profile", false}}},
	}
}

// withOverride returns a copy of es with the named machine entry marked as an
// override of the value defined in a lower layer.
func withOverride(es []Entry, name, origin string) []Entry {
	out := append([]Entry(nil), es...)
	for i := range out {
		if out[i].Name == name && out[i].Origin == origin {
			out[i].Override = true
		}
	}
	return out
}
