package cmd

import (
	"bytes"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImageFormat(t *testing.T) {
	if f, err := imageFormat("", ""); f != nil || err != nil {
		t.Errorf("no flags: got %v, %v; want nil, nil", f, err)
	}
	f, err := imageFormat("16:9", "2k")
	if err != nil {
		t.Fatal(err)
	}
	img := f.ResponseFormat.ImageResponseFormat
	if *img.AspectRatio != "16:9" || *img.ImageSize != "2K" {
		t.Errorf("got aspect %v size %v", *img.AspectRatio, *img.ImageSize)
	}
	for _, bad := range [][2]string{{"7:3", ""}, {"", "3K"}} {
		if _, err := imageFormat(bad[0], bad[1]); err == nil {
			t.Errorf("imageFormat(%q, %q): expected error", bad[0], bad[1])
		}
	}
}

// TestSuggestionsOnWriteError: search suggestions must be printed even when
// the image can't be saved.
func TestSuggestionsOnWriteError(t *testing.T) {
	chips := `<a class=\"chip\" href=\"https://www.google.com/search?q=x\">x</a>`
	events := append(imageEvents(t),
		`{"index":1,"step":{"type":"google_search_result","call_id":"s1","result":[{"search_suggestions":"`+chips+`"}]},"event_type":"step.start"}`,
		`{"index":1,"event_type":"step.stop"}`,
	)
	// The directory passes the check before generating, then goes away.
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stderr, _, err := generateCLI(t, sse(events...), func() { _ = os.Remove(dir) }, "-o", filepath.Join(dir, "out.png"), "x")
	if err == nil || !strings.Contains(err.Error(), "writing image") {
		t.Fatalf("err = %v, want a write error", err)
	}
	if !strings.Contains(stderr, "Google Search suggestions:") {
		t.Errorf("suggestions not printed: %q", stderr)
	}
}

// TestBadOutput: an --output that can't be written is a usage error, found
// before anything is generated.
func TestBadOutput(t *testing.T) {
	dir := t.TempDir()
	for _, out := range []string{dir, filepath.Join(dir, "missing", "out.png")} {
		_, requests, err := generateCLI(t, sse(imageEvents(t)...), nil, "-o", out, "x")
		if exitCode(err) != 2 || requests != 0 {
			t.Errorf("-o %s: err = %v, %d requests; want a usage error and none", out, err, requests)
		}
	}
}

// TestOutputTypeWarning: the API decides the image type, so an --output
// extension that says otherwise gets a warning.
func TestOutputTypeWarning(t *testing.T) {
	dir := t.TempDir()
	for name, warn := range map[string]bool{"out.png": false, "out.jpg": true, "out": false} {
		stderr, _, err := generateCLI(t, sse(imageEvents(t)...), nil, "-o", filepath.Join(dir, name), "x")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.Contains(stderr, "warning:"); got != warn {
			t.Errorf("%s: warning = %v, want %v (stderr %q)", name, got, warn, stderr)
		}
	}
}

// TestSaveImageNewName: without --output, a run doesn't overwrite an image
// from an earlier run in the same second.
func TestSaveImageNewName(t *testing.T) {
	t.Chdir(t.TempDir())
	// Take the names for this second and the next, in case it ticks over.
	now := time.Now()
	for _, ts := range []time.Time{now, now.Add(time.Second)} {
		if err := os.WriteFile("image-"+ts.Format("20060102-150405")+".png", nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := testPNG(t, color.Black)
	path, err := saveImage("", "image/png", data)
	if err != nil || !strings.HasPrefix(path, "image-") || !strings.HasSuffix(path, "-2.png") {
		t.Fatalf("got %q, %v; want image-<timestamp>-2.png", path, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Errorf("%s: %v", path, err)
	}
}
