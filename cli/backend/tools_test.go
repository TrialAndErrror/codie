package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// project builds a folder with a sibling "outside" folder next to it, so
// escape attempts have something real to hit.
func project(t *testing.T) (*FileTools, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "sub"), filepath.Join(root, ".git"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "main.go"):        "package main\n\nfunc main() {}\n",
		filepath.Join(root, "sub", "a.txt"):   "hello\n",
		filepath.Join(root, ".env"):           "API_KEY=hunter2\n",
		filepath.Join(root, "example.env"):    "API_KEY=\n",
		filepath.Join(root, "server.pem"):     "-----BEGIN PRIVATE KEY-----\n",
		filepath.Join(root, ".git", "config"): "[core]\n",
		filepath.Join(outside, "secret.txt"):  "TOP SECRET\n",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "link-out")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "link-env")); err != nil {
		t.Fatal(err)
	}
	ft, err := NewFileTools(root)
	if err != nil {
		t.Fatal(err)
	}
	return ft, root, outside
}

func TestReadFileAndListDir(t *testing.T) {
	ft, _, _ := project(t)
	got, act := ft.Run("read_file", `{"path":"main.go"}`)
	if !strings.Contains(got, "func main()") || act != "reading main.go" {
		t.Fatalf("read: %q (%q)", got, act)
	}
	got, _ = ft.Run("read_file", `{"path":"sub/a.txt"}`)
	if !strings.Contains(got, "hello") {
		t.Fatalf("nested read: %q", got)
	}
	got, _ = ft.Run("list_dir", `{"path":"."}`)
	for _, want := range []string{"main.go", "sub/", "example.env"} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q:\n%s", want, got)
		}
	}
}

func TestToolsRefuseEverythingOutsideOrSecret(t *testing.T) {
	ft, root, outside := project(t)
	cases := map[string]string{
		"dotdot":          `{"path":"../outside/secret.txt"}`,
		"absolute":        fmt.Sprintf(`{"path":%q}`, filepath.Join(outside, "secret.txt")),
		"symlinked dir":   `{"path":"link-out/secret.txt"}`,
		"symlinked file":  `{"path":"link-file"}`,
		"symlink to .env": `{"path":"link-env"}`,
		"dotenv":          `{"path":".env"}`,
		"pem key":         `{"path":"server.pem"}`,
		"git config":      `{"path":".git/config"}`,
	}
	for name, args := range cases {
		got, _ := ft.Run("read_file", args)
		if !strings.HasPrefix(got, "error:") {
			t.Errorf("%s: should be refused, got %q", name, got)
		}
		for _, leak := range []string{"TOP SECRET", "hunter2", "PRIVATE KEY", "[core]"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: leaked %q", name, leak)
			}
		}
	}
	if got, _ := ft.Run("list_dir", fmt.Sprintf(`{"path":%q}`, filepath.Dir(root))); !strings.HasPrefix(got, "error:") {
		t.Errorf("listing the parent should be refused: %q", got)
	}
	if got, _ := ft.Run("list_dir", `{"path":"link-out"}`); !strings.HasPrefix(got, "error:") {
		t.Errorf("listing through an escaping symlink should be refused: %q", got)
	}
}

func TestListingHidesSecretsAndEscapes(t *testing.T) {
	ft, _, _ := project(t)
	got, _ := ft.Run("list_dir", `{"path":"."}`)
	shown := strings.Split(got, "\n")
	for _, hidden := range []string{".env", "server.pem", ".git/", "link-out/", "link-file", "link-env"} {
		for _, line := range shown {
			if line == hidden {
				t.Errorf("listing shows %q:\n%s", hidden, got)
			}
		}
	}
}

func TestReadFileLimits(t *testing.T) {
	ft, root, _ := project(t)
	var long strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&long, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(root, "long.txt"), []byte(long.String()), 0o644)
	os.WriteFile(filepath.Join(root, "bin.dat"), []byte("ab\x00cd"), 0o644)
	os.WriteFile(filepath.Join(root, "huge.txt"), []byte(strings.Repeat("x", maxFileBytes*2)), 0o644)

	got, _ := ft.Run("read_file", `{"path":"long.txt"}`)
	last, next := fmt.Sprintf("line %d\n", maxReadLines), fmt.Sprintf("line %d\n", maxReadLines+1)
	if !strings.Contains(got, last) || strings.Contains(got, next) || !strings.Contains(got, fmt.Sprintf("start_line=%d", maxReadLines+1)) {
		t.Errorf("line cap/continuation wrong:\n%.200s…", got[max(len(got)-200, 0):])
	}
	got, _ = ft.Run("read_file", fmt.Sprintf(`{"path":"long.txt","start_line":%d}`, maxReadLines+1))
	if !strings.Contains(got, next) || strings.Contains(got, last) {
		t.Errorf("start_line ignored")
	}
	if got, _ = ft.Run("read_file", `{"path":"bin.dat"}`); !strings.Contains(got, "binary") {
		t.Errorf("binary file: %q", got)
	}
	if got, _ = ft.Run("read_file", `{"path":"huge.txt"}`); len(got) > maxFileBytes+500 {
		t.Errorf("huge file not capped: %d bytes", len(got))
	}
	if got, _ = ft.Run("read_file", `{"path":"nope.txt"}`); !strings.HasPrefix(got, "error:") {
		t.Errorf("missing file: %q", got)
	}
	if got, _ = ft.Run("read_file", `not json`); !strings.HasPrefix(got, "error:") {
		t.Errorf("bad args: %q", got)
	}
}

