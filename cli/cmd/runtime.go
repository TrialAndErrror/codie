package cmd

import (
	"os"
	"time"

	"codie/backend"
	"codie/tui"
)

// runtime is what the TUI is wired to: who answers, where history lives, and a
// label telling the user which of those they're getting.
type runtime struct {
	Researcher backend.Researcher
	Store      tui.Store
	Label      string
}

// resolveRuntime decides between sample ("demo") mode and the real OpenRouter
// backend. The rules, in order:
//
//  1. --demo           -> demo, even if a key is set
//  2. --demo=false     -> real; error if the key or model is missing
//  3. no API key set   -> demo (so a first run just works)
//  4. a key is set     -> real; error if the model is missing
//
// A set key never silently falls back to demo: if the config is wrong the user
// is told, not shown fake answers they might mistake for real ones.
func resolveRuntime(cfg backend.Config, demo, demoSet bool, storePath func() (string, error), now time.Time) (runtime, error) {
	switch {
	case demoSet && demo:
		return demoRuntime(now, "demo · sample answers, nothing is sent anywhere"), nil
	case !demoSet && cfg.APIKey == "":
		return demoRuntime(now, "demo · sample answers (set "+backend.EnvAPIKey+" and "+backend.EnvModel+" for real ones)"), nil
	}
	return realRuntime(cfg, storePath)
}

func demoRuntime(now time.Time, label string) runtime {
	return runtime{
		// In-memory and pre-seeded: nothing is written to disk.
		Researcher: &tui.FakeResearcher{Delay: 700 * time.Millisecond, ChunkDelay: 20 * time.Millisecond},
		Store:      tui.NewMemStore(tui.DemoEntries(now)),
		Label:      label,
	}
}

func realRuntime(cfg backend.Config, storePath func() (string, error)) (runtime, error) {
	o, err := backend.NewOpenRouter(cfg) // validates the config
	if err != nil {
		return runtime{}, err
	}
	path, err := storePath()
	if err != nil {
		return runtime{}, err
	}
	// The model may read files in the folder Codie was started in, and its
	// children, but only after the user approves (once per question).
	if wd, err := os.Getwd(); err == nil {
		if ft, err := backend.NewFileTools(wd); err == nil {
			o.Files = ft
		}
	}
	return runtime{
		Researcher: o,
		Store:      tui.JSONStore{Path: path},
		Label:      "OpenRouter · " + cfg.Model,
	}, nil
}
