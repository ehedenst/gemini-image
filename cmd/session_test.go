package cmd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSessionFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	if got, err := sessionFile("fox-2.v1_x"); err != nil || got != "/state/gemini-image/sessions/fox-2.v1_x.json" {
		t.Errorf("got %q, %v", got, err)
	}
	for _, bad := range []string{"..", ".hidden", "a/b", "../x", "a b", ""} {
		if _, err := sessionFile(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// TestSessionParallelRun: a step that another run saved while this one was
// generating is kept. Also checks that the size is stored as it was sent.
func TestSessionParallelRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, _ := sessionFile("fox")
	other := func() { _ = saveSession(path, &session{Interactions: []interaction{{ID: "v1_other"}}}) }
	events := append([]string{`{"interaction":{"id":"v1_mine"},"event_type":"interaction.created"}`}, imageEvents(t)...)
	if _, _, err := generateCLI(t, sse(events...), other, "--session", "fox", "--size", "2k", "-o", filepath.Join(t.TempDir(), "out.png"), "x"); err != nil {
		t.Fatal(err)
	}
	s, err := loadSession(path)
	if err != nil || len(s.Interactions) != 2 || s.Interactions[0].ID != "v1_other" || s.Interactions[1].ID != "v1_mine" {
		t.Fatalf("session = %+v, %v; want v1_other then v1_mine", s, err)
	}
	if got := s.Interactions[1].Parameters.Size; got != "2K" {
		t.Errorf("size stored as %q, want 2K", got)
	}
}

// TestSessionCutOff: when the connection drops after the image, the image is
// saved with a warning, but the session isn't advanced, since the API cancels
// interactions whose connection drops.
func TestSessionCutOff(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out := filepath.Join(t.TempDir(), "out.png")
	events := append([]string{`{"interaction":{"id":"v1_cut"},"event_type":"interaction.created"}`}, imageEvents(t)...)
	stderr, _, err := generateCLI(t, cutOff(sse(events...)), nil, "--session", "fox", "--search", "web", "-o", out, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("image not saved: %v", err)
	}
	for _, want := range []string{"session not updated: the response was cut short", "lost after the image arrived", "search suggestions and sources may be missing"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	path, _ := sessionFile("fox")
	if _, err := os.Stat(path); err == nil {
		t.Error("session saved")
	}
}

// TestSessionTurns runs the CLI twice in one session against a fake API and
// checks that the second turn continues from the first turn's interaction,
// and that a third turn with --step 1 branches off the first.
func TestSessionTurns(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	b64 := base64.StdEncoding.EncodeToString(testPNG(t, color.Black))
	var reqs []string
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, string(b))
		turn++
		id := []string{"", "v1_first", "v1_second", "v1_third"}[turn]
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(
			`{"interaction":{"id":"`+id+`"},"event_type":"interaction.created"}`,
			`{"index":0,"step":{"type":"model_output"},"event_type":"step.start"}`,
			`{"index":0,"delta":{"type":"image","mime_type":"image/png","data":"`+b64+`"},"event_type":"step.delta"}`,
			`{"index":0,"event_type":"step.stop"}`,
			`{"interaction":{"id":"`+id+`","status":"completed"},"event_type":"interaction.completed"}`,
		))
	}))
	defer srv.Close()
	t.Setenv("GOOGLE_GEMINI_BASE_URL", srv.URL)

	resetCLI(t)
	out := filepath.Join(t.TempDir(), "out.png")
	var stderr bytes.Buffer
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(&stderr)
	generate := func(extra ...string) error {
		args := []string{"--env-file", "/dev/null", "--api-key", "k", "--model", "m", "generate", "--preview=false", "--size=", "--aspect-ratio=", "--search=", "--thoughts=false", "-o", out}
		rootCmd.SetArgs(append(args, extra...))
		return rootCmd.Execute()
	}
	for _, args := range [][]string{
		{"--session", "fox", "a fox"},
		{"--session", "fox", "add a scarf"},
		{"--session", "fox", "--step", "1", "-i", "../testdata/girl.jpg", "give her this hat"},
	} {
		if err := generate(args...); err != nil {
			t.Fatalf("%v: %v (stderr: %s)", args, err, stderr.String())
		}
	}

	if strings.Contains(reqs[0], "previous_interaction_id") {
		t.Errorf("first turn continued a session: %s", reqs[0])
	}
	if !strings.Contains(reqs[1], `"previous_interaction_id":"v1_first"`) {
		t.Errorf("second turn didn't continue the first: %s", reqs[1])
	}
	if !strings.Contains(reqs[2], `"previous_interaction_id":"v1_first"`) {
		t.Errorf("--step 1 didn't continue the first turn: %s", reqs[2])
	}
	if !strings.Contains(reqs[2], `"mime_type":"image/jpeg","type":"image"`) {
		t.Errorf("reference image not sent: %.300s", reqs[2])
	}
	saved, err := loadSession(filepath.Join(state, "gemini-image", "sessions", "fox.json"))
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{
		"env-file": "/dev/null", "model": "m", "preview": "false", "size": "", "aspect-ratio": "",
		"search": "", "thoughts": "false", "session": "fox", "output": out,
	}
	stepFlags := maps.Clone(flags)
	stepFlags["step"] = "1"
	stepFlags["image"] = "[../testdata/girl.jpg]"
	girl, _ := filepath.Abs("../testdata/girl.jpg")
	want := &session{Interactions: []interaction{
		{ID: "v1_first", Prompt: "a fox", File: out, Parameters: genParams{Model: "m"}, Flags: flags},
		{ID: "v1_second", Prompt: "add a scarf", File: out, Parameters: genParams{Model: "m"}, Flags: flags},
		{ID: "v1_third", Prompt: "give her this hat", Images: []string{girl}, File: out, Parameters: genParams{Model: "m"}, Flags: stepFlags},
	}}
	if !reflect.DeepEqual(saved, want) {
		t.Errorf("saved session:\n%+v\nwant:\n%+v", saved, want)
	}
	if saved.lastID() != "v1_third" {
		t.Errorf("lastID = %q", saved.lastID())
	}
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}

	for _, args := range [][]string{
		{"--session", "fox", "--step", "0", "x"},
		{"--session", "fox", "--step", "4", "x"}, // only 3 steps
		{"--session", "new", "--step", "1", "x"},
		{"--session", "", "--step", "1", "x"},
	} {
		if got := exitCode(generate(args...)); got != 2 {
			t.Errorf("%v: exit code %d, want 2", args, got)
		}
	}
	if len(reqs) != 3 {
		t.Errorf("invalid --step reached the API: %d requests", len(reqs))
	}
}

