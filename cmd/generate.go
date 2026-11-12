package cmd

import (
	"bytes"
	"errors"
	"fmt"
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
	f.Bool("preview", true, "show the image in the terminal if it supports the kitty graphics protocol (env GEMINI_PREVIEW)")
	_ = viper.BindPFlag("preview", f.Lookup("preview"))
	f.Bool("thoughts", false, "print the model's thought summaries and preview its interim images (env GEMINI_THOUGHTS)")
	_ = viper.BindPFlag("thoughts", f.Lookup("thoughts"))
	f.String("search", "", "ground the image in Google Search: web, image or web,image (env GEMINI_SEARCH)")
	_ = viper.BindPFlag("search", f.Lookup("search"))
	f.String("session", "", "continue the named multi-turn editing session, or start it (state in $XDG_STATE_HOME/gemini-image/sessions)")
	f.StringArrayP("image", "i", nil, fmt.Sprintf("reference image `file` to include with the prompt; repeat for more (at most %d)", maxImages))
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

func runGenerate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	format, err := imageFormat(viper.GetString("aspect-ratio"), viper.GetString("size"))
	if err != nil {
		return usageError{err}
	}
	images, _ := cmd.Flags().GetStringArray("image")
	input, err := promptInput(args[0], images)
	if err != nil {
		return err
	}
	tools, err := searchTools(viper.GetString("search"))
	if err != nil {
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
	model, err := setting(cmd, "model")
	if err != nil {
		return fmt.Errorf("resolving model: %w", err)
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
	data, mime, err := streamImage(ctx, client, req, p)
	p.status.Stop()
	if err != nil {
		p.printGrounding()
		return fmt.Errorf("generating image: %w", err)
	}

	path, _ := cmd.Flags().GetString("output")
	if path == "" {
		ext := "png"
		if mime != "" {
			ext = strings.TrimPrefix(mime, "image/")
		}
		path = fmt.Sprintf("image-%s.%s", time.Now().Format("20060102-150405"), ext)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing image: %w", err)
	}
	if sess != nil {
		// Warn rather than fail: the image is already saved.
		err := errors.New("the API sent no interaction ID")
		if p.interactionID != "" {
			// Abs only fails if the working directory is gone.
			abs, _ := filepath.Abs(path)
			var refs []string
			for _, img := range images {
				ref, _ := filepath.Abs(img)
				refs = append(refs, ref)
			}
			sess.Interactions = append(sess.Interactions, interaction{
				ID:     p.interactionID,
				Prompt: args[0],
				Images: refs,
				File:   abs,
				Parameters: genParams{
					Model:       model,
					AspectRatio: viper.GetString("aspect-ratio"),
					Size:        viper.GetString("size"),
					Search:      viper.GetString("search"),
					Thoughts:    p.thoughts,
				},
				Flags: givenFlags(cmd.Flags()),
			})
			err = saveSession(sessionPath, sess)
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