func TestActivityIsSanitized(t *testing.T) {
	ft, _, _ := project(t)
	_, act := ft.Run("read_file", "{\"path\":\"a\\u001b[2Jb\"}")
	if strings.ContainsRune(act, 0x1b) {
		t.Errorf("escape in activity: %q", act)
	}
}

// ---- the tool loop against a fake server -----------------------------------

func toolCallChunks(id, name string, argParts ...string) []string {
	out := []string{fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"function":{"name":%q,"arguments":""}}]}}]}`, id, name)}
	for _, p := range argParts {
		b, _ := json.Marshal(p)
		out = append(out, fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%s}}]}}]}`, b))
	}
	return append(out, finishChunk("tool_calls"), "[DONE]")
}

// toolServer answers the first request with a read_file call (arguments split
// across chunks) and the second with text, recording every request body.
func toolServer(t *testing.T, bodies *[]chatRequest) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		*bodies = append(*bodies, req)
		if n.Add(1) == 1 {
			sse(w, toolCallChunks("call_9", "read_file", `{"pa`, `th":"ma`, `in.go"}`)...)
			return
		}
		sse(w, delta("It is a package main."), finishChunk("stop"), "[DONE]")
	})
}

func TestApprovedReadFlowsThroughToTheAnswer(t *testing.T) {
	ft, _, _ := project(t)
	var bodies []chatRequest
	o := client(t, toolServer(t, &bodies))
	o.Files = ft

	var evs []Event
	for ev := range o.Research(context.Background(), "what does main.go do?") {
		evs = append(evs, ev)
		if ev.Kind == Approval {
			if !strings.Contains(ev.Text, ft.Root()) {
				t.Errorf("approval should name the folder: %q", ev.Text)
			}
			ev.Reply <- true
		}
	}
	var kinds []Kind
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	want := []Kind{Approval, Activity, Text, Done}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	if evs[1].Text != "reading main.go" {
		t.Errorf("activity: %q", evs[1].Text)
	}
	if len(bodies) != 2 || len(bodies[0].Tools) != 2 {
		t.Fatalf("requests: %d, tools in first: %d", len(bodies), len(bodies[0].Tools))
	}
	// The second request carries the assistant's call and the file contents.
	msgs := bodies[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "call_9" || !strings.Contains(last.Content, "func main()") {
		t.Errorf("tool result message: %+v", last)
	}
	if asst := msgs[len(msgs)-2]; len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Function.Arguments != `{"path":"main.go"}` {
		t.Errorf("arguments were not reassembled: %+v", asst.ToolCalls)
	}
}

func TestDeniedReadSendsNoFileContentsAndStopsOfferingTools(t *testing.T) {
	ft, _, _ := project(t)
	var bodies []chatRequest
	o := client(t, toolServer(t, &bodies))
	o.Files = ft

	for ev := range o.Research(context.Background(), "q") {
		if ev.Kind == Approval {
			ev.Reply <- false
		}
		if ev.Kind == Activity {
			t.Errorf("nothing should be read after a no: %q", ev.Text)
		}
	}
	if len(bodies) != 2 {
		t.Fatalf("requests: %d", len(bodies))
	}
	if len(bodies[1].Tools) != 0 {
		t.Errorf("tools still offered after a refusal")
	}
	for _, m := range bodies[1].Messages {
		if strings.Contains(m.Content, "func main()") {
			t.Errorf("file contents sent despite refusal")
		}
	}
}

func TestCancelWhileWaitingForApprovalStopsTheStream(t *testing.T) {
	ft, _, _ := project(t)
	var bodies []chatRequest
	o := client(t, toolServer(t, &bodies))
	o.Files = ft

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := o.Research(ctx, "q")
	for ev := range ch {
		if ev.Kind == Approval {
			cancel() // never answer
		}
	}
	if len(bodies) != 1 {
		t.Errorf("made %d requests after cancel", len(bodies))
	}
}

func TestWithoutFilesNoToolsAreSent(t *testing.T) {
	var bodies []chatRequest
	o := client(t, toolServer(t, &bodies))
	// Files left nil.
	got, err := Collect(o.Research(context.Background(), "q"))
	if len(bodies) != 1 || len(bodies[0].Tools) != 0 {
		t.Errorf("tools sent without Files: %+v (%q, %v)", bodies, got, err)
	}
	if strings.Contains(bodies[0].Messages[0].Content, "list_dir") {
		t.Errorf("tool instructions leaked into the prompt")
	}
}

func TestToolCallLimit(t *testing.T) {
	ft, _, _ := project(t)
	var n atomic.Int32
	var last chatRequest
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		last = req
		if len(req.Tools) > 0 {
			n.Add(1)
			sse(w, toolCallChunks(fmt.Sprintf("c%d", n.Load()), "list_dir", `{"path":"."}`)...)
			return
		}
		sse(w, delta("done"), finishChunk("stop"), "[DONE]")
	})
	o := client(t, srv)
	o.Files = ft
	var reads int
	for ev := range o.Research(context.Background(), "q") {
		switch ev.Kind {
		case Approval:
			ev.Reply <- true
		case Activity:
			reads++
		}
	}
	if reads != MaxToolCalls {
		t.Errorf("reads = %d, want %d", reads, MaxToolCalls)
	}
	if len(last.Tools) != 0 {
		t.Errorf("tools still offered after the limit")
	}
}

