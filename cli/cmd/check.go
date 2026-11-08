package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"codie/backend"

	"github.com/spf13/cobra"
)

var checkLive bool

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Show how Codie is configured, and optionally test the connection",
	Long: `Show which backend Codie would use and whether your settings are complete.

By default this only reads your environment: it makes no network requests and
spends nothing. Add --live to send one tiny test request to OpenRouter (a few
tokens, a fraction of a cent) and confirm your key and model really work.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	// runCheck already explains every failure in its own output; don't let
	// cobra print the same message a second time.
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCheck(cmd.Context(), cmd.OutOrStdout(), loadConfig(), checkLive)
	},
}

func init() {
	checkCmd.Flags().BoolVar(&checkLive, "live", false, "send one tiny real request to OpenRouter to test the key and model")
	rootCmd.AddCommand(checkCmd)
}

// runCheck prints the configuration and, with live, makes one real request.
// It never prints the API key, only whether it is set and its last four
// characters.
func runCheck(ctx context.Context, w io.Writer, cfg backend.Config, live bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fmt.Fprintln(w, "Codie configuration")
	fmt.Fprintf(w, "  %-20s %s\n", backend.EnvAPIKey, cfg.MaskedKey())
	fmt.Fprintf(w, "  %-20s %s\n", backend.EnvModel, orNotSet(cfg.Model))
	fmt.Fprintf(w, "  %-20s %s\n", backend.EnvBaseURL, cfg.BaseURL)
	fmt.Fprintln(w)

	if cfg.APIKey == "" {
		fmt.Fprintln(w, "Mode: demo. Answers are samples and nothing is sent anywhere.")
		fmt.Fprintf(w, "To use a real model, set %s and %s.\n", backend.EnvAPIKey, backend.EnvModel)
		if live {
			err := fmt.Errorf("--live needs a real configuration: %s is not set", backend.EnvAPIKey)
			fmt.Fprintf(w, "Problem: %v\n", err)
			return err
		}
		return nil
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(w, "Problem: %v\n", err)
		return err
	}
	fmt.Fprintln(w, "Mode: real. Your questions will be sent to OpenRouter and on to the model's provider.")
	fmt.Fprintln(w, "Configuration looks complete.")
	if !live {
		fmt.Fprintln(w, "\nNo request was made. Run `codie check --live` to test the connection.")
		return nil
	}

	o, err := backend.NewOpenRouter(cfg)
	if err != nil {
		return err
	}
	o.System = "Reply with exactly one word."
	o.MaxTokens = 16

	fmt.Fprintf(w, "\nSending one tiny test request to %s using model %s…\n", cfg.BaseURL, cfg.Model)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	start := time.Now()
	reply, err := backend.Collect(o.Research(ctx, "Say: ok"))
	if err != nil {
		fmt.Fprintf(w, "Failed: %v\n", err)
		return err
	}
	fmt.Fprintf(w, "OK. A streamed reply arrived in %s: %q\n", time.Since(start).Round(time.Millisecond), snippet(reply, 80))
	return nil
}

func orNotSet(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}

func snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
