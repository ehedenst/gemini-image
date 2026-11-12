package cmd

import (
	"bytes"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionCommand(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	img := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(img, testPNG(t, color.Black), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone.png")
	path, _ := sessionFile("fox")
	if err := saveSession(path, &session{Interactions: []interaction{
		{ID: "1", Prompt: "a fox", File: img},
		{ID: "2", Prompt: "add a\nscarf", File: missing},
	}}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	defer rootCmd.SetOut(io.Discard)
	rootCmd.SetArgs([]string{"session", "fox"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "1. a fox\n" + img + "\n\n2. add a scarf\n" + missing + "\n\n"
	if out.String() != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out.String(), want)
	}
	if !strings.Contains(errOut.String(), "warning:") || !strings.Contains(errOut.String(), "gone.png") {
		t.Errorf("no warning for the missing image: %q", errOut.String())
	}

	rootCmd.SetArgs([]string{"session", "nope"})
	if err := rootCmd.Execute(); err == nil || exitCode(err) != 1 {
		t.Errorf("unknown session: err = %v", err)
	}
	rootCmd.SetArgs([]string{"session", "../x"})
	if got := exitCode(rootCmd.Execute()); got != 2 {
		t.Errorf("invalid name: exit %d, want 2", got)
	}
}
