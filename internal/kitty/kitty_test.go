//go:build unix

package kitty

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"
	"testing"
)

func TestFitColumns(t *testing.T) {
	for _, c := range []struct {
		name                                        string
		cols, rows, xpix, ypix, imgW, imgH, reserve int
		want                                        int
	}{
		{"fits natively", 200, 50, 1600, 800, 320, 160, 2, 0},
		// 10x20px cells: 200x20 cells, limited by width
		{"too wide", 100, 50, 1000, 1000, 2000, 400, 2, 100},
		// 10x20px cells: 40x100 cells, limited to 48 rows -> 0.48 * 40 cols
		{"too tall", 100, 50, 1000, 1000, 400, 2000, 2, 19},
		// same, but 5 rows reserved for grounding lines: 45 rows -> 0.45 * 40 cols
		{"too tall, more reserved", 100, 50, 1000, 1000, 400, 2000, 5, 18},
		// assumed 8x16px cells: 128x64 cells, limited to 22 rows -> 0.34375 * 128 cols
		{"no pixel info", 80, 24, 0, 0, 1024, 1024, 2, 44},
		{"unknown size", 0, 0, 0, 0, 100, 100, 2, 0},
	} {
		if got := fitColumns(c.cols, c.rows, c.xpix, c.ypix, c.imgW, c.imgH, c.reserve); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestWriteChunks(t *testing.T) {
	data := bytes.Repeat([]byte{0xAB}, 10000) // ~13336 base64 chars -> 4 chunks
	var b bytes.Buffer
	if err := write(&b, data, ",c=42"); err != nil {
		t.Fatal(err)
	}
	seqs := regexp.MustCompile(`\x1b_G([^;]*);([^\x1b]*)\x1b\\`).FindAllStringSubmatch(b.String(), -1)
	if len(seqs) != 4 {
		t.Fatalf("got %d chunks, want 4", len(seqs))
	}
	if seqs[0][1] != "a=T,f=100,q=2,c=42,m=1" || seqs[1][1] != "m=1" || seqs[3][1] != "m=0" {
		t.Errorf("unexpected control data: %q %q %q", seqs[0][1], seqs[1][1], seqs[3][1])
	}
	var payload strings.Builder
	for _, s := range seqs {
		if len(s[2]) > chunkSize {
			t.Errorf("chunk of %d bytes exceeds %d", len(s[2]), chunkSize)
		}
		payload.WriteString(s[2])
	}
	got, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("payload does not round-trip: %v", err)
	}
}

func TestToPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	var j bytes.Buffer
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	out, cfg, err := toPNG(j.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 3 || cfg.Height != 2 {
		t.Errorf("size %dx%d, want 3x2", cfg.Width, cfg.Height)
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("output is not PNG: %v", err)
	}
	if _, _, err := toPNG([]byte("not an image")); err == nil {
		t.Error("expected error for invalid image")
	}
}

func TestShrink(t *testing.T) {
	for _, c := range []struct{ w, h, wantW, wantH int }{
		{1376, 768, 384, 214},
		{768, 1376, 214, 384},
		{100, 50, 100, 50}, // already small
	} {
		m := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
		if b := shrink(m, thumbnailSide).Bounds(); b.Dx() != c.wantW || b.Dy() != c.wantH {
			t.Errorf("%dx%d: got %dx%d, want %dx%d", c.w, c.h, b.Dx(), b.Dy(), c.wantW, c.wantH)
		}
	}
}

func TestThumbnailColumns(t *testing.T) {
	for _, c := range []struct {
		name                                       string
		cols, rows, xpix, ypix, imgW, imgH, height int
		want                                       int
	}{
		// 10x20px cells: 10 rows = 200px high, square -> 200px = 20 cols
		{"square", 100, 50, 1000, 1000, 512, 512, 10, 20},
		// 16:9 -> 200px high is 356px wide = 36 cols, even from a smaller source
		{"wide, upscaled", 100, 50, 1000, 1000, 256, 144, 10, 36},
		{"capped at window width", 30, 50, 300, 1000, 512, 128, 10, 30},
		// assumed 8x16px cells: 160px high, square -> 20 cols
		{"no pixel info", 80, 24, 0, 0, 512, 512, 10, 20},
		{"no window info", 0, 0, 0, 0, 512, 512, 10, 20},
	} {
		if got := thumbnailColumns(c.cols, c.rows, c.xpix, c.ypix, c.imgW, c.imgH, c.height); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
