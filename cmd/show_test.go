package cmd

import (
	"bytes"
	"errors"
	"fmt"
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
		{ID: "3", Prompt: "a hat instead", File: img, Flags: map[string]string{"session": "fox", "step": "1"}},
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
	want := "1. a fox\n" + img + "\n\n2. add a scarf\n" + missing + "\n\n3. a hat instead (from step 1)\n" + img + "\n\n"
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

	// --delete removes the history but keeps the images.
	t.Cleanup(func() { _ = sessionCmd.Flags().Set("delete", "false") })
	errOut.Reset()
	rootCmd.SetArgs([]string{"session", "--delete", "fox"})
	if err := rootCmd.Execute(); err != nil || errOut.String() != "deleted session \"fox\"\n" {
		t.Errorf("delete: err = %v, stderr %q", err, errOut.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session file still there: %v", err)
	}
	if _, err := os.Stat(img); err != nil {
		t.Errorf("image deleted along with the session: %v", err)
	}
	if got := exitCode(rootCmd.Execute()); got != 1 {
		t.Errorf("deleting again: exit %d, want 1", got)
	}
}

func TestSessionsCommand(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	defer rootCmd.SetOut(io.Discard)
	defer rootCmd.SetErr(io.Discard)

	rootCmd.SetArgs([]string{"sessions"})
	if err := rootCmd.Execute(); err != nil || out.Len() != 0 || errOut.String() != "no sessions\n" {
		t.Errorf("no sessions: err = %v, stdout %q, stderr %q", err, out.String(), errOut.String())
	}

	for name, n := range map[string]int{"fox": 3, "cat": 1, "empty": 0} {
		path, _ := sessionFile(name)
		s := &session{}
		for i := range n {
			s.Interactions = append(s.Interactions, interaction{ID: fmt.Sprint(i), File: "../testdata/girl.jpg"})
		}
		if err := saveSession(path, s); err != nil {
			t.Fatal(err)
		}
	}
	dir, _ := sessionsDir()
	_ = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a session"), 0o600)

	out.Reset()
	errOut.Reset()
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "cat  1 step\nempty  0 steps\nfox  3 steps\n"; out.String() != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out.String(), want)
	}
	if !strings.Contains(errOut.String(), "warning:") || !strings.Contains(errOut.String(), "broken.json") {
		t.Errorf("no warning for the broken session: %q", errOut.String())
	}
}
