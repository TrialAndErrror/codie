/*
Copyright © 2026 TrialAndErrror <coding@trialanderrror.com>
*/
package cmd

import (
	"os"
	"strings"
	"time"

	"codie/backend"
	"codie/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var (
	demo      bool
	modelFlag string
)

// loadConfig reads the environment, letting --model override CODIE_MODEL.
func loadConfig() backend.Config {
	cfg := backend.ConfigFromEnv(os.Getenv)
	if modelFlag != "" {
		cfg.Model = modelFlag
	}
	return cfg
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "codie",
	Short: "A coding assistant, not a coding agent.",
	Long: `Codie is a tool for researching best practices and how to
implement features.

With no settings it runs in demo mode (sample answers). To use a real model,
set OPENROUTER_API_KEY and CODIE_MODEL. Run "codie check" to see what Codie
will do before you ask anything.`,
	SilenceUsage: true, // a config error isn't a usage error
	RunE: func(cmd *cobra.Command, args []string) error {
		rt, err := resolveRuntime(loadConfig(), demo, cmd.Flags().Changed("demo"), tui.DefaultStorePath, time.Now())
		if err != nil {
			return err
		}
		m := tui.New(rt.Researcher, rt.Store).WithLabel(rt.Label).WithSaveDir(strings.TrimSpace(os.Getenv(tui.EnvSaveDir)))
		_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
		return err
	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolVar(&demo, "demo", false, "force demo mode: sample answers, in-memory history, nothing sent anywhere")
	rootCmd.PersistentFlags().StringVar(&modelFlag, "model", "", "OpenRouter model ID (overrides "+backend.EnvModel+")")
}
