package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestExitCode(t *testing.T) {
	for err, want := range map[error]int{
		nil:                                   0,
		errors.New("boom"):                    1,
		usageError{errors.New("bad flag")}:    2,
		fmt.Errorf("x: %w", usageError{nil}):  2,
		fmt.Errorf("x: %w", context.Canceled): 130,
	} {
		if got := exitCode(err); got != want {
			t.Errorf("exitCode(%v) = %d, want %d", err, got, want)
		}
	}
}

// TestExecuteInterrupted: Ctrl-C also reaches child processes such as a
// "!command", which then fail with their own error; the run must still exit
// with 130 and just say it was interrupted.
func TestExecuteInterrupted(t *testing.T) {
	resetCLI(t)
	// Interrupt this process as Ctrl-C would, then wait to be killed.
	t.Setenv("GEMINI_API_KEY", "!kill -INT $PPID; exec sleep 10")
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	defer rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"--env-file", "/dev/null", "generate", "--size=", "--aspect-ratio=", "--search=", "x"})
	if got := Execute(); got != 130 || stderr.String() != "error: interrupted\n" {
		t.Errorf("exit code %d, stderr %q; want 130, \"error: interrupted\"", got, stderr.String())
	}
}

// TestCLIUsageErrors runs the real command tree. Every case fails before any
// API call. Flag values persist between cases, so --size goes in the last one.
func TestCLIUsageErrors(t *testing.T) {
	resetCLI(t)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_ASPECT_RATIO", "")
	t.Setenv("GEMINI_SIZE", "")
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	for _, args := range [][]string{
		{"generate"},
		{"generate", "a", "b"},
		{"generate", "--bogus", "x"},
		{"--env-file", "/dev/null", "generate", "x"}, // no API key
		{"--env-file", "/dev/null", "generate", "--size", "3K", "x"},
	} {
		rootCmd.SetArgs(args)
		if got := exitCode(rootCmd.Execute()); got != 2 {
			t.Errorf("%v: exit code %d, want 2", args, got)
		}
	}
}
