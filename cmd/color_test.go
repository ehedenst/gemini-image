package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPaint(t *testing.T) {
	if got := paint(false, sgrRed, "x"); got != "x" {
		t.Errorf("off: got %q", got)
	}
	if got := paint(true, sgrRed, "x"); got != "\x1b[1;31mx\x1b[0m" {
		t.Errorf("on: got %q", got)
	}
}

func TestColorThought(t *testing.T) {
	got := colorThought("**Defining Need**\n\nI'm thinking.")
	want := "\x1b[1;35mDefining Need\x1b[0m\n\n\x1b[2mI'm thinking.\x1b[0m"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Thoughts keep their Markdown headings when not colored (pipes, NO_COLOR).
func TestThoughtNoColor(t *testing.T) {
	var out bytes.Buffer
	p := &progress{errOut: &out, thoughts: true}
	p.thoughtText("**Heading**\nbody")
	p.flushThought()
	if got := out.String(); got != "**Heading**\nbody\n\n" || strings.Contains(got, "\x1b") {
		t.Errorf("got %q", got)
	}
}
