package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTruncate(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 10, "this is t…"},
		{"lämpliga kläder", 8, "lämplig…"},
		{"日本語テキスト", 7, "日本語…"}, // wide runes take 2 columns: 6 + ellipsis
		{"anything", 0, ""},
		{"anything", -5, ""},
	} {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if w := displayWidth(truncate(c.in, c.max)); c.max >= 0 && w > c.max {
			t.Errorf("truncate(%q, %d) is %d columns wide", c.in, c.max, w)
		}
	}
}

// TestStatusLineFitsTerminal is the regression test for a long status (two
// search queries) wrapping, which left a copy of the line on every redraw.
func TestStatusLineFitsTerminal(t *testing.T) {
	var buf bytes.Buffer
	s := &statusLine{
		w:     &buf,
		cols:  func() int { return 60 },
		start: time.Now(),
		msg:   "searching: weather forecast Stockholm Sweden November 13-19 2026;\nlämpliga kläder väder Stockholm november",
	}
	s.draw()
	line := strings.TrimPrefix(buf.String(), "\r\x1b[2K")
	if strings.ContainsAny(line, "\r\n") {
		t.Errorf("status line contains a line break: %q", line)
	}
	if w := displayWidth(line); w >= 60 {
		t.Errorf("status line is %d columns, want < 60: %q", w, line)
	}
	if !strings.HasSuffix(line, "… (0s)") {
		t.Errorf("expected truncated message with elapsed time, got %q", line)
	}
}
