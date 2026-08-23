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
