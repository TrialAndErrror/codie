package tui

import (
	"reflect"
	"strings"
	"testing"

	"codie/backend"
)

const f = "```" // keeps the fences in these tests readable

func lines(ls ...string) string { return strings.Join(ls, "\n") }

func TestParseAnswer(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []segment
	}{
		{"empty", "", nil},
		{"only whitespace", "\n  \n", nil},
		{
			"prose only",
			"Just words.\nTwo lines.",
			[]segment{{Text: "Just words.\nTwo lines."}},
		},
		{
			"one block, nothing else",
			lines(f+"go", "fmt.Println(1)", f),
			[]segment{{Code: true, Text: "fmt.Println(1)", Info: "go"}},
		},
		{
			"prose around a block is kept separate",
			lines("Before.", f+"go", "x := 1", f, "After."),
			[]segment{{Text: "Before."}, {Code: true, Text: "x := 1", Info: "go"}, {Text: "After."}},
		},
		{
			"several blocks with explanation between them",
			lines("Put this in main.go:", f+"go", "package main", f, "Then this in util.go:", f+"go", "package main", "func Util() {}", f),
			[]segment{
				{Text: "Put this in main.go:"},
				{Code: true, Text: "package main", Info: "go"},
				{Text: "Then this in util.go:"},
				{Code: true, Text: "package main\nfunc Util() {}", Info: "go"},
			},
		},
		{
			"blocks back to back",
			lines(f+"sh", "a", f, f+"sh", "b", f),
			[]segment{{Code: true, Text: "a", Info: "sh"}, {Code: true, Text: "b", Info: "sh"}},
		},
		{
			"no language",
			lines(f, "plain", f),
			[]segment{{Code: true, Text: "plain"}},
		},
		{
			"info string keeps everything after the fence",
			lines(f+"go main.go", "x", f),
			[]segment{{Code: true, Text: "x", Info: "go main.go"}},
		},
		{
			"tilde fences",
			lines("~~~python", "print(1)", "~~~"),
			[]segment{{Code: true, Text: "print(1)", Info: "python"}},
		},
		{
			"a longer outer fence can contain a shorter inner one",
			lines("````md", "Example:", f+"go", "x", f, "````"),
			[]segment{{Code: true, Text: lines("Example:", f+"go", "x", f), Info: "md"}},
		},
		{
			"a shorter fence does not close a longer one",
			lines("````", "a", f, "b", "````"),
			[]segment{{Code: true, Text: lines("a", f, "b")}},
		},
		{
			"a shorter fence still does not close a longer one when padded with spaces",
			lines("````", "a", f+"    ", "b", "````"),
			[]segment{{Code: true, Text: lines("a", f+"    ", "b")}},
		},
		{
			"a different fence character does not close the block",
			lines(f, "a", "~~~", "b", f),
			[]segment{{Code: true, Text: lines("a", "~~~", "b")}},
		},
		{
			"closing fence may have trailing spaces",
			lines(f, "a", f+"   "),
			[]segment{{Code: true, Text: "a"}},
		},
		{
			"text after a closing fence on the same line keeps the block open",
			lines(f, "a", f+" oops", "b", f),
			[]segment{{Code: true, Text: lines("a", f+" oops", "b")}},
		},
		{
			"fences may be indented up to three spaces",
			lines("   "+f+"go", "x", "   "+f),
			[]segment{{Code: true, Text: "x", Info: "go"}},
		},
		{
			"four spaces of indent is not a fence",
			lines("    "+f+"go", "x"),
			[]segment{{Text: lines("    "+f+"go", "x")}},
		},
		{
			"a line that starts with inline triple backticks is not a fence",
			f + "inline" + f + " is how you write it",
			[]segment{{Text: f + "inline" + f + " is how you write it"}},
		},
		{
			"inline triple backticks on one line are not a fence",
			"Use " + f + "foo" + f + " here.",
			[]segment{{Text: "Use " + f + "foo" + f + " here."}},
		},
		{
			"a block that is never closed runs to the end",
			lines("Intro.", f+"go", "func main() {", "}"),
			[]segment{{Text: "Intro."}, {Code: true, Text: "func main() {\n}", Info: "go"}},
		},
		{
			"an opening fence alone is an empty block",
			f + "go",
			[]segment{{Code: true, Text: "", Info: "go"}},
		},
		{
			"CRLF line endings",
			"Hi\r\n" + f + "go\r\nx := 1\r\n" + f + "\r\n",
			[]segment{{Text: "Hi"}, {Code: true, Text: "x := 1", Info: "go"}},
		},
		{
			"blank lines, indentation and tabs inside code are preserved",
			lines(f+"go", "func a() {", "\tx := 1", "", "\treturn", "}", f),
			[]segment{{Code: true, Text: "func a() {\n\tx := 1\n\n\treturn\n}", Info: "go"}},
		},
		{
			"trailing blank lines in a block are trimmed, leading ones are not",
			lines(f, "", "a", "", "", f),
			[]segment{{Code: true, Text: "\na"}},
		},
		{
			"surrounding blank lines in prose are trimmed",
			lines("", "", "Hello", "", f, "x", f, "", "", "Bye", ""),
			[]segment{{Text: "Hello"}, {Code: true, Text: "x"}, {Text: "Bye"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAnswer(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

func TestSegmentLang(t *testing.T) {
	for info, want := range map[string]string{"": "", "go": "go", "Go": "go", "go main.go": "go", "  python  ": "python", "c++": "c++"} {
		if got := (segment{Code: true, Info: info}).Lang(); got != want {
			t.Errorf("Lang(%q) = %q, want %q", info, got, want)
		}
	}
}

func TestSelectable(t *testing.T) {
	if (segment{Text: "prose"}).selectable() {
		t.Error("prose is not selectable")
	}
	if (segment{Code: true, Text: "  \n\t"}).selectable() {
		t.Error("a blank code block is not selectable")
	}
	if !(segment{Code: true, Text: "x"}).selectable() {
		t.Error("a code block with content is selectable")
	}
}

// A model is being parsed mid-stream, so every prefix of an answer must parse,
// and the blocks found so far must only ever grow or fill in, never vanish.
func TestEveryPrefixOfAStreamParses(t *testing.T) {
	answer := lines(
		"Add a handler in health.go:", f+"go", "package main", "", "func Health() string {", "\treturn \"ok\"", "}", f,
		"Then call it from main.go:", f+"go", "func main() { println(Health()) }", f,
	)
	prevBlocks := 0
	for i := 0; i <= len(answer); i++ {
		segs := parseAnswer(answer[:i])
		blocks := 0
		for _, s := range segs {
			if s.Code {
				blocks++
			}
		}
		if blocks < prevBlocks {
			t.Fatalf("at byte %d the number of blocks dropped from %d to %d: %q", i, prevBlocks, blocks, answer[:i])
		}
		prevBlocks = blocks
	}
	final := parseAnswer(answer)
	if len(final) != 4 || !final[1].Code || !final[3].Code ||
		final[1].Text != "package main\n\nfunc Health() string {\n\treturn \"ok\"\n}" {
		t.Fatalf("final parse wrong: %#v", final)
	}
}

func FuzzParseAnswer(f *testing.F) {
	for _, seed := range []string{
		"", "```", "```go\nx\n```", "~~~\n```\n~~~", "````\n```\n````", "a\r\n```\r\nb", "   ```x```", "```\n\n```\n```",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		in = backend.Sanitize(in) // answers are sanitized before they are parsed
		segs := parseAnswer(in)
		total := 0
		for _, s := range segs {
			if strings.Contains(s.Text, "\r") {
				t.Fatalf("segment kept a carriage return: %q", s.Text)
			}
			if !s.Code && strings.TrimSpace(s.Text) == "" {
				t.Fatalf("empty prose segment emitted for %q", in)
			}
			total += len(s.Text)
		}
		if total > len(in) {
			t.Fatalf("output (%d bytes) larger than input (%d bytes)", total, len(in))
		}
	})
}
