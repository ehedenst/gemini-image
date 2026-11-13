package cmd

import (
	"bytes"
	"context"
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

	// --delete asks first. Answering anything but y or yes keeps the session
	// and isn't an error; no answer is. Answers not typed on a terminal are
	// echoed.
	resetCLI(t)
	defer rootCmd.SetIn(nil)
	prompt := "delete session \"fox\"? The images are kept. [y/N] "
	for answer, want := range map[string]struct {
		code   int
		stderr string
	}{
		"":             {1, prompt + "\n"},
		"n\n":          {0, prompt + "n\nnothing deleted\n"},
		"yes please\n": {0, prompt + "yes please\nnothing deleted\n"},
	} {
		errOut.Reset()
		rootCmd.SetIn(strings.NewReader(answer))
		rootCmd.SetArgs([]string{"session", "--delete", "fox"})
		if err := rootCmd.Execute(); exitCode(err) != want.code || errOut.String() != want.stderr {
			t.Errorf("answer %q: err = %v, stderr %q", answer, err, errOut.String())
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("answer %q deleted the session: %v", answer, err)
		}
	}
	// y removes the history but keeps the images.
	errOut.Reset()
	rootCmd.SetIn(strings.NewReader(" Y\n"))
	if err := rootCmd.Execute(); err != nil || errOut.String() != prompt+"Y\ndeleted session \"fox\"\n" {
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

// TestSessionsPreviewOff: the global --preview=false turns thumbnails off, so
// the image isn't even read and a missing one gets no warning.
func TestSessionsPreviewOff(t *testing.T) {
	resetCLI(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GEMINI_PREVIEW", "")
	path, _ := sessionFile("fox")
	if err := saveSession(path, &session{Interactions: []interaction{{ID: "1", File: filepath.Join(t.TempDir(), "gone.png")}}}); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "stdout")) // a file, like a real stdout
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var errOut bytes.Buffer
	rootCmd.SetOut(out)
	rootCmd.SetErr(&errOut)
	defer rootCmd.SetOut(io.Discard)
	defer rootCmd.SetErr(io.Discard)

	for _, c := range []struct {
		args []string
		warn bool
	}{
		{[]string{"sessions"}, true},
		{[]string{"sessions", "--preview=false"}, false},
		{[]string{"--preview=false", "sessions"}, false},
	} {
		errOut.Reset()
		rootCmd.SetArgs(c.args)
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := strings.Contains(errOut.String(), "gone.png"); got != c.warn {
			t.Errorf("%v: warning = %v, want %v (stderr %q)", c.args, got, c.warn, errOut.String())
		}
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

// TestSessionCompactCommand: session --compact rewrites the history to the
// steps the last one was built on, and can't be combined with --delete.
func TestSessionCompactCommand(t *testing.T) {
	resetCLI(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, _ := sessionFile("fox")
	var steps []interaction
	for i, from := range []string{"", "", "", "2", "", "4"} { // 4 from step 2, 6 from step 4
		in := interaction{ID: fmt.Sprint(i + 1), Flags: map[string]string{"session": "fox"}}
		if from != "" {
			in.Flags["step"] = from
		}
		steps = append(steps, in)
	}
	if err := saveSession(path, &session{Interactions: steps}); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	rootCmd.SetErr(&errOut)
	defer rootCmd.SetErr(io.Discard)

	rootCmd.SetArgs([]string{"session", "--compact", "--delete", "fox"})
	if got := exitCode(rootCmd.Execute()); got != 2 {
		t.Errorf("--compact --delete: exit %d, want 2", got)
	}
	resetCLI(t)
	errOut.Reset()
	rootCmd.SetArgs([]string{"session", "--compact", "fox"})
	if err := rootCmd.Execute(); err != nil || errOut.String() != "compacted session \"fox\": kept steps 1, 2, 4, 6 of 6\n" {
		t.Fatalf("compact: err = %v, stderr %q", err, errOut.String())
	}
	s, err := loadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, in := range s.Interactions {
		ids = append(ids, in.ID)
		if _, ok := in.Flags["step"]; ok || in.Flags["session"] != "fox" {
			t.Errorf("step %s: flags %v, want only session", in.ID, in.Flags)
		}
	}
	if strings.Join(ids, ",") != "1,2,4,6" {
		t.Errorf("kept %v, want 1, 2, 4, 6", ids)
	}
}

// TestSessionsOps: sessions --compact and --delete work on every session,
// going on past one that fails.
func TestSessionsOps(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for name, from := range map[string][]string{"fox": {"", "", "", "2"}, "cat": {"", ""}} {
		s := &session{}
		for i, step := range from {
			in := interaction{ID: fmt.Sprint(i + 1)}
			if step != "" {
				in.Flags = map[string]string{"step": step}
			}
			s.Interactions = append(s.Interactions, in)
		}
		path, _ := sessionFile(name)
		if err := saveSession(path, s); err != nil {
			t.Fatal(err)
		}
	}
	dir, _ := sessionsDir()
	_ = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a session"), 0o600)
	var errOut bytes.Buffer
	rootCmd.SetErr(&errOut)
	defer rootCmd.SetErr(io.Discard)
	run := func(args ...string) error {
		resetCLI(t)
		errOut.Reset()
		rootCmd.SetArgs(append([]string{"sessions"}, args...))
		return rootCmd.Execute()
	}
	ids := func(name string) string {
		path, _ := sessionFile(name)
		s, err := loadSession(path)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, in := range s.Interactions {
			ids = append(ids, in.ID)
		}
		return strings.Join(ids, ",")
	}

	if got := exitCode(run("--compact", "--delete")); got != 2 {
		t.Errorf("--compact --delete: exit %d, want 2", got)
	}
	err := run("--compact")
	if err == nil || !strings.Contains(err.Error(), "broken") || ids("fox") != "1,2,4" || ids("cat") != "1,2" ||
		!strings.Contains(errOut.String(), `compacted session "fox": kept steps 1, 2, 4 of 4`) {
		t.Errorf("--compact: err = %v, fox %s, cat %s, stderr %q", err, ids("fox"), ids("cat"), errOut.String())
	}
	// One question for all of them; --force skips it.
	defer rootCmd.SetIn(nil)
	rootCmd.SetIn(strings.NewReader("no\n"))
	err = run("--delete")
	if kept, _ := filepath.Glob(filepath.Join(dir, "*.json")); err != nil || len(kept) != 3 ||
		errOut.String() != "delete 3 sessions: \"broken\", \"cat\", \"fox\"? The images are kept. [y/N] no\nnothing deleted\n" {
		t.Errorf("--delete answered no: err = %v, stderr %q", err, errOut.String())
	}
	rootCmd.SetIn(strings.NewReader("y\n")) // not read
	if err := run("--delete", "--force"); err != nil || strings.Contains(errOut.String(), "[y/N]") ||
		strings.Count(errOut.String(), "deleted session") != 3 {
		t.Errorf("--delete --force: err = %v, stderr %q", err, errOut.String())
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != 1 || filepath.Base(left[0]) != "notes.txt" {
		t.Errorf("left after --delete: %v, want only notes.txt", left)
	}
}

// TestConfirmInterrupted: Ctrl-C ends the wait for an answer, although the
// read itself can't be interrupted.
func TestConfirmInterrupted(t *testing.T) {
	r, w := io.Pipe() // never answers
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var errOut bytes.Buffer
	if ok, err := confirm(ctx, r, &errOut, "delete?", "Sure?"); ok || !errors.Is(err, context.Canceled) || errOut.String() != "delete? Sure? [y/N] \n" {
		t.Errorf("got %v, %v, stderr %q", ok, err, errOut.String())
	}
}

// TestDeleteImages: --delete=all deletes the images the steps generated
// too, but not the reference images; an image that can't be deleted keeps
// the history, so nothing is lost track of.
func TestDeleteImages(t *testing.T) {
	resetCLI(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	file := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, testPNG(t, color.Black), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	exists := func(path string) bool { _, err := os.Stat(path); return err == nil }
	ref, a, b, c := file("ref.png"), file("a.png"), file("b.png"), file("c.png")
	save := func(name string, steps ...interaction) string {
		path, _ := sessionFile(name)
		if err := saveSession(path, &session{Interactions: steps}); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// b.png twice (saved with the same -o), and one image already gone.
	fox := save("fox", interaction{ID: "1", File: a, Images: []string{ref}}, interaction{ID: "2", File: b},
		interaction{ID: "3", File: b}, interaction{ID: "4", File: filepath.Join(dir, "gone.png")})
	var errOut bytes.Buffer
	rootCmd.SetErr(&errOut)
	defer rootCmd.SetErr(io.Discard)
	defer rootCmd.SetIn(nil)

	rootCmd.SetArgs([]string{"session", "--delete=image", "fox"})
	if got := exitCode(rootCmd.Execute()); got != 2 || !exists(fox) || !exists(a) {
		t.Errorf("--delete=image: exit %d, want 2 and nothing deleted", got)
	}
	resetCLI(t)
	errOut.Reset()
	rootCmd.SetIn(strings.NewReader("y\n"))
	rootCmd.SetArgs([]string{"session", "--delete=all", "fox"})
	want := `delete session "fox" and its images? Reference images are kept. [y/N] y` + "\n" +
		`deleted session "fox" and 2 images` + "\n"
	if err := rootCmd.Execute(); err != nil || errOut.String() != want {
		t.Errorf("err = %v, stderr:\n%s\nwant:\n%s", err, errOut.String(), want)
	}
	if exists(fox) || exists(a) || exists(b) || !exists(ref) {
		t.Errorf("after --delete=all: session %v, a %v, b %v, reference %v; want only the reference left",
			exists(fox), exists(a), exists(b), exists(ref))
	}

	// A "file" that can't be deleted (a directory with something in it).
	stuck := filepath.Join(dir, "stuck")
	if err := os.MkdirAll(filepath.Join(stuck, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	cat := save("cat", interaction{ID: "1", File: stuck})
	owl := save("owl", interaction{ID: "1", File: c})
	resetCLI(t)
	errOut.Reset()
	rootCmd.SetArgs([]string{"sessions", "--delete=all", "--force"})
	if err := rootCmd.Execute(); err == nil || !exists(cat) || exists(owl) || exists(c) ||
		!strings.Contains(errOut.String(), `deleted session "owl" and 1 image`+"\n") {
		t.Errorf("err = %v, cat kept %v, owl kept %v, c kept %v, stderr %q", err, exists(cat), exists(owl), exists(c), errOut.String())
	}
}
