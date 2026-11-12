package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session <name>",
	Short: "Show the prompts and images of a multi-turn editing session",
	Example: `  gemini-image session fox
  gemini-image session --delete fox`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: runSession,
}

func init() {
	sessionCmd.Flags().Bool("delete", false, "delete the session's history instead of showing it (the images are kept)")
	rootCmd.AddCommand(sessionCmd)
}

func runSession(cmd *cobra.Command, args []string) error {
	path, err := sessionFile(args[0])
	if err != nil {
		return usageError{err}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no session named %q", args[0])
	}
	if del, _ := cmd.Flags().GetBool("delete"); del {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("deleting session: %w", err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "deleted session %q\n", args[0])
		return nil
	}
	s, err := loadSession(path)
	if err != nil {
		return fmt.Errorf("reading session: %w", err)
	}
	showSession(cmd.OutOrStdout(), cmd.ErrOrStderr(), s)
	return nil
}

// showSession prints each turn's prompt, draws its image when out is a
// terminal with kitty graphics support, and prints the image path followed by
// a blank line.
func showSession(out, errOut io.Writer, s *session) {
	color := useColor(out)
	f, _ := out.(*os.File)
	for i, in := range s.Interactions {
		from := ""
		if step, ok := in.Flags["step"]; ok { // run with --step: it branched off that step
			from = " " + paint(color, sgrDim, "(from step "+oneLine(step)+")")
		}
		fmt.Fprintf(out, "%s %s%s\n", paint(color, sgrLabel, fmt.Sprintf("%d.", i+1)), oneLine(in.Prompt), from)
		img, err := os.ReadFile(in.File)
		switch {
		case err != nil:
			fmt.Fprintln(errOut, paint(useColor(errOut), sgrYellow, "warning:"), err)
		case f != nil:
			// Leave room for the path, the blank line and the next prompt.
			if err := kitty.Preview(f, img, 3); err != nil && !errors.Is(err, errors.ErrUnsupported) {
				fmt.Fprintln(errOut, paint(useColor(errOut), sgrYellow, "warning:"), "preview failed:", err)
			}
		}
		fmt.Fprintf(out, "%s\n\n", sanitize(in.File))
	}
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List the multi-turn editing sessions",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.NoArgs(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := sessionsDir()
		if err != nil {
			return err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "no sessions")
			return nil
		}
		listSessions(cmd.OutOrStdout(), cmd.ErrOrStderr(), paths)
		return nil
	},
}

func init() { rootCmd.AddCommand(sessionsCmd) }

// thumbnailRows is how many terminal rows a session thumbnail takes.
const thumbnailRows = 10

// listSessions prints each session's name and number of steps and, on a
// terminal with kitty graphics support, a thumbnail of its last image.
func listSessions(out, errOut io.Writer, paths []string) {
	color, warn := useColor(out), paint(useColor(errOut), sgrYellow, "warning:")
	f, _ := out.(*os.File)
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		s, err := loadSession(path)
		if err != nil {
			fmt.Fprintln(errOut, warn, err)
			continue
		}
		steps := fmt.Sprintf("%d steps", len(s.Interactions))
		if len(s.Interactions) == 1 {
			steps = "1 step"
		}
		fmt.Fprintf(out, "%s  %s\n", paint(color, sgrLabel, name), paint(color, sgrDim, steps))
		if f == nil || len(s.Interactions) == 0 {
			continue
		}
		last := s.Interactions[len(s.Interactions)-1].File
		img, err := os.ReadFile(last)
		if err != nil {
			fmt.Fprintln(errOut, warn, err)
			continue
		}
		switch err := kitty.Thumbnail(f, img, thumbnailRows); {
		case err == nil:
			fmt.Fprintln(out) // a blank line between thumbnails
		case !errors.Is(err, errors.ErrUnsupported):
			fmt.Fprintln(errOut, warn, "thumbnail failed:", err)
		}
	}
}
