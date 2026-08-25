package color

import (
	"bytes"
	"strings"
	"testing"
)

func TestColorDisabled(t *testing.T) {
	Disable()
	t.Cleanup(Reset)

	var out bytes.Buffer
	c := New(&out, &out)

	c.Check("test")
	c.Success("msg")
	c.Muted("dim")
	c.Important("imp")
	prompt := c.Prompt(1, "label")

	buf := out.String()
	if strings.Contains(buf, "\x1b[") {
		t.Fatal("expected no ANSI codes when disabled")
	}
	if strings.Contains(prompt, "\x1b[") {
		t.Fatal("expected no ANSI codes in prompt when disabled")
	}
	if !strings.Contains(buf, "test") || !strings.Contains(buf, "✓") {
		t.Fatalf("expected plain text markers in output: %q", buf)
	}
}

func TestColorMethods(t *testing.T) {
	var out, err bytes.Buffer
	c := New(&out, &err)

	c.Success("hello")
	outStr := out.String()
	if !strings.Contains(outStr, "hello") {
		t.Fatalf("missing success message: %q", outStr)
	}

	c.Warning("bad thing")
	errStr := err.String()
	if !strings.Contains(errStr, "bad thing") {
		t.Fatalf("missing warning: %q", errStr)
	}

	c.Error("fatal")
	errStr2 := err.String()
	if !strings.Contains(errStr2, "fatal") {
		t.Fatalf("missing error: %q", errStr2)
	}

	c.Print("plain")
	outStr2 := out.String()
	if !strings.Contains(outStr2, "plain") {
		t.Fatalf("missing print: %q", outStr2)
	}

	c.Printf("%s: %d", "count", 42)
	if !strings.Contains(out.String(), "count: 42") {
		t.Fatalf("missing printf: %q", out.String())
	}
}

func TestPromptFormat(t *testing.T) {
	p := (&Writer{}).Prompt(1, "First option")
	if !strings.Contains(p, "1") || !strings.Contains(p, "First option") {
		t.Fatalf("prompt format wrong: %q", p)
	}
}

func TestColorAllowedHonorsConventions(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	if !colorAllowed(true) {
		t.Fatal("terminal should allow color by default")
	}
	t.Setenv("NO_COLOR", "1")
	if colorAllowed(true) {
		t.Fatal("NO_COLOR should disable color")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if colorAllowed(true) {
		t.Fatal("TERM=dumb should disable color")
	}
	t.Setenv("TERM", "xterm")
	Disable()
	if colorAllowed(true) {
		t.Fatal("explicit disable should disable color")
	}
}