// TestSessionCompact: compacting keeps the steps the last one was built on
// and clears their --step flags; a --step that doesn't name an earlier step
// leaves the session as it was.
func TestSessionCompact(t *testing.T) {
	// 1, 2, 3, 4 from step 2, 5, 6 from step 4.
	s := &session{Interactions: []interaction{
		{ID: "1", Flags: map[string]string{"session": "fox"}},
		{ID: "2", Flags: map[string]string{"session": "fox"}},
		{ID: "3", Flags: map[string]string{"session": "fox"}},
		{ID: "4", Flags: map[string]string{"session": "fox", "step": "2"}},
		{ID: "5"},
		{ID: "6", Flags: map[string]string{"step": "4"}},
	}}
	kept, err := s.compact()
	if err != nil || !slices.Equal(kept, []int{1, 2, 4, 6}) {
		t.Fatalf("kept %v, %v; want [1 2 4 6]", kept, err)
	}
	want := []interaction{
		{ID: "1", Flags: map[string]string{"session": "fox"}},
		{ID: "2", Flags: map[string]string{"session": "fox"}},
		{ID: "4", Flags: map[string]string{"session": "fox"}},
		{ID: "6"},
	}
	if !reflect.DeepEqual(s.Interactions, want) {
		t.Errorf("got %+v\nwant %+v", s.Interactions, want)
	}

	for _, step := range []string{"x", "0", "3", "4"} {
		s := &session{Interactions: []interaction{{ID: "1"}, {ID: "2"}, {ID: "3", Flags: map[string]string{"step": step}}}}
		before := fmt.Sprint(s.Interactions)
		if _, err := s.compact(); err == nil || fmt.Sprint(s.Interactions) != before {
			t.Errorf("--step %q on step 3: err = %v, session %v", step, err, s.Interactions)
		}
	}
}
