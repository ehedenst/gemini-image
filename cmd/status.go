package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/term"
	"golang.org/x/text/width"
)

// statusLine is an animated one-line status ("⠋ thinking (3s)") redrawn in
// place on a terminal. A nil *statusLine is valid and draws nothing, which is
// what callers use when stderr isn't a terminal.
type statusLine struct {
	w     io.Writer
	cols  func() int // terminal width, read on every draw to follow resizes
	start time.Time
	color bool
	done  chan struct{}
	wg    sync.WaitGroup

	mu    sync.Mutex
	msg   string
	frame int
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newStatusLine(f *os.File, msg string, color bool) *statusLine {
	cols := func() int {
		w, _, err := term.GetSize(int(f.Fd()))
		if err != nil || w <= 0 {
			return 80
		}
		return w
	}
	s := &statusLine{w: f, cols: cols, msg: msg, start: time.Now(), color: color, done: make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			s.mu.Lock()
			s.draw()
			s.frame++
			s.mu.Unlock()
			select {
			case <-s.done:
				return
			case <-t.C:
			}
		}
	}()
	return s
}

// Set changes the message.
func (s *statusLine) Set(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msg = msg
	s.draw()
}

// Do runs fn with the status line cleared, so fn can write to the terminal,
// and redraws it afterwards.
func (s *statusLine) Do(fn func()) {
	if s == nil {
		fn()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
	fn()
	s.draw()
}

// Stop removes the status line for good. It is safe to call more than once.
func (s *statusLine) Stop() {
	if s == nil {
		return
	}
	select {
	case <-s.done:
		return
	default:
		close(s.done)
	}
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clear()
}

// draw redraws the line, cut to fit the terminal. A line that wrapped could
// not be cleared with \r, so every redraw would leave a copy behind.
func (s *statusLine) draw() {
	suffix := fmt.Sprintf(" (%s)", time.Since(s.start).Truncate(time.Second))
	// spinner + space take 2 columns; keep the last column free as well,
	// since writing there makes some terminals wrap.
	msg := truncate(oneLine(s.msg), s.cols()-3-displayWidth(suffix))
	// Color after truncating, so escape codes don't count as columns.
	fmt.Fprintf(s.w, "\r\x1b[2K%s %s%s", paint(s.color, sgrCyan, spinner[s.frame%len(spinner)]), msg, paint(s.color, sgrDim, suffix))
}

// oneLine strips control characters and collapses whitespace, so messages
// built from API data (e.g. search queries) stay on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(sanitize(s)), " ") }

// truncate cuts s to at most max display columns, ending in "…" if cut.
func truncate(s string, max int) string {
	if displayWidth(s) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	w := 0
	for i, r := range s {
		if w+runeWidth(r) > max-1 { // leave room for the ellipsis
			return s[:i] + "…"
		}
		w += runeWidth(r)
	}
	return s
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth is the number of terminal columns r takes: 2 for wide East Asian
// characters, 0 for combining marks, 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r):
		return 0
	case width.LookupRune(r).Kind() == width.EastAsianWide, width.LookupRune(r).Kind() == width.EastAsianFullwidth:
		return 2
	default:
		return 1
	}
}

func (s *statusLine) clear() { fmt.Fprint(s.w, "\r\x1b[2K") }
