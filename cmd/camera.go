package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// defaultCamera is the --camera value when no device is given.
const defaultCamera = "default"

// Landscape sizes to ask Mac cameras for, best first. Without a size ffmpeg
// takes the camera's first mode, which can be portrait (1080x1920 on recent
// MacBooks). "" lets the camera choose, for cameras with neither size.
var cameraSizes = []string{"1920x1080", "1280x720", ""}

// defaultDevice is the device --camera uses without a value: the first video
// device, normally the built-in camera.
func defaultDevice(goos string) string {
	switch goos {
	case "darwin":
		return "0"
	case "linux":
		return "/dev/video0"
	}
	return ""
}

// cameraArgs returns the ffmpeg arguments that grab one JPEG frame of the
// given size (WxH, or "" for the camera's default) from device and write it to
// stdout. The size only applies on macOS.
func cameraArgs(goos, device, size string) ([]string, error) {
	if device == defaultCamera {
		device = defaultDevice(goos)
	}
	var input []string
	switch goos {
	case "darwin":
		// Most Mac cameras reject ffmpeg's default 29.97 fps.
		input = []string{"-f", "avfoundation", "-framerate", "30"}
		if size != "" {
			input = append(input, "-video_size", size)
		}
		input = append(input, "-i", device)
	case "linux":
		input = []string{"-f", "v4l2", "-i", device}
	default:
		return nil, fmt.Errorf("--camera isn't supported on %s", goos)
	}
	args := append([]string{"-hide_banner", "-loglevel", "error"}, input...)
	// Skip the first second: cameras start dark until auto exposure settles.
	return append(args, "-ss", "1", "-frames:v", "1", "-f", "image2pipe", "-c:v", "mjpeg", "-q:v", "2", "-"), nil
}

// cameraSnapshot takes a landscape photo with the camera, using ffmpeg. It
// tries the sizes in cameraSizes until the camera accepts one.
func cameraSnapshot(ctx context.Context, device string) ([]byte, error) {
	if _, err := cameraArgs(runtime.GOOS, device, ""); err != nil {
		return nil, usageError{err}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("--camera needs ffmpeg; install it, e.g. with `brew install ffmpeg`")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, size := range cameraSizes {
		args, _ := cameraArgs(runtime.GOOS, device, size)
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err != nil && size != "" && strings.Contains(stderr.String(), "is not supported by the device") {
			continue // try the next size
		}
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				err = fmt.Errorf("%w: %s", err, msg)
			}
			return nil, fmt.Errorf("taking camera snapshot: %w", err)
		}
		if stdout.Len() == 0 {
			return nil, errors.New("taking camera snapshot: ffmpeg returned no image")
		}
		return stdout.Bytes(), nil
	}
	return nil, errors.New("taking camera snapshot: the camera supports none of the tried sizes")
}

var camerasCmd = &cobra.Command{
	Use:   "cameras",
	Short: "List the cameras --camera can use",
	Long: `List the cameras --camera can use and mark the default. Pass the index or
name (macOS) or the device file (Linux) to --camera, e.g. --camera=1.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.NoArgs(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, _ []string) error {
		cams, err := listCameras(cmd.Context(), runtime.GOOS, "/sys/class/video4linux")
		if err != nil {
			return err
		}
		if len(cams) == 0 {
			return errors.New("no cameras found")
		}
		out := cmd.OutOrStdout()
		for _, c := range cams {
			line := c.id + "  " + c.name
			if c.id == defaultDevice(runtime.GOOS) {
				line += paint(useColor(out), sgrDim, " (default)")
			}
			fmt.Fprintln(out, line)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(camerasCmd) }

// cameraInfo is a camera as --camera knows it: id is the value to pass.
type cameraInfo struct{ id, name string }

// listCameras returns the cameras in device order: on macOS what ffmpeg's
// AVFoundation input reports, on Linux the video4linux devices under sysfs.
func listCameras(ctx context.Context, goos, sysfs string) ([]cameraInfo, error) {
	switch goos {
	case "darwin":
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return nil, errors.New("listing cameras needs ffmpeg; install it, e.g. with `brew install ffmpeg`")
		}
		// ffmpeg prints the devices and then fails for want of an input;
		// the exit status says nothing, so only the output is used.
		out, _ := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "").CombinedOutput()
		return parseAVFoundation(string(out)), nil
	case "linux":
		return listV4L2(sysfs)
	default:
		return nil, fmt.Errorf("cameras aren't supported on %s", goos)
	}
}

var avfDevice = regexp.MustCompile(`\] \[(\d+)\] (.+)$`)

// parseAVFoundation picks the cameras out of ffmpeg's AVFoundation device
// list. Screens ("Capture screen N") and audio devices are left out.
func parseAVFoundation(out string) []cameraInfo {
	var cams []cameraInfo
	video := false
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "AVFoundation video devices:"):
			video = true
		case strings.Contains(line, "AVFoundation audio devices:"):
			video = false
		case video:
			if m := avfDevice.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[2], "Capture screen") {
				cams = append(cams, cameraInfo{id: m[1], name: m[2]})
			}
		}
	}
	return cams
}

// listV4L2 lists the video4linux devices described under sysfs
// (/sys/class/video4linux), in device order, leaving out metadata nodes.
func listV4L2(sysfs string) ([]cameraInfo, error) {
	dirs, err := filepath.Glob(filepath.Join(sysfs, "video*"))
	if err != nil {
		return nil, err
	}
	num := func(dir string) int { n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(dir), "video")); return n }
	slices.SortFunc(dirs, func(a, b string) int { return num(a) - num(b) }) // video10 after video2
	var cams []cameraInfo
	for _, dir := range dirs {
		// Cameras also have a metadata node (index 1) that can't take photos.
		// shortcut: assumes the capture node is index 0; check the device's
		// capabilities (VIDIOC_QUERYCAP) if a camera is ever left out.
		if index, err := os.ReadFile(filepath.Join(dir, "index")); err == nil && strings.TrimSpace(string(index)) != "0" {
			continue
		}
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		cams = append(cams, cameraInfo{id: "/dev/" + filepath.Base(dir), name: strings.TrimSpace(string(name))})
	}
	return cams, nil
}
