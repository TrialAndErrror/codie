/*
Copyright © 2026 TrialAndErrror <coding@trialanderrror.com>
*/
package cmd

import (
	"os"
	"time"

	"codie/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var demo bool

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "codie",
	Short: "A coding assistant, not a coding agent.",
	Long: `Codie is a tool for researching best practices and how to
implement features.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var store tui.Store
		if demo {
			// In-memory and pre-seeded: nothing is written to disk.
			store = tui.NewMemStore(tui.DemoEntries(time.Now()))
		} else {
			path, err := tui.DefaultStorePath()
			if err != nil {
				return err
			}
			store = tui.JSONStore{Path: path}
		}
		m := tui.New(&tui.FakeResearcher{Delay: 2 * time.Second}, store)
		_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
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
	// Default on while the backend is fake. Flip when a real Researcher is wired in.
	rootCmd.Flags().BoolVar(&demo, "demo", true, "use fake responses and sample in-memory history (--demo=false persists to ~/.config/codie)")
}
