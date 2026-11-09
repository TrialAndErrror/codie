package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one question and the answer generated for it at the time.
type Entry struct {
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
	At       time.Time `json:"at"`
}

// Store persists history across runs.
type Store interface {
	Load() ([]Entry, error)
	Append(Entry) error
}

// JSONStore keeps history in a single JSON file. Fine for a prototype; swap in
// SQLite later if history gets large or you want server-side search.
type JSONStore struct {
	Path string
}

// DefaultStorePath is $XDG_CONFIG_HOME/codie/history.json (or the OS equivalent).
func DefaultStorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "codie", "history.json"), nil
}

func (s JSONStore) Load() ([]Entry, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path, err)
	}
	return entries, nil
}

// Append adds an entry, writing via a temp file + rename so a crash mid-write
// can't corrupt the existing history.
func (s JSONStore) Append(e Entry) error {
	entries, err := s.Load()
	if err != nil {
		return err
	}
	entries = append(entries, e)

	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// memStore is an in-memory Store for tests and for running without persistence.
type memStore struct{ entries []Entry }

func (m *memStore) Load() ([]Entry, error) { return m.entries, nil }
func (m *memStore) Append(e Entry) error {
	m.entries = append(m.entries, e)
	return nil
}

// EnvSaveDir names the environment variable that sets the default save folder.
const EnvSaveDir = "CODIE_SAVE_DIR"
