package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportRejectsSymlinkAliasOfInput(t *testing.T) {
	input := t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "source")
	if err := os.Symlink(input, alias); err != nil {
		t.Fatal(err)
	}
	if err := Import(input, alias, true); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("Import error = %v", err)
	}
}

func TestImportPreservesJSONNumberTypes(t *testing.T) {
	input, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "opencode.json"), []byte(`{"integer":1,"decimal":1.5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Import(input, source, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(source, "ocp.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "integer: 1") || !strings.Contains(text, "decimal: 1.5") {
		t.Fatalf("imported YAML changed number types:\n%s", text)
	}
}

func TestImportRejectsInvalidAgentNameBeforeWriting(t *testing.T) {
	input, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(input, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "agents", "bad name.md"), []byte("agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Import(input, source, false); err == nil || !strings.Contains(err.Error(), "invalid imported agent name") {
		t.Fatalf("Import error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "ocp.yaml")); !os.IsNotExist(err) {
		t.Fatalf("ocp.yaml was written after validation failure: %v", err)
	}
}

func TestImportForceReplacesDestinationSymlinkWithoutFollowingIt(t *testing.T) {
	input, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "opencode.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(source, "ocp.yaml")
	if err := os.Symlink(external, destination); err != nil {
		t.Fatal(err)
	}
	if err := Import(input, source, true); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(external); err != nil || string(content) != "keep\n" {
		t.Fatalf("external file = %q, %v", content, err)
	}
	if info, err := os.Lstat(destination); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination was not replaced with a regular file: %v, %v", info, err)
	}
}
