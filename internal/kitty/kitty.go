//go:build unix

// Package kitty displays images in terminals that support the kitty graphics
// protocol (https://sw.kovidgoyal.net/kitty/graphics-protocol/).
package kitty

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register decoder
	_ "image/jpeg" // register decoder
	"image/png"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	chunkSize    = 4096 // max base64 payload per escape sequence
	queryTimeout = 2 * time.Second
	// A graphics query (1x1 RGB pixel, not stored) followed by a primary
	// device attributes request. Every terminal answers the latter, so if the
	// graphics reply hasn't arrived by then, the protocol isn't supported.
	query     = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"
	queryOK   = "\x1b_Gi=31;OK"
	daPrefix  = "\x1b[?"
	daSuffix  = 'c'
	maxDAScan = 4096
)

// Preview draws img (PNG, JPEG or GIF) on out, scaled to fit the terminal.
// It returns errors.ErrUnsupported when out isn't a terminal or the terminal
// doesn't support the kitty graphics protocol.
func Preview(out *os.File, img []byte) error {
	if !term.IsTerminal(int(out.Fd())) || os.Getenv("TERM") == "dumb" {
		return errors.ErrUnsupported
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return errors.ErrUnsupported
	}
	defer tty.Close()
	if !supported(tty) {
		return errors.ErrUnsupported
	}

	data, cfg, err := toPNG(img)
	if err != nil {
		return err
	}
	cols := 0
	if ws, err := unix.IoctlGetWinsize(int(out.Fd()), unix.TIOCGWINSZ); err == nil {
		cols = fitColumns(int(ws.Col), int(ws.Row), int(ws.Xpixel), int(ws.Ypixel), cfg.Width, cfg.Height)
	}
	return write(out, data, cols)
}

// supported queries the terminal and reports whether it answered the
// graphics query before the device attributes request.
func supported(tty *os.File) bool {
	state, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		return false
	}
	defer term.Restore(int(tty.Fd()), state)
	if _, err := io.WriteString(tty, query); err != nil {
		return false
	}

	// shortcut: on timeout the reader goroutine stays blocked on the tty until
	// the next input; fine for a CLI that exits right after, but use a
	// poll-based read if this ever runs in a long-lived process.
	result := make(chan bool, 1)
	go func() {
		var resp []byte
		buf := make([]byte, 256)
		for len(resp) < maxDAScan {
			n, err := tty.Read(buf)
			if err != nil {
				result <- false
				return
			}
			resp = append(resp, buf[:n]...)
			if i := bytes.Index(resp, []byte(daPrefix)); i >= 0 && bytes.IndexByte(resp[i:], daSuffix) >= 0 {
				result <- bytes.Contains(resp, []byte(queryOK))
				return
			}
		}
		result <- false
	}()

	select {
	case ok := <-result:
		return ok
	case <-time.After(queryTimeout):
		return false
	}
}

// toPNG returns img as PNG, converting if needed, plus its dimensions.
func toPNG(img []byte) ([]byte, image.Config, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		return nil, cfg, fmt.Errorf("reading image: %w", err)
	}
	if format == "png" {
		return img, cfg, nil
	}
	m, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, cfg, fmt.Errorf("decoding %s: %w", format, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		return nil, cfg, fmt.Errorf("converting to png: %w", err)
	}
	return buf.Bytes(), cfg, nil
}

// fitColumns returns how many terminal columns the image should span so it
// fits in the window (leaving two rows for the prompt and output), or 0 to
// draw it at its native size. xpix/ypix are the window size in pixels and
// may be 0 if the terminal doesn't report them.
func fitColumns(cols, rows, xpix, ypix, imgW, imgH int) int {
	if cols <= 0 || rows <= 0 || imgW <= 0 || imgH <= 0 {
		return 0
	}
	cellW, cellH := 8.0, 16.0 // typical cell size when the terminal doesn't say
	if xpix > 0 && ypix > 0 {
		cellW, cellH = float64(xpix)/float64(cols), float64(ypix)/float64(rows)
	}
	imgCols, imgRows := float64(imgW)/cellW, float64(imgH)/cellH
	scale := min(1, float64(cols)/imgCols, float64(max(rows-2, 1))/imgRows)
	if scale >= 1 {
		return 0
	}
	return max(1, int(imgCols*scale))
}

// write sends a PNG to w as kitty graphics escape sequences, transmitted and
// displayed at the cursor. cols > 0 scales it to that many columns; the
// terminal keeps the aspect ratio.
func write(w io.Writer, data []byte, cols int) error {
	enc := base64.StdEncoding.EncodeToString(data)
	size := ""
	if cols > 0 {
		size = fmt.Sprintf(",c=%d", cols)
	}
	var b bytes.Buffer
	for first := true; first || len(enc) > 0; first = false {
		chunk := enc[:min(chunkSize, len(enc))]
		enc = enc[len(chunk):]
		more := 0
		if len(enc) > 0 {
			more = 1
		}
		if first {
			// q=2 stops the terminal from replying, which would end up as
			// stray input in the shell.
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2%s,m=%d;%s\x1b\\", size, more, chunk)
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	b.WriteString("\n")
	_, err := w.Write(b.Bytes())
	return err
}
