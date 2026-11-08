package tui

import (
	"fmt"
	"os"

	"github.com/atotto/clipboard"
	"github.com/muesli/termenv"
)

// copyToClipboard uses the system clipboard (xclip/xsel/wl-copy/pbcopy) and,
// if that isn't available, falls back to the OSC 52 terminal escape, which
// most modern terminals honour and which works over SSH.
func copyToClipboard(s string) error {
	if err := clipboard.WriteAll(s); err == nil {
		return nil
	}
	termenv.NewOutput(os.Stdout).Copy(s)
	return nil
}

// saveFile writes text to a new file. It refuses to overwrite an existing one.
func saveFile(name, text string) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s: %w", name, errExists)
		}
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
