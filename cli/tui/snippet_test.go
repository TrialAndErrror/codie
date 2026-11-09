package tui

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// bannered is what `c` should put on the clipboard for a block.
func bannered(lang, code string) string {
	w, _ := withBanner(lang, code)
	return w
}

// This is the design as it was asked for, character for character.
func TestPythonBannerMatchesTheRequestedDesignExactly(t *testing.T) {
	want := strings.Join([]string{
		"#############################",
		"# Codie Code Snippet: Begin #",
		"#############################",
		"",
		"def main():",
		"    pass",
		"",
		"#############################",
		"# Codie Code Snippet:  End  #",
		"#############################",
	}, "\n")
	got, ok := withBanner("python", "def main():\n    pass")
	if !ok || got != want {
		t.Fatalf("ok=%v\n got:\n%s\nwant:\n%s", ok, got, want)
	}
}

func TestEveryLanguageWithoutAnotherStyleGetsTheRequestedHashBanner(t *testing.T) {
	want := bannered("python", "x")
	for _, lang := range []string{"", "klingon", "txt", "console", "sh", "bash", "ruby", "yaml"} {
		if got, ok := withBanner(lang, "x"); !ok || got != want {
			t.Errorf("language %q should get the # banner (ok=%v):\n%s", lang, ok, got)
		}
	}
}

func TestBannerBoxesMatchInWidth(t *testing.T) {
	for _, lang := range []string{"python", "go", "sql", "css", "html"} {
		lines := strings.Split(bannerLines(t, lang), "\n")
		if len(lines) != 6 {
			t.Fatalf("%s: want two 3-line boxes, got %d lines", lang, len(lines))
		}
		for i, l := range lines {
			if len(l) != len(lines[0]) {
				t.Errorf("%s: line %d is %d wide, the first is %d:\n%s", lang, i+1, len(l), len(lines[0]), strings.Join(lines, "\n"))
			}
		}
	}
}

// bannerLines returns just the six banner lines (no code) for lang.
func bannerLines(t *testing.T, lang string) string {
	t.Helper()
	got, ok := withBanner(lang, "CODE")
	if !ok {
		t.Fatalf("%s: expected a banner", lang)
	}
	var out []string
	for _, l := range strings.Split(got, "\n") {
		if l != "" && l != "CODE" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// Every line of the banner must be a comment in the snippet's language, or pasting
// it into a source file would break the file.
func TestEveryBannerLineIsAComment(t *testing.T) {
	tests := []struct {
		lang, open, close string
	}{
		{"python", "#", ""}, {"sh", "#", ""}, {"yaml", "#", ""},
		{"go", "//", ""}, {"javascript", "//", ""}, {"rust", "//", ""}, {"java", "//", ""}, {"typescript", "//", ""},
		{"sql", "--", ""}, {"haskell", "--", ""}, {"lua", "--", ""},
		{"css", "/*", "*/"},
		{"html", "<!--", "-->"}, {"xml", "<!--", "-->"}, {"markdown", "<!--", "-->"},
	}
	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			for _, l := range strings.Split(bannerLines(t, tt.lang), "\n") {
				if !strings.HasPrefix(l, tt.open) {
					t.Errorf("line does not start a %q comment: %q", tt.open, l)
				}
				if tt.close != "" && !strings.HasSuffix(l, tt.close) {
					t.Errorf("line does not end with %q: %q", tt.close, l)
				}
			}
		})
	}
}

func TestBannerSaysBeginAndEndInTheRightPlaces(t *testing.T) {
	for _, lang := range []string{"python", "go", "sql", "css", "html"} {
		got := bannered(lang, "CODE")
		b, e, c := strings.Index(got, "Begin"), strings.Index(got, "End"), strings.Index(got, "CODE")
		if b < 0 || e < 0 || !(b < c && c < e) {
			t.Errorf("%s: want Begin, then the code, then End:\n%s", lang, got)
		}
		if strings.Count(got, "Codie Code Snippet") != 2 {
			t.Errorf("%s: the banner text should appear exactly twice:\n%s", lang, got)
		}
	}
}

// The wrapper may add lines around the code but must never alter a byte of it.
func TestBannerLeavesTheCodeByteForByte(t *testing.T) {
	code := "func a() {\n\tif x {\n\t\treturn   \n\t}\n\n\n\tb() // end \n}\n\t"
	for _, lang := range []string{"go", "python", "sql", "css", "html", ""} {
		got := bannered(lang, code)
		style := languageStyles[lang]
		if _, ok := languageStyles[lang]; !ok {
			style = hashStyle
		}
		prefix := banner(style, bannerBegin) + "\n\n"
		suffix := "\n\n" + banner(style, bannerEnd)
		if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
			t.Fatalf("%s: unexpected wrapper:\n%s", lang, got)
		}
		if middle := strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix); middle != code {
			t.Errorf("%s: the code changed inside the banner:\n got %q\nwant %q", lang, middle, code)
		}
	}
}

