package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ehedenst/gemini-image/internal/kitty"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

var generateCmd = &cobra.Command{
	Use:   "generate <prompt>",
	Short: "Generate an image from a text prompt",
	Example: `  gemini-image generate "A watercolor fox in a snowy forest"
  gemini-image generate -o fox.png "A watercolor fox in a snowy forest"
  gemini-image generate --aspect-ratio 16:9 --size 2K "A mountain panorama at dawn"`,
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

	res, err := client.Interactions.Create(ctx, operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(interactions.CreateModelInteraction{
			Model:          interactions.Model(model),
			Input:          genai.Ptr(interactions.NewInteractionsInput(args[0])),
			ResponseFormat: format,
		}),
	})
	if err != nil {
		return fmt.Errorf("generating image: %w", err)
	}

	it := res.Interaction
	if it == nil || it.OutputImage == nil || it.OutputImage.Data == nil {
		msg := "no image in response"
		if it != nil {
			msg += fmt.Sprintf(" (status %s)", it.Status)
			if it.OutputText != nil {
				msg += ": " + *it.OutputText
			}
		}
		return errors.New(msg)
	}

	data, err := base64.StdEncoding.DecodeString(*it.OutputImage.Data)
	if err != nil {
		return fmt.Errorf("decoding image: %w", err)
	}

	path, _ := cmd.Flags().GetString("output")
	if path == "" {
		ext := "png"
		if mt := it.OutputImage.MimeType; mt != nil {
			ext = strings.TrimPrefix(string(*mt), "image/")
		}
		path = fmt.Sprintf("image-%s.%s", time.Now().Format("20060102-150405"), ext)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing image: %w", err)
	}

	// Only preview when stdout is the real terminal, never when piped or captured.
	if out, ok := cmd.OutOrStdout().(*os.File); ok && viper.GetBool("preview") {
		if err := kitty.Preview(out, data); err != nil && !errors.Is(err, errors.ErrUnsupported) {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: preview failed:", err)
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), path)
	return nil
}
