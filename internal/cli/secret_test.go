package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretPrintAndExec(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// No Paths: the helper must work without OCP state or configuration.
	r := &Runner{Out: &out, Err: &out}
	if err := r.Run([]string{"secret", "print", "{file:" + key + "}"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "abc" {
		t.Fatalf("print = %q", out.String())
	}
	var gotCmd, gotEnv []string
	r.ExecReplace = func(command, env []string) error {
		gotCmd, gotEnv = command, env
		return nil
	}
	if err := r.Run([]string{"secret", "exec", "TOKEN=Bearer {file:" + key + "}", "--", "server", "--flag"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotCmd, " ") != "server --flag" {
		t.Fatalf("command = %v", gotCmd)
	}
	found := false
	for _, kv := range gotEnv {
		if kv == "TOKEN=Bearer abc" {
			found = true
		}
	}
	if !found {
		t.Fatal("TOKEN not set for the command")
	}
	out.Reset()
	t.Setenv("OCP_TEST_HEADER", "e")
	if err := r.Run([]string{"secret", "headers", "Authorization=Bearer {file:" + key + "}", "X-Env={env:OCP_TEST_HEADER}"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != `{"Authorization":"Bearer abc","X-Env":"e"}`+"\n" {
		t.Fatalf("headers = %q", out.String())
	}
	for _, args := range [][]string{
		{"secret", "headers"},
		{"secret", "headers", "bad"},
		{"secret"},
		{"secret", "print"},
		{"secret", "exec", "TOKEN=x"},
		{"secret", "exec", "--"},
		{"secret", "exec", "bad", "--", "x"},
		{"secret", "exec", "T={file:" + filepath.Join(dir, "missing") + "}", "--", "x"},
		{"secret", "nope"},
	} {
		if err := r.Run(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