func TestLanguagesWithoutCommentsGetNoBanner(t *testing.T) {
	code := `{"a": 1}`
	got, ok := withBanner("json", code)
	if ok || got != code {
		t.Fatalf("JSON has no comments, so it must be returned untouched (ok=%v): %q", ok, got)
	}
}

func TestEveryKnownLanguageProducesABanner(t *testing.T) {
	var langs []string
	for l := range languageStyles {
		langs = append(langs, l)
	}
	for l := range langExt { // every language we know how to save
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, l := range langs {
		_, ok := withBanner(l, "x")
		if noComments[l] {
			if ok {
				t.Errorf("%q has no comment syntax and must not get a banner", l)
			}
			continue
		}
		if !ok {
			t.Errorf("%q should get a banner", l)
		}
	}
}

// ---- pasting it must not break real code -------------------------------------------------

func TestAGoFileWithTheBannerStillParses(t *testing.T) {
	src := bannered("go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("a Go file wrapped in the banner no longer parses: %v\n%s", err, src)
	}
	if len(file.Comments) < 2 {
		t.Fatalf("expected the banner to be parsed as comments, found %d comment groups", len(file.Comments))
	}
}

func TestAPythonSnippetWithTheBannerStillCompiles(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	src := bannered("python", "def main():\n    pass\n\nmain()")
	cmd := exec.Command(py, "-c", "import sys; compile(sys.stdin.read(), 'snippet', 'exec')")
	cmd.Stdin = strings.NewReader(src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a Python snippet wrapped in the banner no longer compiles: %v\n%s\n%s", err, out, src)
	}
}

func TestAShellSnippetWithTheBannerStillParses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	src := bannered("sh", "echo hello\nls -la")
	cmd := exec.Command(sh, "-n")
	cmd.Stdin = strings.NewReader(src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a shell snippet wrapped in the banner no longer parses: %v\n%s", err, out)
	}
}

// ---- the keys: c with markers, C plain -------------------------------------------------------

func TestCopyAddsTheMarkersAndPlainCopyDoesNot(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)
	h.typ("j")

	h.typ("c")
	if h.copied != bannered("go", blockA) || h.model().status != "copied block 1/3 (go) with begin/end markers" {
		t.Fatalf("c: copied %q, status %q", h.copied, h.model().status)
	}
	if !strings.Contains(h.copied, "// Codie Code Snippet: Begin //") || !strings.Contains(h.copied, "// Codie Code Snippet:  End  //") {
		t.Fatalf("a Go block should get // markers:\n%s", h.copied)
	}

	h.typ("C")
	if h.copied != blockA || h.model().status != "copied block 1/3 (go) as plain code" {
		t.Fatalf("C: copied %q, status %q", h.copied, h.model().status)
	}
	if h.copies != 2 {
		t.Fatalf("each key press should copy once, got %d", h.copies)
	}
}

func TestPlainCopyNeedsASelectionToo(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)
	h.typ("C")
	if h.copies != 0 || !strings.Contains(h.model().status, "press j or k") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
}

func TestPlainCopyIsBlockedWhileStreamingAndForIncompleteAnswers(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("Words.\n" + f + "go\nx()\n" + f + "\n")
	h.typ("C")
	if h.copies != 0 || !strings.Contains(h.model().status, "still answering") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
	h.fail("lost")
	h.typ("C")
	if h.copies != 0 || !strings.Contains(h.model().status, "incomplete") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
}

func TestJSONIsCopiedWithoutMarkersAndTheStatusSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", "Here is the config.\n"+f+"json\n{\"a\": 1}\n"+f)
	h.typ("j")
	h.typ("c")
	if h.copied != `{"a": 1}` {
		t.Fatalf("JSON must be copied untouched, a comment would make it invalid: %q", h.copied)
	}
	if !strings.Contains(h.model().status, "json has no comments") {
		t.Fatalf("status should explain the missing markers: %q", h.model().status)
	}
}

func TestSavingNeverAddsTheMarkers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	h := newHarness(t)
	h.answerWith("save me", multi)
	h.typ("j")
	h.key(tea.KeyCtrlS)
	h.key(tea.KeyEnter)
	b := readFile(t, dir+"/save-me.go")
	if b != blockA || strings.Contains(b, "Codie Code Snippet") {
		t.Fatalf("a saved file should hold the code only, not the copy markers: %q", b)
	}
}

func TestFooterOffersBothCopies(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)
	h.typ("j")
	v := h.model().View()
	if !strings.Contains(v, "c copy") || !strings.Contains(v, "C plain") {
		t.Fatalf("the footer should offer c and C:\n%s", v)
	}
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "C plain") && len([]rune(line)) > 80 {
			t.Fatalf("the footer should fit an 80-column terminal, it is %d wide: %q", len([]rune(line)), line)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
