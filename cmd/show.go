package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:     "session <name>",
	Short:   "Show the prompts and images of a multi-turn editing session",
	Example: `  gemini-image session fox`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: runSession,
}

func init() { rootCmd.AddCommand(sessionCmd) }

func runSession(cmd *cobra.Command, args []string) error {
	path, err := sessionFile(args[0])
	if err != nil {
		return usageError{err}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no session named %q", args[0])
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
