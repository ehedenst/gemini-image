package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
)

var generateCmd = &cobra.Command{
	Use:   "generate <prompt>",
	Short: "Generate an image from a text prompt",
	Example: `  gemini-image generate "A watercolor fox in a snowy forest"
  gemini-image generate -o fox.png "A watercolor fox in a snowy forest"
  gemini-image generate --aspect-ratio 16:9 --size 2K "A mountain panorama at dawn"
  gemini-image generate -i fox.png -i hat.jpg "Put the hat on the fox"
  gemini-image generate --camera "Turn me into a watercolor painting"
  gemini-image generate --session fox "A watercolor fox in a snowy forest"
  gemini-image generate --session fox "Make the fox wear a red scarf"
  gemini-image generate --session fox --step 1 "Make the fox wear a blue hat instead"`,
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	},
	RunE: runGenerate,
}

func init() {
	f := generateCmd.Flags()
	f.StringP("output", "o", "", "output file (default image-<timestamp>.<ext>)")
	f.String("aspect-ratio", "", "aspect ratio: "+strings.Join(aspectRatios, ", ")+" (env GEMINI_ASPECT_RATIO, default model's choice)")
	f.String("size", "", "image size: "+strings.Join(imageSizes, ", ")+" (env GEMINI_SIZE, default model's choice)")
	f.Bool("thoughts", false, "print the model's thought summaries and preview its interim images (env GEMINI_THOUGHTS)")
	_ = viper.BindPFlag("thoughts", f.Lookup("thoughts"))
	f.String("search", "", "ground the image in Google Search: web, image or web,image (env GEMINI_SEARCH)")
	_ = viper.BindPFlag("search", f.Lookup("search"))
	f.String("session", "", "continue the named multi-turn editing session, or start it (state in $XDG_STATE_HOME/gemini-image/sessions)")
	f.StringArrayP("image", "i", nil, fmt.Sprintf("reference image `file` to include with the prompt; repeat for more (at most %d)", maxImages))
	f.String("camera", "", "take a photo with the camera and add it as a reference image; --camera=`device` picks a camera (macOS: name or index, Linux: /dev/videoN)")
	f.Lookup("camera").NoOptDefVal = defaultCamera
	f.Int("step", 0, "with --session, continue from this step (1 = first) instead of the last one")
	f.String("debug-events", "", "write the request and every raw stream event to `file` as JSON lines")
	_ = f.MarkHidden("debug-events")
	_ = generateCmd.RegisterFlagCompletionFunc("search", cobra.FixedCompletions([]string{"web", "image", "web,image"}, cobra.ShellCompDirectiveNoFileComp))
	_ = viper.BindPFlag("aspect-ratio", f.Lookup("aspect-ratio"))
	_ = viper.BindPFlag("size", f.Lookup("size"))
	_ = generateCmd.RegisterFlagCompletionFunc("aspect-ratio", cobra.FixedCompletions(aspectRatios, cobra.ShellCompDirectiveNoFileComp))
	_ = generateCmd.RegisterFlagCompletionFunc("size", cobra.FixedCompletions(imageSizes, cobra.ShellCompDirectiveNoFileComp))
	rootCmd.AddCommand(generateCmd)
}

// Values accepted by the API, see interactions.ImageResponseFormat.
var (
	aspectRatios = []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9", "1:4", "4:1", "1:8", "8:1"}
	imageSizes   = []string{"512", "1K", "2K", "4K"}
)

// imageFormat builds the response format from the --aspect-ratio and --size
// flags. It returns nil when neither is set so the model defaults apply.
func imageFormat(aspectRatio, size string) (*interactions.CreateModelInteractionResponseFormat, error) {
	if aspectRatio == "" && size == "" {
		return nil, nil
	}
	var f interactions.ImageResponseFormat
	if aspectRatio != "" {
		if !slices.Contains(aspectRatios, aspectRatio) {
			return nil, fmt.Errorf("invalid aspect ratio %q, valid: %s", aspectRatio, strings.Join(aspectRatios, ", "))
		}
		f.AspectRatio = interactions.ImageResponseFormatAspectRatio(aspectRatio).ToPointer()
	}
	if size != "" {
		size = strings.ToUpper(size) // accept 2k as well as 2K
		if !slices.Contains(imageSizes, size) {
			return nil, fmt.Errorf("invalid size %q, valid: %s", size, strings.Join(imageSizes, ", "))
		}
		f.ImageSize = interactions.ImageResponseFormatImageSize(size).ToPointer()
	}
	rf := interactions.NewCreateModelInteractionResponseFormat(interactions.NewResponseFormat(f))
	return &rf, nil
}

