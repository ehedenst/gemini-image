package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// version is set at build time:
//
//	go build -ldflags "-X github.com/ehedenst/gemini-image/cmd.version=v1.0.0"
var version = "dev"

const defaultModel = "gemini-nano-banana-2.1"

var envFile string

var rootCmd = &cobra.Command{
	Use:   "gemini-image",
	Short: "Generate images with the Google Gemini API",
	Long: `Generate images with the Google Gemini API.

Settings are read from flags, then environment variables (GEMINI_API_KEY,
GEMINI_MODEL, GEMINI_ASPECT_RATIO, GEMINI_SIZE). Environment variables can also
be set in a .env file; variables already set in the environment take
precedence over the file.

An environment variable value starting with "!" is run as a shell command and
replaced by the command's output, e.g.:

  GEMINI_API_KEY="!op read 'op://Vault/API Key/credential'"`,
	Version:       version,
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
	if err != nil {
		fmt.Fprintln(rootCmd.ErrOrStderr(), paint(useColor(rootCmd.ErrOrStderr()), sgrRed, "error:"), err)
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
	_ = viper.BindPFlags(pf)
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })

	viper.SetEnvPrefix("GEMINI")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
}
