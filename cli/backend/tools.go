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
	// MaxToolCalls is how many tool calls one question may make. It is small on
	// purpose: every call is a full round trip to the model, so a long chain of
	// them is minutes of waiting, and the tools below return a lot per call.
	MaxToolCalls = 5

	maxFileBytes   = 32 << 10 // read at most this much of a file per call
	maxReadLines   = 300      // lines of one file per call
	maxMultiLines  = 120      // lines per file when several are read in one call
	maxReadPaths   = 4        // files in one read_file call
	maxListEntries = 200
	maxListDepth   = 3
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
			Description: "List the files and folders in a folder of the user's project, as a tree if depth is more than 1 (use depth 2 or 3 on the project root to see the layout in one call). Folders end with /. Use \".\" for the project root.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  str("Folder path relative to the project root."),
					"depth": map[string]any{"type": "integer", "description": "How many levels to show, 1 to 3. Defaults to 1."},
				},
				"required": []string{"path"},
			},
		}},
		{Type: "function", Function: toolFunction{
			Name:        "read_file",
			Description: "Read text files from the user's project. To save time, name every file you need in one call with paths (up to 4; each is cut off after 120 lines). A single path gives up to 300 lines; pass start_line to continue it.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       str("One file path relative to the project root."),
					"paths":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Several file paths to read in one call."},
					"start_line": map[string]any{"type": "integer", "description": "1-based line to start from (single path only). Defaults to 1."},
				},
			},
		}},
	}
}

// Run executes one tool call. It never fails outright: a problem comes back as
// text the model can read and recover from. activity is a short, sanitized
// status line for the user ("reading cli/main.go").
func (f *FileTools) Run(name, argsJSON string) (result, activity string) {
	var args struct {
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		StartLine int      `json:"start_line"`
		Depth     int      `json:"depth"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "error: the arguments were not valid JSON", "reading a file"
	}
	switch name {
	case "list_dir":
		return f.listDir(args.Path, args.Depth), activityFor("listing", args.Path)
	case "read_file":
		paths := args.Paths
		if args.Path != "" {
			paths = append([]string{args.Path}, paths...)
		}
		if len(paths) == 0 {
			return "error: give a path, or paths for several files", "reading a file"
		}
		if len(paths) == 1 {
			return f.readFile(paths[0], args.StartLine, maxReadLines), activityFor("reading", paths[0])
		}
		paths = paths[:min(len(paths), maxReadPaths)]
		var b strings.Builder
		for i, p := range paths {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(f.readFile(p, 1, maxMultiLines))
		}
		return b.String(), activityFor("reading", strings.Join(paths, ", "))
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

// child is one visible entry of a folder.
type child struct {
	name  string
	rel   string
	isDir bool
}

// children lists a folder's entries the model may see: sorted, with secrets,
// escaping symlinks and .gitignored entries removed.
func (f *FileTools) children(abs string) ([]child, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var out []child
	var rels []string
	for _, e := range entries {
		// Resolve each entry so a symlink pointing outside the root, or at a
		// secret, is hidden rather than listed.
		_, erel, err := f.resolveBasic(filepath.Join(abs, e.Name()))
		if err != nil {
			continue
		}
		c := child{name: e.Name(), rel: erel}
		if fi, err := os.Stat(filepath.Join(abs, c.name)); err == nil && fi.IsDir() {
			c.isDir = true
		}
		out = append(out, c)
		rels = append(rels, erel)
	}
	if skip := f.ignored(rels); len(skip) > 0 {
		kept := out[:0]
		for _, c := range out {
			if !skip[c.rel] {
				kept = append(kept, c)
			}
		}
		out = kept
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// noiseDirs are never worth descending into when showing a tree.
var noiseDirs = map[string]bool{"node_modules": true, "__pycache__": true, ".venv": true, "venv": true}

func (f *FileTools) listDir(p string, depth int) string {
	abs, rel, err := f.resolve(p)
	if err != nil {
		return "error: " + err.Error()
	}
	depth = min(max(depth, 1), maxListDepth)
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n", displayRel(rel))
	budget := maxListEntries
	if err := f.writeTree(&b, abs, depth, 0, &budget); err != nil {
		return "error: not a readable folder"
	}
	if budget < 0 {
		b.WriteString("… more entries not shown\n")
	}
	return b.String()
}

// writeTree lists a folder's entries, indented by level, descending into
// subfolders until depth levels are shown. budget counts the entries left to
// print across the whole tree and goes negative once it ran out.
func (f *FileTools) writeTree(b *strings.Builder, abs string, depth, level int, budget *int) error {
	kids, err := f.children(abs)
	if err != nil {
		return err
	}
	if len(kids) == 0 && level == 0 {
		b.WriteString("(empty)\n")
	}
	for _, c := range kids {
		if *budget <= 0 {
			*budget = -1
			return nil
		}
		*budget--
		name := c.name
		if c.isDir {
			name += "/"
		}
		b.WriteString(strings.Repeat("  ", level) + name + "\n")
		if c.isDir && level+1 < depth && !noiseDirs[c.name] {
			// A subfolder that can't be read is skipped, not fatal.
			_ = f.writeTree(b, filepath.Join(abs, c.name), depth, level+1, budget)
			if *budget < 0 {
				return nil
			}
		}
	}
	return nil
}

func (f *FileTools) readFile(p string, start, maxLines int) string {
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
	end := min(start-1+maxLines, len(lines))
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
