package cmd

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
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

// TestOutputConversion: the API decides the image type; an --output
// extension for another type converts the image to it, if that type can be
// written, and any other extension gets the image as it is.
func TestOutputConversion(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]string{
		"out.png": "image/png", "out.jpg": "image/jpeg", "out.JPEG": "image/jpeg", "out.gif": "image/gif",
		"out": "image/png", "out.txt": "image/png",
	} {
		path := filepath.Join(dir, name)
		stderr, _, err := generateCLI(t, sse(imageEvents(t)...), nil, "-o", path, "x")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if got := http.DetectContentType(data); got != want || err != nil || cfg.Width != 4 || cfg.Height != 4 {
			t.Errorf("%s: saved %s (%v, %dx%d), want a 4x4 %s", name, got, err, cfg.Width, cfg.Height, want)
		}
		// Only an extension the image doesn't match after all gets a warning.
		if warn := strings.Contains(stderr, "warning:"); warn != (name == "out.txt") {
			t.Errorf("%s: warning = %v (stderr %q)", name, warn, stderr)
		}
	}
	// An image type it can't be converted to is caught before generating.
	if _, requests, err := generateCLI(t, sse(imageEvents(t)...), nil, "-o", filepath.Join(dir, "out.webp"), "x"); exitCode(err) != 2 || requests != 0 {
		t.Errorf("-o out.webp: err = %v, %d requests; want a usage error and none", err, requests)
	}
}

// TestConvertImage: a JPEG from the API saved as .png is a PNG of the same
// picture, and an image that can't be decoded is kept as it is.
func TestConvertImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.RGBA{200, 30, 30, 255}), image.Point{}, draw.Src)
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, src, nil); err != nil {
		t.Fatal(err)
	}
	out, err := convertImage("fox.png", "image/jpeg", jpg.Bytes())
	if err != nil || http.DetectContentType(out) != "image/png" {
		t.Fatalf("got %s, %v; want a PNG", http.DetectContentType(out), err)
	}
	m, err := png.Decode(bytes.NewReader(out))
	if err != nil || m.Bounds() != src.Bounds() {
		t.Fatalf("PNG: %v, bounds %v", err, m.Bounds())
	}
	if r, g, b, _ := m.At(4, 4).RGBA(); r>>8 < 190 || g>>8 > 45 || b>>8 > 45 {
		t.Errorf("color %d,%d,%d; want about 200,30,30", r>>8, g>>8, b>>8)
	}

	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8 not really")
	if out, err := convertImage("fox.png", "image/webp", webp); err == nil || !bytes.Equal(out, webp) {
		t.Errorf("undecodable: got %d bytes, %v; want the data as it is and an error", len(out), err)
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
