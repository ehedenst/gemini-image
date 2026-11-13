package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var sessionCmd = &cobra.Command{
	Use:   "session <name>",
	Short: "Show the prompts and images of a multi-turn editing session",
	Example: `  gemini-image session fox
  gemini-image session --compact fox
  gemini-image session --delete fox
  gemini-image session --delete=all fox
  gemini-image session --delete --force fox`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: runSession,
}

func init() {
	addDeleteFlag(sessionCmd, "delete the session's history instead of showing it; --delete=all deletes the images it generated too")
	sessionCmd.Flags().Bool("compact", false, "keep only the steps the last one was built on, dropping other branches (the images are kept)")
	sessionCmd.Flags().BoolP("force", "f", false, "with --delete, don't ask for confirmation")
	rootCmd.AddCommand(sessionCmd)
}

// Values of --delete: just the history (a bare --delete), or the generated
// images too.
const (
	deleteHistory = "history"
	deleteAll     = "all"
)

// addDeleteFlag adds --delete[=image] to cmd.
func addDeleteFlag(cmd *cobra.Command, usage string) {
	cmd.Flags().String("delete", "", usage)
	cmd.Flags().Lookup("delete").NoOptDefVal = deleteHistory
	_ = cmd.RegisterFlagCompletionFunc("delete", cobra.FixedCompletions([]string{deleteHistory, deleteAll}, cobra.ShellCompDirectiveNoFileComp))
}

func runSession(cmd *cobra.Command, args []string) error {
	path, err := sessionFile(args[0])
	if err != nil {
		return usageError{err}
	}
	op, err := sessionOp(cmd)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no session named %q", args[0])
	}
	if op != nil {
		if ok, err := confirmDelete(cmd, []string{args[0]}); !ok {
			return err
		}
		return op(cmd.ErrOrStderr(), args[0], path)
	}
	s, err := loadSession(path)
	if err != nil {
		return fmt.Errorf("reading session: %w", err)
	}
	showSession(cmd.OutOrStdout(), cmd.ErrOrStderr(), s, viper.GetBool("preview"))
	return nil
}

// sessionOp returns the operation --delete or --compact asks for, or nil.
func sessionOp(cmd *cobra.Command) (func(errOut io.Writer, name, path string) error, error) {
	del, _ := cmd.Flags().GetString("delete")
	compact, _ := cmd.Flags().GetBool("compact")
	switch {
	case del != "" && compact:
		return nil, usageError{errors.New("--delete and --compact can't be used together")}
	case del == deleteHistory, del == deleteAll:
		return func(errOut io.Writer, name, path string) error {
			return deleteSession(errOut, name, path, del == deleteAll)
		}, nil
	case del != "":
		return nil, usageError{fmt.Errorf("--delete: unknown value %q, use --delete or --delete=%s", del, deleteAll)}
	case compact:
		return compactSession, nil
	}
	return nil, nil
}

// confirmDelete reports whether to go on: it asks before --delete deletes
// the named sessions, unless --force is given. Answering anything but y or
// yes isn't an error, the user chose not to; no answer at all (as in a
// script without --force) is.
func confirmDelete(cmd *cobra.Command, names []string) (bool, error) {
	del, _ := cmd.Flags().GetString("delete")
	force, _ := cmd.Flags().GetBool("force")
	if del == "" || force {
		return true, nil
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	question, hint := "delete session "+quoted[0]+"?", "The images are kept."
	switch {
	case del == deleteAll && len(names) > 1:
		question = fmt.Sprintf("delete %d sessions and their images: %s?", len(names), strings.Join(quoted, ", "))
	case del == deleteAll:
		question = "delete session " + quoted[0] + " and its images?"
	case len(names) > 1:
		question = fmt.Sprintf("delete %d sessions: %s?", len(names), strings.Join(quoted, ", "))
	}
	if del == deleteAll {
		hint = "Reference images are kept."
	}
	errOut := cmd.ErrOrStderr()
	ok, err := confirm(cmd.Context(), cmd.InOrStdin(), errOut, question, hint)
	switch {
	case errors.Is(err, errNoAnswer):
		return false, errors.New("no answer, nothing deleted; use --force to delete without asking")
	case err != nil:
		return false, err
	case !ok:
		fmt.Fprintln(errOut, paint(useColor(errOut), sgrDim, "nothing deleted"))
	}
	return ok, nil
}

// errNoAnswer is confirm's error when the input ends before an answer.
var errNoAnswer = errors.New("no answer")

// confirm asks question, followed by hint, on errOut and reads the answer
// from in: true for y or yes. Ctrl-C ends the wait, which a blocked read
// alone wouldn't, as Execute catches the signal. An answer that isn't typed
// on a terminal is echoed, so later output starts on a line of its own.
func confirm(ctx context.Context, in io.Reader, errOut io.Writer, question, hint string) (bool, error) {
	c := useColor(errOut)
	fmt.Fprintf(errOut, "%s %s %s ", paint(c, sgrBold, question), paint(c, sgrDim, hint), paint(c, sgrNote, "[y/N]"))
	answer := make(chan string, 1)
	go func() { // left blocked on in after Ctrl-C, until the process exits
		s, _ := bufio.NewReader(in).ReadString('\n')
		answer <- s
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(errOut)
		return false, ctx.Err()
	case s := <-answer:
		if f, ok := in.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) || s == "" {
			fmt.Fprintln(errOut, strings.TrimSpace(s))
		}
		if s == "" { // ReadString only returns "" with an error, such as EOF
			return false, errNoAnswer
		}
		s = strings.ToLower(strings.TrimSpace(s))
		return s == "y" || s == "yes", nil
	}
}

