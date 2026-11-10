package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limits that keep a single question from reading the whole disk or flooding
// the model's context.
const (
	// MaxToolCalls is how many tool calls one question may make.
	MaxToolCalls = 15

	maxFileBytes   = 64 << 10 // read at most this much of a file per call
	maxReadLines   = 400
	maxListEntries = 200
)

// FileTools lets the model look at files under one folder, read-only. Root is
// the only place it can see: paths that resolve outside it (by "..", an
// absolute path, or a symlink) are refused, as are secrets such as .env files
// and private keys, and the .git folder.
type FileTools struct {
	root string // absolute, symlinks resolved
}

// NewFileTools returns tools confined to dir and its children.
func NewFileTools(dir string) (*FileTools, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", dir)
	}
	return &FileTools{root: real}, nil
}

// Root is the folder the tools are confined to.
func (f *FileTools) Root() string { return f.root }

type toolSpec struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Specs describes the tools to the model.
func (f *FileTools) Specs() []toolSpec {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []toolSpec{
		{Type: "function", Function: toolFunction{
			Name:        "list_dir",
			Description: "List the files and folders in a folder of the user's project. Folders end with /. Use \".\" for the project root.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": str("Folder path relative to the project root.")},
				"required":   []string{"path"},
			},
		}},
		{Type: "function", Function: toolFunction{
			Name:        "read_file",
			Description: "Read a text file from the user's project. Long files are cut off; pass start_line to continue.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       str("File path relative to the project root."),
					"start_line": map[string]any{"type": "integer", "description": "1-based line to start from. Defaults to 1."},
				},
				"required": []string{"path"},
			},
		}},
	}
}

// Run executes one tool call. It never fails outright: a problem comes back as
// text the model can read and recover from. activity is a short, sanitized
// status line for the user ("reading cli/main.go").
func (f *FileTools) Run(name, argsJSON string) (result, activity string) {
	var args struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "error: the arguments were not valid JSON", "reading a file"
	}
	switch name {
	case "list_dir":
		return f.listDir(args.Path), activityFor("listing", args.Path)
	case "read_file":
		return f.readFile(args.Path, args.StartLine), activityFor("reading", args.Path)
	}
	return "error: unknown tool " + Sanitize(name), "using an unknown tool"
}

func activityFor(verb, p string) string {
	return truncate(Sanitize(verb+" "+p), 80)
}

// resolve turns a model-supplied path into an absolute path inside the root,
// or explains why it can't. rel is the path relative to the root. It also
// refuses anything .gitignore excludes.
func (f *FileTools) resolve(p string) (abs, rel string, err error) {
	abs, rel, err = f.resolveBasic(p)
	if err != nil {
		return "", "", err
	}
	if rel != "." && f.ignored([]string{rel})[rel] {
		return "", "", errors.New("that file is excluded by .gitignore, which is not allowed")
	}
	return abs, rel, nil
}

// resolveBasic is resolve without the .gitignore check, which costs a process
// launch; listings do that check once for the whole folder.
func (f *FileTools) resolveBasic(p string) (abs, rel string, err error) {
	if strings.TrimSpace(p) == "" {
		p = "."
	}
	if strings.ContainsRune(p, 0) {
		return "", "", errors.New("invalid path")
	}
	full := p
	if !filepath.IsAbs(p) {
		full = filepath.Join(f.root, p)
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", "", errors.New("no such file or folder")
	}
	rel, err = filepath.Rel(f.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("that path is outside the project folder, which is not allowed")
	}
	if blockedPath(rel) {
		return "", "", errors.New("that file is off limits (it may hold secrets)")
	}
	return real, rel, nil
}

