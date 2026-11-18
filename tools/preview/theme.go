package main

// THEME — edit this file and the watcher re-renders instantly.
//
// Each value is an SGR parameter string (what goes between ESC[ and m):
//   effects : 1 bold · 2 dim · 3 italic · 4 underline · 7 reverse · 9 strike
//   basic   : 30-37 fg · 40-47 bg · 90-97 bright fg  (follow your terminal theme)
//   256     : 38;5;N fg · 48;5;N bg                   (fixed colors, N = 0-255)
//   truecolor: 38;2;R;G;B fg
// Combine with ";" e.g. "1;38;5;75" = bold + color 75. "" = plain.
// Tip: basic colors adapt to light/dark terminals; 256/truecolor do not.

var theme = map[string]string{
	// structure
	"title": "1",   // profile / root names
	"tree":  "2",   // │ ├── └── guides
	"kind":  "1;2", // section headers: skills, plugins, mcp…
	"label": "2",   // row labels in the overview (model, plugins…)
	"dim":   "2",   // secondary text, notes
	"count": "36",  // "84 skills"

	// profile state
	"active":          "1;32", // the * marker and "active" word
	"extends":         "34",   // "extends default"
	"machine":         "35",   // machine name, always purple
	"machine.current": "1;35", // the machine you are on: same purple, bold

	// where an entry came from (right-hand column); entries a profile declares
	// itself carry no mark
	"origin.global":          "36",
	"origin.extends":         "34",
	"origin.machine":         "35",
	"origin.machine.current": "1;35", // the machine you are on, bold

	// what an entry is
	"src.repo":   "0",  // git repo, e.g. anthropics/skills (git is the default, unlabeled)
	"src.local":  "33", // local
	"src.npm":    "31", // npm
	"src.remote": "32", // mcp remote
	"src.stdio":  "32", // mcp local command

	// an entry that replaces a value inherited from root or a parent profile
	"override": "33", // the * mark on an entry that replaces an inherited value

	// notices
	"warn":  "33",
	"ok":    "32",
	"muted": "2;3", // disabled / greyed entries
}
