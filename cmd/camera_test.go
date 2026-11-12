package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCameraArgs(t *testing.T) {
	for _, c := range []struct{ goos, device, size, want string }{
		{"darwin", defaultCamera, "1920x1080", "-f avfoundation -framerate 30 -video_size 1920x1080 -i 0 "},
		{"darwin", "MacBook Pro Camera", "", "-framerate 30 -i MacBook Pro Camera "},
		{"linux", defaultCamera, "1920x1080", "-f v4l2 -i /dev/video0 "},
		{"linux", "/dev/video2", "", "-i /dev/video2 "},
	} {
		args, err := cameraArgs(c.goos, c.device, c.size)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, c.want) || !strings.HasSuffix(joined, "-frames:v 1 -f image2pipe -c:v mjpeg -q:v 2 -") {
			t.Errorf("%s %q: %s", c.goos, c.device, joined)
		}
	}
	if _, err := cameraArgs("windows", defaultCamera, ""); err == nil {
		t.Error("windows: no error")
	}
}

func TestParseAVFoundation(t *testing.T) {
	out := `[AVFoundation indev @ 0x7a1d014140] AVFoundation video devices:
[AVFoundation indev @ 0x7a1d014140] [0] MacBook Pro Camera
[AVFoundation indev @ 0x7a1d014140] [1] iPhone Camera
[AVFoundation indev @ 0x7a1d014140] [2] Capture screen 0
[AVFoundation indev @ 0x7a1d014140] AVFoundation audio devices:
[AVFoundation indev @ 0x7a1d014140] [0] MacBook Pro Microphone
[in#0 @ 0x7a1d014000] Error opening input: Input/output error
`
	want := []cameraInfo{{"0", "MacBook Pro Camera"}, {"1", "iPhone Camera"}}
	if got := parseAVFoundation(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListV4L2(t *testing.T) {
	sysfs := t.TempDir()
	for dev, name := range map[string]string{"video0": "Integrated Camera\n", "video2": "USB Webcam\n", "video10": "Loopback\n", "video1": ""} {
		dir := filepath.Join(sysfs, dev)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if name != "" { // video1 has no name file and is skipped
			if err := os.WriteFile(filepath.Join(dir, "name"), []byte(name), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := listV4L2(sysfs)
	want := []cameraInfo{{"/dev/video0", "Integrated Camera"}, {"/dev/video2", "USB Webcam"}, {"/dev/video10", "Loopback"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, %v; want %v", got, err, want)
	}
}
