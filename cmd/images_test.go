package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPromptInput(t *testing.T) {
	dir := t.TempDir()
	pngAsJPG := filepath.Join(dir, "really-a-png.jpg")
	heic := filepath.Join(dir, "photo.HEIC")
	text := filepath.Join(dir, "notes.txt")
	for path, data := range map[string][]byte{pngAsJPG: testPNG(t, color.Black), heic: []byte("not sniffable"), text: []byte("hello")} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	in, err := promptInput("put them together", []string{"../testdata/man.jpg", pngAsJPG, heic}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(in)
	var got []map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("input isn't a content list: %s", b)
	}
	man, _ := os.ReadFile("../testdata/man.jpg")
	if len(got) != 4 || got[0]["type"] != "text" || got[0]["text"] != "put them together" {
		t.Fatalf("want the prompt then 3 images, got %s", b)
	}
	if got[1]["type"] != "image" || got[1]["mime_type"] != "image/jpeg" || got[1]["data"] != base64.StdEncoding.EncodeToString(man) {
		t.Errorf("man.jpg sent as %s %s", got[1]["type"], got[1]["mime_type"])
	}
	if got[2]["mime_type"] != "image/png" {
		t.Errorf("PNG named .jpg sent as %s, want image/png", got[2]["mime_type"])
	}
	if got[3]["mime_type"] != "image/heic" {
		t.Errorf(".HEIC sent as %s, want image/heic", got[3]["mime_type"])
	}

	if in, err := promptInput("just text", nil, nil); err != nil {
		t.Error(err)
	} else if b, _ := json.Marshal(in); string(b) != `"just text"` {
		t.Errorf("no images: input = %s, want a plain string", b)
	}

	var ue usageError
	if _, err := promptInput("p", []string{text}, nil); !errors.As(err, &ue) {
		t.Errorf("text file: err = %v, want a usage error", err)
	}
	if _, err := promptInput("p", slices.Repeat([]string{"../testdata/man.jpg"}, maxImages+1), nil); !errors.As(err, &ue) {
		t.Errorf("%d images: err = %v, want a usage error", maxImages+1, err)
	}
	if _, err := promptInput("p", []string{filepath.Join(dir, "missing.png")}, nil); err == nil || errors.As(err, &ue) {
		t.Errorf("missing file: err = %v, want a plain error", err)
	}

	// The camera photo comes last, and the camera is only used once the
	// files are fine.
	boy, _ := os.ReadFile("../testdata/boy.jpg")
	called := false
	camera := func() ([]byte, error) { called = true; return boy, nil }
	in, err = promptInput("p", []string{"../testdata/man.jpg"}, camera)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(in)
	got = nil
	_ = json.Unmarshal(b, &got)
	if len(got) != 3 || got[2]["data"] != base64.StdEncoding.EncodeToString(boy) || got[2]["mime_type"] != "image/jpeg" {
		t.Errorf("camera photo not sent last as JPEG: %.200s", b)
	}
	called = false
	if _, err := promptInput("p", []string{text}, camera); err == nil || called {
		t.Errorf("bad file: err = %v, camera used = %v", err, called)
	}
	if _, err := promptInput("p", slices.Repeat([]string{"../testdata/man.jpg"}, maxImages), camera); !errors.As(err, &ue) || called {
		t.Errorf("%d files + camera: err = %v, camera used = %v", maxImages, err, called)
	}
	if in, err := promptInput("p", nil, camera); err != nil || !called {
		t.Errorf("camera only: err = %v, camera used = %v", err, called)
	} else if b, _ := json.Marshal(in); !strings.Contains(string(b), `"type":"image"`) {
		t.Errorf("camera only: %.200s", b)
	}
}
