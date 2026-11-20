package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStatusJSON(t *testing.T) {
	p := testPaths(t)
	source := filepath.Join(t.TempDir(), "source")
	write(t, filepath.Join(source, "ocp.yaml"), "version: 1\nprofiles:\n  default: {}\n  deep: {}\n")
	var out bytes.Buffer
	r := runner(p, &out)
	if err := r.Run([]string{"setup", "--source", source, "--machine", "windy"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := r.Run([]string{"status", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got statusDoc
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	abs, _ := canonical(source)
	if got.Source != abs || got.ConfigFile != filepath.Join(abs, "ocp.yaml") || got.Machine != "windy" || got.Active["opencode"] != "default" || len(got.Profiles) != 2 || got.Harnesses[0] != "opencode" {
		t.Fatalf("status = %+v", got)
	}
	// A broken source is reported, not fatal.
	write(t, filepath.Join(source, "ocp.yaml"), "version: [\n")
	out.Reset()
	if err := r.Run([]string{"status", "--json"}); err != nil {
		t.Fatal(err)
	}
	got = statusDoc{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Error == "" {
		t.Fatalf("broken source status = %+v, %v", got, err)
	}
}
