package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/pflag"
)

// session is the history of a --session, stored as JSON. The last
// interaction is the one the next turn continues from.
type session struct {
	Interactions []interaction `json:"interactions"`
}

// interaction is one successful turn of a session.
type interaction struct {
	ID         string            `json:"id"`
	Prompt     string            `json:"prompt"`
	Images     []string          `json:"images,omitempty"` // absolute paths of the reference images
	File       string            `json:"file"`             // absolute path of the generated image
	Parameters genParams         `json:"parameters"`
	Flags      map[string]string `json:"flags,omitempty"` // flags given on the command line, without --api-key
}

// genParams are the effective generation settings, wherever they came from
// (flag, environment or .env file).
type genParams struct {
	Model       string `json:"model"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Size        string `json:"size,omitempty"`
	Search      string `json:"search,omitempty"`
	Thoughts    bool   `json:"thoughts,omitempty"`
}

// lastID returns the ID to continue from, or "" for a new session.
func (s *session) lastID() string {
	if len(s.Interactions) == 0 {
		return ""
	}
	return s.Interactions[len(s.Interactions)-1].ID
}

// stepID returns the ID of the given step, counting from 1, for --step.
func (s *session) stepID(name string, step int) (string, error) {
	n := len(s.Interactions)
	switch {
	case n == 0:
		return "", fmt.Errorf("--step: session %q has no steps yet", name)
	case step < 1 || step > n:
		return "", fmt.Errorf("--step: session %q has steps 1 to %d, got %d", name, n, step)
	}
	return s.Interactions[step-1].ID, nil
}

// Session names become file names, so only allow safe characters (and no
// leading dot, which rules out "." and "..").
var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// sessionFile returns where a --session is stored:
// $XDG_STATE_HOME/gemini-image/sessions/<name>.json, by default under
// ~/.local/state.
func sessionFile(name string) (string, error) {
	if !sessionName.MatchString(name) {
		return "", fmt.Errorf("invalid session name %q: use letters, digits, '.', '_' and '-'", name)
	}
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "gemini-image", "sessions", name+".json"), nil
}

// loadSession reads a session; a missing file is a new, empty session.
func loadSession(path string) (*session, error) {
	s := &session{}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// saveSession writes s via a temporary file and a rename, so a crash can't
// leave a half-written history behind.
func saveSession(path string, s *session) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after a successful rename
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// givenFlags returns the flags set on the command line. --api-key is left out
// so the key never ends up in the session file.
func givenFlags(flags *pflag.FlagSet) map[string]string {
	m := map[string]string{}
	flags.Visit(func(f *pflag.Flag) {
		if f.Name != "api-key" {
			m[f.Name] = f.Value.String()
		}
	})
	return m
}