func (f *FileTools) listDir(p string) string {
	abs, rel, err := f.resolve(p)
	if err != nil {
		return "error: " + err.Error()
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "error: not a readable folder"
	}
	var names, rels []string
	for _, e := range entries {
		// Resolve each entry so a symlink pointing outside the root, or at a
		// secret, is hidden rather than listed.
		_, erel, err := f.resolveBasic(filepath.Join(abs, e.Name()))
		if err != nil {
			continue
		}
		name := e.Name()
		if fi, err := os.Stat(filepath.Join(abs, name)); err == nil && fi.IsDir() {
			name += "/"
		}
		names = append(names, name)
		rels = append(rels, erel)
	}
	if skip := f.ignored(rels); len(skip) > 0 {
		kept := names[:0]
		for i, n := range names {
			if !skip[rels[i]] {
				kept = append(kept, n)
			}
		}
		names = kept
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", displayRel(rel))
	for i, n := range names {
		if i == maxListEntries {
			fmt.Fprintf(&b, "… and %d more\n", len(names)-i)
			break
		}
		b.WriteString(n + "\n")
	}
	if len(names) == 0 {
		b.WriteString("(empty)\n")
	}
	return b.String()
}

func (f *FileTools) readFile(p string, start int) string {
	abs, rel, err := f.resolve(p)
	if err != nil {
		return "error: " + err.Error()
	}
	file, err := os.Open(abs)
	if err != nil {
		return "error: could not open the file"
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return "error: not a regular file"
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return "error: could not read the file"
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return "error: this looks like a binary file, not text"
	}
	cut := len(data) > maxFileBytes
	if cut {
		data = data[:maxFileBytes]
	}

	lines := strings.SplitAfter(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return fmt.Sprintf("error: the file has only %d lines", len(lines))
	}
	end := min(start-1+maxReadLines, len(lines))
	var b strings.Builder
	fmt.Fprintf(&b, "%s (lines %d-%d)\n", displayRel(rel), start, end)
	for _, l := range lines[start-1 : end] {
		b.WriteString(l)
	}
	switch {
	case end < len(lines):
		fmt.Fprintf(&b, "\n[cut off: more lines follow, call read_file again with start_line=%d]\n", end+1)
	case cut:
		b.WriteString("\n[cut off: the file is larger than the read limit]\n")
	}
	return b.String()
}

func displayRel(rel string) string {
	if rel == "." {
		return "./"
	}
	return filepath.ToSlash(rel)
}

// blockedPath reports whether any part of rel names something the model must
// not read. It's a safety net for common secrets, not a guarantee: the user's
// approval is the main guard.
func blockedPath(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if blockedName(strings.ToLower(part)) {
			return true
		}
	}
	return false
}

var blockedGlobs = []string{
	"*.pem", "*.key", "*.p12", "*.pfx", "*.kdbx", "*.keystore", "*.jks",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
	"credentials*", "secrets*", ".netrc", ".npmrc", ".pypirc", ".pgpass",
}

func blockedName(n string) bool {
	switch n {
	case ".git", ".ssh", ".aws", ".gnupg", ".env":
		return true
	}
	if strings.HasPrefix(n, ".env.") {
		switch strings.TrimPrefix(n, ".env.") {
		case "example", "sample", "template", "dist":
			return false
		}
		return true
	}
	for _, g := range blockedGlobs {
		if ok, _ := path.Match(g, n); ok {
			return true
		}
	}
	return false
}

// ignored reports which of the given paths (relative to the root) .gitignore
// excludes, by asking git itself so nested files, global ignores and negations
// all behave exactly as they do for the user. Tracked files are not counted as
// ignored, matching git. If git is missing, or the root isn't in a repository,
// there is nothing to honor and nothing is reported ignored.
func (f *FileTools) ignored(rels []string) map[string]bool {
	out := map[string]bool{}
	if len(rels) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", f.root, "check-ignore", "-z", "--stdin")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var in bytes.Buffer
	for _, r := range rels {
		in.WriteString(filepath.ToSlash(r))
		in.WriteByte(0)
	}
	cmd.Stdin = &in
	res, err := cmd.Output() // exit 1 (none ignored) and 128 (no repo) both land here
	if err != nil && len(res) == 0 {
		return out
	}
	for _, p := range strings.Split(string(res), "\x00") {
		if p != "" {
			out[filepath.FromSlash(p)] = true
		}
	}
	return out
}
