package cmd

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// statusLine is an animated one-line status ("⠋ thinking (3s)") redrawn in
// place on a terminal. A nil *statusLine is valid and draws nothing, which is
// what callers use when stderr isn't a terminal.
type statusLine struct {
	w     io.Writer
	start time.Time
	done  chan struct{}
	wg    sync.WaitGroup

	mu    sync.Mutex
	msg   string
	frame int
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newStatusLine(w io.Writer, msg string) *statusLine {
	s := &statusLine{w: w, msg: msg, start: time.Now(), done: make(chan struct{})}
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

func (s *statusLine) draw() {
	elapsed := time.Since(s.start).Truncate(time.Second)
	fmt.Fprintf(s.w, "\r\x1b[2K%s %s (%s)", spinner[s.frame%len(spinner)], s.msg, elapsed)
}

func (s *statusLine) clear() { fmt.Fprint(s.w, "\r\x1b[2K") }