// checkOutput catches an --output that can't be written before anything is
// generated: a directory, or a file in a directory that doesn't exist.
func checkOutput(path string) error {
	if path == "" {
		return nil
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return fmt.Errorf("--output: %s is a directory", path)
	}
	dir := filepath.Dir(path)
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--output: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("--output: %s is not a directory", dir)
	}
	return nil
}

// saveImage writes data to path and returns the path. With path "" it writes
// a new image-<timestamp>.<ext> file instead, adding -2, -3, … to the name if
// it's taken, so runs in the same second don't overwrite each other.
func saveImage(path, mimeType string, data []byte) (string, error) {
	if path != "" {
		return path, os.WriteFile(path, data, 0o644)
	}
	name := "image-" + time.Now().Format("20060102-150405")
	ext := "png"
	if t, ok := strings.CutPrefix(mimeType, "image/"); ok {
		ext = t
	}
	for i := 1; ; i++ {
		path = name + "." + ext
		if i > 1 {
			path = fmt.Sprintf("%s-%d.%s", name, i, ext)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return path, err
	}
}

func runGenerate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	format, err := imageFormat(viper.GetString("aspect-ratio"), viper.GetString("size"))
	if err != nil {
		return usageError{err}
	}
	tools, err := searchTools(viper.GetString("search"))
	if err != nil {
		return usageError{err}
	}
	output, _ := cmd.Flags().GetString("output")
	if err := checkOutput(output); err != nil {
		return usageError{err}
	}
	var sessionPath, previousID string
	var sess *session
	name, _ := cmd.Flags().GetString("session")
	if cmd.Flags().Changed("step") && name == "" {
		return usageError{errors.New("--step requires --session")}
	}
	if name != "" {
		if sessionPath, err = sessionFile(name); err != nil {
			return usageError{err}
		}
		if sess, err = loadSession(sessionPath); err != nil {
			return fmt.Errorf("reading session: %w", err)
		}
		previousID = sess.lastID()
		if cmd.Flags().Changed("step") {
			step, _ := cmd.Flags().GetInt("step")
			if previousID, err = sess.stepID(name, step); err != nil {
				return usageError{err}
			}
		}
	}

	apiKey, err := setting(cmd, "api-key")
	if err != nil {
		return fmt.Errorf("resolving API key: %w", err)
	}
	if apiKey == "" {
		return usageError{errors.New("no API key: set --api-key or GEMINI_API_KEY")}
	}
	model := viper.GetString("model")

	// Built after all other checks, so a bad flag doesn't turn the camera on.
	images, _ := cmd.Flags().GetStringArray("image")
	var camera func() ([]byte, error)
	if cmd.Flags().Changed("camera") {
		device, _ := cmd.Flags().GetString("camera")
		camera = func() ([]byte, error) {
			fmt.Fprintln(cmd.ErrOrStderr(), "taking a photo with the camera…")
			photo, err := cameraSnapshot(ctx, device)
			// Show the photo before generating, so it's clear what was sent.
			if out, ok := cmd.OutOrStdout().(*os.File); ok && err == nil && viper.GetBool("preview") {
				if err := kitty.Preview(out, photo, 2); err != nil && !errors.Is(err, errors.ErrUnsupported) {
					fmt.Fprintln(cmd.ErrOrStderr(), paint(useColor(cmd.ErrOrStderr()), sgrYellow, "warning:"), "preview failed:", err)
				}
			}
			return photo, err
		}
	}
	input, err := promptInput(args[0], images, camera)
	if err != nil {
		return err
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey, Backend: genai.BackendGeminiAPI})
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}

	req := interactions.CreateModelInteraction{
		Model:          interactions.Model(model),
		Input:          input,
		ResponseFormat: format,
		Tools:          tools,
	}
	if previousID != "" {
		req.PreviousInteractionID = &previousID
	}
	p := newProgress(cmd)
	if path, _ := cmd.Flags().GetString("debug-events"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("creating debug events file: %w", err)
		}
		defer f.Close()
		p.events = f
	}
	if p.thoughts {
		req.GenerationConfig = &interactions.GenerationConfig{ThinkingSummaries: interactions.ThinkingSummariesAuto.ToPointer()}
	}
	data, mimeType, err := streamImage(ctx, client, req, p)
	p.status.Stop()
	if err != nil {
		p.printGrounding()
		return fmt.Errorf("generating image: %w", err)
	}

	if p.cutShort != nil {
		// With the grounding lines, so it shows below the image.
		msg := fmt.Sprintf("the connection to the API was lost after the image arrived (%v)", p.cutShort)
		if tools != nil {
			msg += "; search suggestions and sources may be missing"
		}
		p.grounding = append(p.grounding, paint(p.color, sgrYellow, "warning:")+" "+msg)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	path, err := saveImage(output, mimeType, data)
	if err != nil {
		p.printGrounding()
		return fmt.Errorf("writing image: %w", err)
	}
	if t := mime.TypeByExtension(filepath.Ext(output)); output != "" && t != "" && t != mimeType {
		fmt.Fprintln(cmd.ErrOrStderr(), paint(p.color, sgrYellow, "warning:"),
			fmt.Sprintf("%s: the image is %s, not %s as the extension suggests", path, mimeType, t))
	}
	if sess != nil {
		// Warn rather than fail: the image is already saved.
		err := errors.New("the API sent no interaction ID")
		if p.cutShort != nil {
			// The API cancels an interaction whose connection drops, so it
			// may not be possible to continue from it.
			err = errors.New("the response was cut short")
		} else if p.interactionID != "" {
			// Abs only fails if the working directory is gone.
			abs, _ := filepath.Abs(path)
			var refs []string
			for _, img := range images {
				ref, _ := filepath.Abs(img)
				refs = append(refs, ref)
			}
			entry := interaction{
				ID:     p.interactionID,
				Prompt: args[0],
				Images: refs,
				File:   abs,
				Parameters: genParams{
					Model:       model,
					AspectRatio: viper.GetString("aspect-ratio"),
					Size:        strings.ToUpper(viper.GetString("size")), // as sent: 2k is sent as 2K
					Search:      viper.GetString("search"),
					Thoughts:    p.thoughts,
				},
				Flags: givenFlags(cmd.Flags()),
			}
			// Re-read the history: another run may have added steps while this
			// one was generating.
			// shortcut: no file lock, so two runs saving at the same instant can
			// still lose a step; lock the file if that ever matters.
			if sess, err = loadSession(sessionPath); err == nil {
				sess.Interactions = append(sess.Interactions, entry)
				err = saveSession(sessionPath, sess)
			}
		}
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), paint(p.color, sgrYellow, "warning:"), "session not updated:", err)
		}
	}

	// The last interim image is usually the final one; don't draw it twice.
	// Leave room below the image for the grounding lines, the path and the
	// shell prompt.
	if p.preview != nil && !bytes.Equal(data, p.shownPreview) {
		if err := kitty.Preview(p.preview, data, len(p.grounding)+2); err != nil && !errors.Is(err, errors.ErrUnsupported) {
			fmt.Fprintln(cmd.ErrOrStderr(), paint(p.color, sgrYellow, "warning:"), "preview failed:", err)
		}
	}
	p.printGrounding()
	fmt.Fprintln(cmd.OutOrStdout(), path)
	return nil
}

// newProgress sets up progress reporting: a status line when stderr is a
// terminal, and previews only when stdout is a real file (never when captured).
func newProgress(cmd *cobra.Command) *progress {
	p := &progress{errOut: cmd.ErrOrStderr(), thoughts: viper.GetBool("thoughts")}
	if out, ok := cmd.OutOrStdout().(*os.File); ok && viper.GetBool("preview") {
		p.preview = out
	}
	if f, ok := cmd.ErrOrStderr().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		p.color = useColor(f)
		p.status = newStatusLine(f, "connecting", p.color)
		p.hyperlinks = true
	}
	return p
}
