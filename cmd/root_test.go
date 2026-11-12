package cmd

import (
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

// TestCLIUsageErrors runs the real command tree. Every case fails before any
// API call. Cobra and Viper state is global, so --size goes in the last case.
func TestCLIUsageErrors(t *testing.T) {
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
