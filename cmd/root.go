package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// version is set at build time:
//
//	go build -ldflags "-X github.com/ehedenst/gemini-image/cmd.version=v1.0.0"
var version = "dev"

// buildVersion returns version or, when it wasn't set, the module version Go
// records in the binary (go install, or go build in a git checkout).
func buildVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

const defaultModel = "gemini-nano-banana-2.1"

var envFile string

var rootCmd = &cobra.Command{
	Use:   "gemini-image",
	Short: "Generate images with the Google Gemini API",
	Long: `Generate images with the Google Gemini API.

Settings are read from flags, then environment variables (GEMINI_API_KEY,
GEMINI_MODEL, GEMINI_ASPECT_RATIO, GEMINI_SIZE, GEMINI_PREVIEW, GEMINI_THOUGHTS,
GEMINI_SEARCH). Environment variables can also be set in a .env file; variables
already set in the environment take precedence over the file.

If GEMINI_API_KEY starts with "!", the rest is run as a shell command and
replaced by the command's output, so the key can stay in a password manager:

  GEMINI_API_KEY="!op read 'op://Vault/Gemini API Key/credential'"`,
	Version:       buildVersion(),
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		return loadEnvFile(envFile, cmd.Flags().Changed("env-file"))
	},
}

// Execute runs the CLI with signal-aware cancellation, prints any error to
// stderr and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := rootCmd.ExecuteContext(ctx)
	errOut := rootCmd.ErrOrStderr()
	if err != nil && ctx.Err() != nil {
		// Whatever failed after Ctrl-C failed because of it; child processes
		// (ffmpeg, "!command") get the signal too and fail with their own error
		// instead of context.Canceled. The user asked to stop, so it's not
		// reported as an error.
		fmt.Fprintln(errOut, paint(useColor(errOut), sgrDim, "interrupted"))
		return 130
	}
	if err != nil {
		fmt.Fprintln(errOut, paint(useColor(errOut), sgrRed, "error:"), err)
	}
	return exitCode(err)
}

// usageError marks errors caused by invalid flags, arguments or settings.
type usageError struct{ error }

func (e usageError) Unwrap() error { return e.error }

// exitCode maps an error to a Unix exit code: 2 for usage errors, 130 when
// interrupted, 1 for anything else.
func exitCode(err error) int {
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled):
		return 130
	case errors.As(err, &ue):
		return 2
	default:
		return 1
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&envFile, "env-file", ".env", "file to load environment variables from")
	pf.String("api-key", "", "Gemini API key (env GEMINI_API_KEY)")
	pf.String("model", defaultModel, "model to use (env GEMINI_MODEL)")
	pf.Bool("preview", true, "show images in the terminal if it supports the kitty graphics protocol (env GEMINI_PREVIEW)")
	_ = viper.BindPFlags(pf)
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })

	viper.SetEnvPrefix("GEMINI")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
}
