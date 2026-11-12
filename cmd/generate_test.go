package cmd

import "testing"

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