// ---- .gitignore ------------------------------------------------------------

func gitProject(t *testing.T) (*FileTools, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, root, _ := project(t)
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "ignored.txt\nbuild/\n*.log\n!keep.log\n")
	write("sub/.gitignore", "local.txt\n")
	write("ignored.txt", "IGNORED-CONTENT\n")
	write("build/out.bin.txt", "BUILD-CONTENT\n")
	write("debug.log", "LOG-CONTENT\n")
	write("keep.log", "KEPT\n")
	write("sub/local.txt", "NESTED-IGNORED\n")
	write("sub/shown.txt", "SHOWN\n")
	ft, err := NewFileTools(root)
	if err != nil {
		t.Fatal(err)
	}
	return ft, root
}

func TestGitignoredFilesAreRefused(t *testing.T) {
	ft, _ := gitProject(t)
	for _, p := range []string{"ignored.txt", "build/out.bin.txt", "debug.log", "sub/local.txt"} {
		got, _ := ft.Run("read_file", fmt.Sprintf(`{"path":%q}`, p))
		if !strings.HasPrefix(got, "error:") || strings.Contains(got, "CONTENT") {
			t.Errorf("%s should be refused, got %q", p, got)
		}
	}
	if got, _ := ft.Run("list_dir", `{"path":"build"}`); !strings.HasPrefix(got, "error:") {
		t.Errorf("listing an ignored folder should be refused: %q", got)
	}
	for _, p := range []string{"keep.log", "sub/shown.txt", "main.go"} {
		if got, _ := ft.Run("read_file", fmt.Sprintf(`{"path":%q}`, p)); strings.HasPrefix(got, "error:") {
			t.Errorf("%s is not ignored but was refused: %q", p, got)
		}
	}
}

func TestListingHidesGitignored(t *testing.T) {
	ft, _ := gitProject(t)
	got, _ := ft.Run("list_dir", `{"path":"."}`)
	lines := strings.Split(got, "\n")
	has := func(n string) bool {
		for _, l := range lines {
			if l == n {
				return true
			}
		}
		return false
	}
	for _, hidden := range []string{"ignored.txt", "build/", "debug.log"} {
		if has(hidden) {
			t.Errorf("listing shows ignored %q:\n%s", hidden, got)
		}
	}
	for _, shown := range []string{"keep.log", "main.go", "sub/", ".gitignore"} {
		if !has(shown) {
			t.Errorf("listing lost %q:\n%s", shown, got)
		}
	}
	if sub, _ := ft.Run("list_dir", `{"path":"sub"}`); strings.Contains(sub, "local.txt") || !strings.Contains(sub, "shown.txt") {
		t.Errorf("nested .gitignore not honored:\n%s", sub)
	}
}

