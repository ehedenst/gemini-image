package cmd

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
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

	// Flag values outlive Execute; reset them for other tests.
	t.Cleanup(func() {
		for _, f := range []string{"session", "output", "size", "aspect-ratio", "search"} {
			_ = generateCmd.Flags().Set(f, "")
		}
		_ = generateCmd.Flags().Set("preview", "true")
		_ = generateCmd.Flags().Set("step", "0")
		generateCmd.Flags().Lookup("step").Changed = false
		for _, f := range []string{"api-key", "model", "env-file"} {
			fl := rootCmd.PersistentFlags().Lookup(f)
			_ = fl.Value.Set(fl.DefValue)
			fl.Changed = false
		}
	})
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
		{"--session", "fox", "--step", "1", "add a hat instead"},
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
	want := &session{Interactions: []interaction{
		{ID: "v1_first", Prompt: "a fox", File: out, Parameters: genParams{Model: "m"}, Flags: flags},
		{ID: "v1_second", Prompt: "add a scarf", File: out, Parameters: genParams{Model: "m"}, Flags: flags},
		{ID: "v1_third", Prompt: "add a hat instead", File: out, Parameters: genParams{Model: "m"}, Flags: stepFlags},
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
