package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// savePath says where a save goes: an absolute name is used as typed, a relative
// one goes in dir (with a leading ~ meaning the home folder), and with no dir it
// stays relative to the folder Codie was launched from.
func savePath(dir, name string) string {
	if dir == "" || filepath.IsAbs(name) {
		return name
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	return filepath.Join(dir, name)
}

// saveFile writes text to a new file. It refuses to overwrite an existing one.
func saveFile(name, text string) error {
	if dir := filepath.Dir(name); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
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