func TestGitignoreHonoredWhenRootIsASubfolderOfTheRepo(t *testing.T) {
	_, root := gitProject(t)
	ft, err := NewFileTools(filepath.Join(root, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ft.Run("read_file", `{"path":"local.txt"}`); !strings.HasPrefix(got, "error:") {
		t.Errorf("ignored file readable from a subfolder root: %q", got)
	}
	if got, _ := ft.Run("read_file", `{"path":"shown.txt"}`); strings.HasPrefix(got, "error:") {
		t.Errorf("normal file refused: %q", got)
	}
}

func TestOutsideARepoNothingIsIgnored(t *testing.T) {
	ft, _, _ := project(t) // not a git repo
	if got := ft.ignored([]string{"main.go"}); len(got) != 0 {
		t.Errorf("ignored outside a repo: %v", got)
	}
}

func TestReadSeveralFilesInOneCall(t *testing.T) {
	ft, root, _ := project(t)
	var long strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&long, "row %d\n", i)
	}
	os.WriteFile(filepath.Join(root, "long.txt"), []byte(long.String()), 0o644)

	got, act := ft.Run("read_file", `{"paths":["main.go","sub/a.txt","long.txt",".env"]}`)
	for _, want := range []string{"func main()", "hello", "row 1\n", fmt.Sprintf("row %d\n", maxMultiLines)} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%.300s", want, got)
		}
	}
	if strings.Contains(got, fmt.Sprintf("row %d\n", maxMultiLines+1)) {
		t.Errorf("per-file cap not applied in a multi read")
	}
	if strings.Contains(got, "hunter2") || !strings.Contains(got, "error:") {
		t.Errorf("a blocked file in a batch must be refused, not leaked:\n%s", got)
	}
	if !strings.Contains(act, "main.go") || !strings.Contains(act, "sub/a.txt") {
		t.Errorf("activity: %q", act)
	}

	// More paths than allowed: the extras are not read.
	many := `{"paths":["main.go","sub/a.txt","main.go","sub/a.txt","long.txt"]}`
	if got, _ := ft.Run("read_file", many); strings.Contains(got, "row 1\n") {
		t.Errorf("read more than %d paths", maxReadPaths)
	}
	// path and paths together are both honoured.
	if got, _ := ft.Run("read_file", `{"path":"main.go","paths":["sub/a.txt"]}`); !strings.Contains(got, "func main()") || !strings.Contains(got, "hello") {
		t.Errorf("path+paths: %q", got)
	}
	if got, _ := ft.Run("read_file", `{}`); !strings.HasPrefix(got, "error:") {
		t.Errorf("no path: %q", got)
	}
}

func TestListDirDepth(t *testing.T) {
	ft, root, _ := project(t)
	os.MkdirAll(filepath.Join(root, "sub", "deep", "deeper"), 0o755)
	os.WriteFile(filepath.Join(root, "sub", "deep", "d.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "sub", "deep", "deeper", "e.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755)

	flat, _ := ft.Run("list_dir", `{"path":"."}`)
	if strings.Contains(flat, "a.txt") {
		t.Errorf("depth 1 should not descend:\n%s", flat)
	}
	two, _ := ft.Run("list_dir", `{"path":".","depth":2}`)
	if !strings.Contains(two, "\n  a.txt\n") || !strings.Contains(two, "  deep/\n") || strings.Contains(two, "d.txt") {
		t.Errorf("depth 2:\n%s", two)
	}
	three, _ := ft.Run("list_dir", `{"path":".","depth":99}`) // clamped to 3
	if !strings.Contains(three, "d.txt") || strings.Contains(three, "e.txt") {
		t.Errorf("depth clamp:\n%s", three)
	}
	if strings.Contains(three, "pkg") {
		t.Errorf("node_modules should not be descended:\n%s", three)
	}
	// Secrets and escapes stay hidden in a tree too.
	for _, line := range strings.Split(three, "\n") {
		switch strings.TrimSpace(line) {
		case ".env", "link-out/", "link-file", "link-env", ".git/", "server.pem":
			t.Errorf("tree shows %q:\n%s", line, three)
		}
	}
}

func TestListTreeIsCapped(t *testing.T) {
	ft, root, _ := project(t)
	for i := 0; i < maxListEntries+50; i++ {
		os.WriteFile(filepath.Join(root, "sub", fmt.Sprintf("f%03d.txt", i)), nil, 0o644)
	}
	got, _ := ft.Run("list_dir", `{"path":".","depth":3}`)
	if n := strings.Count(got, "\n"); n > maxListEntries+5 || !strings.Contains(got, "more entries not shown") {
		t.Errorf("tree not capped (%d lines)", n)
	}
}

func TestPromptAsksForSparingReads(t *testing.T) {
	for _, want := range []string{"at most three", "paths", "depth", "Never read everything"} {
		if !strings.Contains(filesPrompt, want) {
			t.Errorf("filesPrompt lost %q", want)
		}
	}
	if MaxToolCalls > 6 {
		t.Errorf("MaxToolCalls = %d; a long chain of calls is what makes reads slow", MaxToolCalls)
	}
}
