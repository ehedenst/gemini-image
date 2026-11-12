package cmd

import (
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"
)

// ANSI SGR codes used for terminal output.
const (
	sgrDim     = "2"
	sgrRed     = "1;31"
	sgrYellow  = "33"
	sgrCyan    = "36"
	sgrHeading = "1;35" // bold magenta: thought headings
	sgrLabel   = "1;34" // bold blue: "Google Search suggestions:", "Source:"
	sgrLink    = "4;36" // underlined cyan
)

// useColor reports whether w is a terminal and NO_COLOR (https://no-color.org)
// is unset or empty.
func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""
}

// paint wraps s in the SGR code when on, otherwise returns s unchanged.
func paint(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// thoughtHeading matches the "**Heading**" lines in thought summaries.
var thoughtHeading = regexp.MustCompile(`^\*\*(.+)\*\*$`)

// colorThought shows thought headings bold without the Markdown asterisks and
// dims the rest, so the thoughts read as secondary to the result.
func colorThought(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if m := thoughtHeading.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			lines[i] = paint(true, sgrHeading, m[1])
		} else {
			lines[i] = paint(true, sgrDim, l)
		}
	}
	return strings.Join(lines, "\n")
}