// deleteSession deletes a session's history and, with images, the images
// its steps generated, but not the reference images. The images go first,
// so if one can't be deleted, the history still lists them all.
func deleteSession(errOut io.Writer, name, path string, images bool) error {
	deleted := 0
	if images {
		s, err := loadSession(path)
		if err != nil {
			return fmt.Errorf("reading session %q: %w", name, err)
		}
		for _, in := range s.Interactions {
			// Steps saved with the same -o share a file; it's gone the second time.
			switch err := os.Remove(in.File); {
			case err == nil:
				deleted++
			case !errors.Is(err, fs.ErrNotExist):
				return fmt.Errorf("deleting the images of session %q: %w", name, err)
			}
		}
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("deleting session %q: %w", name, err)
	}
	c := useColor(errOut)
	msg := fmt.Sprintf("%s session %s", paint(c, sgrGreen, "deleted"), paint(c, sgrBold, strconv.Quote(name)))
	if images {
		msg += fmt.Sprintf(" and %d image", deleted)
		if deleted != 1 {
			msg += "s"
		}
	}
	fmt.Fprintln(errOut, msg)
	return nil
}

// compactSession keeps only the steps of a session the last one was built
// on (see session.compact).
func compactSession(errOut io.Writer, name, path string) error {
	s, err := loadSession(path)
	if err != nil {
		return fmt.Errorf("reading session %q: %w", name, err)
	}
	all := len(s.Interactions)
	kept, err := s.compact()
	if err != nil {
		return fmt.Errorf("compacting session %q: %w", name, err)
	}
	if err := saveSession(path, s); err != nil {
		return fmt.Errorf("saving session %q: %w", name, err)
	}
	steps := make([]string, len(kept))
	for i, n := range kept {
		steps[i] = strconv.Itoa(n)
	}
	c := useColor(errOut)
	fmt.Fprintf(errOut, "%s session %s: kept steps %s of %d\n",
		paint(c, sgrGreen, "compacted"), paint(c, sgrBold, strconv.Quote(name)), strings.Join(steps, ", "), all)
	return nil
}

// showSession prints each turn's prompt, draws its image when preview is on
// and out is a terminal with kitty graphics support, and prints the image path
// followed by a blank line.
func showSession(out, errOut io.Writer, s *session, preview bool) {
	color := useColor(out)
	f, _ := out.(*os.File)
	if !preview {
		f = nil
	}
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
	Example: `  gemini-image sessions
  gemini-image sessions --compact
  gemini-image sessions --delete
  gemini-image sessions --delete=all
  gemini-image sessions --delete --force`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.NoArgs(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, _ []string) error {
		op, err := sessionOp(cmd)
		if err != nil {
			return err
		}
		dir, err := sessionsDir()
		if err != nil {
			return err
		}
		paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), paint(useColor(cmd.ErrOrStderr()), sgrDim, "no sessions"))
			return nil
		}
		if op != nil {
			names := make([]string, len(paths))
			for i, path := range paths {
				names[i] = strings.TrimSuffix(filepath.Base(path), ".json")
			}
			if ok, err := confirmDelete(cmd, names); !ok {
				return err
			}
			// Go on past a session that fails, and report them all at the end.
			var errs []error
			for i, path := range paths {
				errs = append(errs, op(cmd.ErrOrStderr(), names[i], path))
			}
			return errors.Join(errs...)
		}
		listSessions(cmd.OutOrStdout(), cmd.ErrOrStderr(), paths, viper.GetBool("preview"))
		return nil
	},
}

func init() {
	addDeleteFlag(sessionsCmd, "delete the history of every session; --delete=all deletes the images they generated too")
	sessionsCmd.Flags().Bool("compact", false, "compact every session, as session --compact does (the images are kept)")
	sessionsCmd.Flags().BoolP("force", "f", false, "with --delete, don't ask for confirmation")
	rootCmd.AddCommand(sessionsCmd)
}

// thumbnailRows is how many terminal rows a session thumbnail takes.
const thumbnailRows = 10

// listSessions prints each session's name and number of steps and, when
// preview is on and out is a terminal with kitty graphics support, a thumbnail
// of its last image.
func listSessions(out, errOut io.Writer, paths []string, preview bool) {
	color, warn := useColor(out), paint(useColor(errOut), sgrYellow, "warning:")
	f, _ := out.(*os.File)
	if !preview {
		f = nil
	}
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
