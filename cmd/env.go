package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// loadEnvFile loads path into the environment without overriding variables
// that are already set. A missing file is only an error when required.
func loadEnvFile(path string, required bool) error {
	err := godotenv.Load(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading env file: %w", err)
	}
	return nil
}

// setting returns the value for key from its flag or environment variable.
// Values that did not come from a flag are passed through resolveValue, so
// "!command" is only run when the flag doesn't override it.
func setting(cmd *cobra.Command, key string) (string, error) {
	v := viper.GetString(key)
	if cmd.Flags().Changed(key) {
		return v, nil
	}
	return resolveValue(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), v)
}

// resolveValue runs v as a shell command if it starts with "!" and returns its
// trimmed stdout; any other value is returned unchanged.
// stdin and stderr are passed to the command so it can prompt (e.g. op sign-in).
func resolveValue(ctx context.Context, stdin io.Reader, stderr io.Writer, v string) (string, error) {
	command, ok := strings.CutPrefix(v, "!")
	if !ok {
		return v, nil
	}
	// Running a shell command is the intended feature: the value comes from the
	// user's own environment or .env file, which is trusted like a shell rc file.
	// shortcut: uses sh, so no Windows support; switch to cmd /C there if needed.
	c := exec.CommandContext(ctx, "sh", "-c", command) // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c.Stdin, c.Stderr = stdin, stderr
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("running %q: %w", command, err)
	}
	return strings.TrimSpace(string(out)), nil
}
