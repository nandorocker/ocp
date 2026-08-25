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
	ansiYellow  = "\033[33m"
	ansiRed     = "\033[31m"
	ansiDim     = "\033[2m"
	ansiMagenta = "\033[35m"
)

var colorDisabled atomic.Int32 // 0 = auto-detect, 1 = force disable

// Disable disables all color output for the process.
func Disable() { colorDisabled.Store(1) }

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

func colorEnabled() bool {
	return colorAllowed(term.IsTerminal(2))
}

func colorAllowed(terminal bool) bool {
	return colorDisabled.Load() == 0 && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && terminal
}

func applyColor(code, text string) string {
	if !colorEnabled() {
		return text
	}
	return code + text + ansiReset
}

// ApplyColor wraps text with ANSI escape codes when colors are enabled.
func ApplyColor(code, text string) string { return applyColor(code, text) }

// Check writes a success indicator to Out.
func (c *Writer) Check(msg string) {
	fmt.Fprintf(c.Out, "%s %s\n", applyColor(ANSIGreen, "\u2713"), msg)
}

// CheckErr writes an error indicator to Err.
func (c *Writer) CheckErr(msg string) {
	fmt.Fprintf(c.Err, "%s %s\n", applyColor(ansiRed, "\u2717"), msg)
}

// CheckWarn writes a warning indicator to Err.
func (c *Writer) CheckWarn(msg string) {
	fmt.Fprintf(c.Err, "%s %s\n", applyColor(ansiYellow, "!"), msg)
}

// Success outputs a green success message.
func (c *Writer) Success(msg string) {
	fmt.Fprintln(c.Out, applyColor(ANSIGreen, "\u2713 "+msg))
}

// Warning outputs a yellow warning message.
func (c *Writer) Warning(msg string) {
	fmt.Fprintln(c.Err, applyColor(ansiYellow, "Warning: "+msg))
}

// Error outputs a red error message.
func (c *Writer) Error(msg string) {
	fmt.Fprintln(c.Err, applyColor(ansiRed, "Error: "+msg))
}

// Muted outputs dimmed context text.
func (c *Writer) Muted(msg string) {
	fmt.Fprintln(c.Out, applyColor(ansiDim, msg))
}

// Important outputs a magenta highlighted message.
func (c *Writer) Important(msg string) {
	fmt.Fprintln(c.Out, applyColor(ansiMagenta, msg))
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
	return fmt.Sprintf("  %s) %s", applyColor(ANSIGreen, strconv.Itoa(num)), label)
}
