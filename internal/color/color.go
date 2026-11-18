// Package color provides semantic terminal output with ANSI escape codes.
package color

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync/atomic"

	"golang.org/x/term"
)

const (
	ansiReset   = "\033[0m"
	ANSIGreen   = "\033[32m"
	ANSIYellow  = "\033[33m"
	ansiRed     = "\033[31m"
	ANSIDim     = "\033[2m"
	ANSIMagenta = "\033[35m"
	ANSICyan    = "\033[36m"

	// Styles used by structured views such as `ocp show`. Basic ANSI only, so
	// they follow the terminal theme.
	ANSIRed         = ansiRed
	ANSIBlue        = "\033[34m"
	ANSIBold        = "\033[1m"
	ANSIBoldDim     = "\033[1;2m"
	ANSIBoldGreen   = "\033[1;32m"
	ANSIDimItalic   = "\033[2;3m"
	ANSIBoldMagenta = "\033[1;35m"
)

var colorDisabled atomic.Int32 // 0 = auto-detect, 1 = force disable, 2 = force enable

var isTerminal = func(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// Disable disables all color output for the process.
func Disable() { colorDisabled.Store(1) }

// Force enables styling even when output is not a terminal. It is primarily
// useful in tests that assert on styled output.
func Force() { colorDisabled.Store(2) }

// Reset restores automatic color detection. It is primarily useful in tests.
func Reset() { colorDisabled.Store(0) }

// Writer holds the output writers used by OCP commands.
type Writer struct {
	Out, Err io.Writer
}

// New returns a Writer configured with the given output destinations.
func New(out, err io.Writer) *Writer {
	return &Writer{Out: out, Err: err}
}

func colorAllowed(terminal bool) bool {
	switch colorDisabled.Load() {
	case 1:
		return false
	case 2:
		return true
	}
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && terminal
}

func applyColor(w io.Writer, code, text string) string {
	if !colorAllowed(isTerminal(w)) {
		return text
	}
	return code + text + ansiReset
}

// Style wraps text for the standard output writer when colors are enabled.
func (c *Writer) Style(code, text string) string {
	if c == nil {
		return text
	}
	return applyColor(c.Out, code, text)
}

// Styled reports whether Out will receive ANSI styling. Structured views use
// it to choose between Unicode glyphs and plain ASCII.
func (c *Writer) Styled() bool {
	return c != nil && colorAllowed(isTerminal(c.Out))
}

// Width returns the columns available on Out: the terminal size when Out is a
// terminal, otherwise COLUMNS, otherwise 80.
func (c *Writer) Width() int {
	if c != nil {
		if f, ok := c.Out.(*os.File); ok {
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				return w
			}
		}
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 80
}

// Check writes a success indicator to Out.
func (c *Writer) Check(msg string) {
	fmt.Fprintf(c.Out, "%s %s\n", applyColor(c.Out, ANSIGreen, "\u2713"), msg)
}

// CheckErr writes an error indicator to Err.
func (c *Writer) CheckErr(msg string) {
	fmt.Fprintf(c.Err, "%s %s\n", applyColor(c.Err, ansiRed, "\u2717"), msg)
}

// CheckWarn writes a warning indicator to Err.
func (c *Writer) CheckWarn(msg string) {
	fmt.Fprintf(c.Err, "%s %s\n", applyColor(c.Err, ANSIYellow, "!"), msg)
}

// Success outputs a green success message.
func (c *Writer) Success(msg string) {
	fmt.Fprintln(c.Out, applyColor(c.Out, ANSIGreen, "\u2713 "+msg))
}

// Warning outputs a yellow warning message.
func (c *Writer) Warning(msg string) {
	fmt.Fprintln(c.Err, applyColor(c.Err, ANSIYellow, "Warning: "+msg))
}

// Error outputs a red error message.
func (c *Writer) Error(msg string) {
	fmt.Fprintln(c.Err, applyColor(c.Err, ansiRed, "Error: "+msg))
}

// Muted outputs dimmed context text.
func (c *Writer) Muted(msg string) {
	fmt.Fprintln(c.Out, applyColor(c.Out, ANSIDim, msg))
}

// Important outputs a magenta highlighted message.
func (c *Writer) Important(msg string) {
	fmt.Fprintln(c.Out, applyColor(c.Out, ANSIMagenta, msg))
}

// Print outputs plain text with no formatting.
func (c *Writer) Print(msg string) {
	fmt.Fprintln(c.Out, msg)
}

// Printf outputs formatted plain text (no color).
func (c *Writer) Printf(format string, args ...any) {
	fmt.Fprintf(c.Out, format+"\n", args...)
}

// Prompt formats a menu selection line with a colored number prefix.
func (c *Writer) Prompt(num int, label string) string {
	return fmt.Sprintf("  %s) %s", applyColor(c.Out, ANSIGreen, strconv.Itoa(num)), label)
}
