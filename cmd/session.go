package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Session names become file names, so only allow safe characters (and no
// leading dot, which rules out "." and "..").
var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// sessionFile returns where a --session stores its last interaction ID:
// $XDG_STATE_HOME/gemini-image/sessions/<name>, by default under ~/.local/state.
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
	return filepath.Join(dir, "gemini-image", "sessions", name), nil
}

// loadSession returns the session's last interaction ID, or "" for a new session.
func loadSession(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// saveSession records id as the session's last interaction, which the next
// turn continues from.
func saveSession(path, id string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id+"\n"), 0o600)
}
