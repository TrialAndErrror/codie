package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codie/backend"

	tea "github.com/charmbracelet/bubbletea"
)

// multi is a finished answer in the format the prompt asks for: explanations
// with code blocks between them.
var multi = strings.Join([]string{
	"Put this in a.go:", f + "go", "package a", "", "func A() {}", f,
	"Then this in b.go:", f + "go", "package b", "", "func B() {}", f,
	"And run it like this:", f + "sh", "go run .", f,
}, "\n")

const (
	blockA = "package a\n\nfunc A() {}"
	blockB = "package b\n\nfunc B() {}"
	blockC = "go run ."
)

// answerWith runs a question to completion with the given answer.
func (h *harness) answerWith(q, raw string) {
	h.t.Helper()
	h.start(q)
	h.text(raw)
	h.done()
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// ---- picking blocks ------------------------------------------------------------------

func TestFirstJOrDownSelectsTheFirstBlock(t *testing.T) {
	for name, press := range map[string]func(*harness){
		"j":    func(h *harness) { h.typ("j") },
		"down": func(h *harness) { h.key(tea.KeyDown) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.answerWith("q", multi)
			if h.model().sel != -1 {
				t.Fatal("nothing should be selected at first: the response is shown whole")
			}
			press(h)
			if h.model().sel != 0 {
				t.Fatalf("sel = %d, want the first block", h.model().sel)
			}
		})
	}
}

func TestFirstKOrUpSelectsTheLastBlock(t *testing.T) {
	for name, press := range map[string]func(*harness){
		"k":  func(h *harness) { h.typ("k") },
		"up": func(h *harness) { h.key(tea.KeyUp) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.answerWith("q", multi)
			press(h)
			if h.model().sel != 2 {
				t.Fatalf("sel = %d, want the last block (2)", h.model().sel)
			}
		})
	}
}

func TestSelectionCyclesInBothDirections(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)

	var got []int
	for i := 0; i < 4; i++ {
		h.typ("j")
		got = append(got, h.model().sel)
	}
	if fmt.Sprint(got) != "[0 1 2 0]" {
		t.Fatalf("j j j j gave %v, want [0 1 2 0] (wraps)", got)
	}
	h.typ("k")
	if h.model().sel != 2 {
		t.Fatalf("k from the first block should wrap to the last, got %d", h.model().sel)
	}
	h.key(tea.KeyUp)
	h.key(tea.KeyUp)
	if h.model().sel != 0 {
		t.Fatalf("got %d", h.model().sel)
	}
}

func TestBlocksAreFoundAndOnlyRealOnes(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", "Intro.\n"+f+"go\n"+f+"\nEmpty block above is skipped.\n"+f+"go\nreal()\n"+f)
	m := h.model()
	if len(m.blocks) != 1 || m.blocks[0].text != "real()" {
		t.Fatalf("blocks: %+v", m.blocks)
	}
}

// ---- seeing the selection --------------------------------------------------------------

func TestSelectedBlockIsHighlightedAndFooterSaysSo(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)

	v := h.model().View()
	if strings.Contains(v, "▌") {
		t.Fatalf("no block is selected yet, so nothing should carry the selection gutter:\n%s", v)
	}
	if !strings.Contains(v, "│") || !strings.Contains(v, "3 code blocks") || !strings.Contains(v, "j/k select") {
		t.Fatalf("expected plain gutters and a hint about the 3 blocks:\n%s", v)
	}

	h.typ("j")
	v = h.model().View()
	if got := strings.Count(v, "▌"); got != 3 { // exactly the three lines of block A
		t.Fatalf("selection gutter on %d lines, want 3 (block A):\n%s", got, v)
	}
	if !strings.Contains(v, "block 1/3 (go)") || !strings.Contains(v, "c copy") {
		t.Fatalf("footer should name the selected block and offer copy:\n%s", v)
	}

	h.typ("j")
	h.typ("j")
	if v = h.model().View(); !strings.Contains(v, "block 3/3 (sh)") {
		t.Fatalf("footer should follow the selection:\n%s", v)
	}
}

func TestSingleBlockFooterIsGrammatical(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", f+"go\nx\n"+f)
	if v := h.model().View(); !strings.Contains(v, "1 code block •") {
		t.Fatalf("want '1 code block', not '1 code blocks':\n%s", v)
	}
}

func TestEscDeselectsThenGoesBack(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)
	h.typ("j")
	h.key(tea.KeyEsc)
	if m := h.model(); m.sel != -1 || m.screen != screenAnswer {
		t.Fatalf("first esc should only deselect: sel=%d screen=%v", m.sel, m.screen)
	}
	if strings.Contains(h.model().View(), "▌") {
		t.Fatal("the highlight should be gone")
	}
	h.key(tea.KeyEsc)
	if h.model().screen != screenAsk {
		t.Fatal("second esc should go back to the ask screen")
	}
}

// ---- keeping the block on screen ----------------------------------------------------------

// proseBlockAnswer puts `gap` lines of prose before each of three blocks, so the
// later blocks are well outside the viewport.
func proseBlockAnswer(gap, blockLines int) string {
	var parts []string
	for i := 1; i <= 3; i++ {
		for l := 0; l < gap; l++ {
			parts = append(parts, fmt.Sprintf("explanation %d.%d", i, l))
		}
		parts = append(parts, f+"go")
		for l := 0; l < blockLines; l++ {
			parts = append(parts, fmt.Sprintf("block%d_line%d()", i, l))
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, "\n")
}

func TestSelectingScrollsTheBlockIntoView(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", proseBlockAnswer(40, 5)) // pane is 26 rows, blocks are ~45 lines apart

	for i := 0; i < 3; i++ {
		h.typ("j")
		m := h.model()
		first := fmt.Sprintf("block%d_line0()", i+1)
		last := fmt.Sprintf("block%d_line4()", i+1)
		v := m.View()
		if !strings.Contains(v, first) || !strings.Contains(v, last) {
			t.Fatalf("block %d is not fully on screen (offset %d):\n%s", i+1, m.pane.YOffset, v)
		}
	}
	h.typ("j") // wraps back to block 1, which is far above
	if v := h.model().View(); !strings.Contains(v, "block1_line0()") || !strings.Contains(v, "▌") {
		t.Fatalf("wrapping to the first block should scroll back up:\n%s", v)
	}
}

func TestVisibleBlockDoesNotCauseScrolling(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi) // everything fits on one screen
	h.typ("g")
	before := h.model().pane.YOffset
	h.typ("j")
	h.typ("j")
	if h.model().pane.YOffset != before {
		t.Fatal("moving between blocks that are already visible must not scroll the view")
	}
}

func TestTallBlockShowsItsTopAndCanBePaged(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", "Explanation.\n"+f+"go\n"+tall+"\n"+f) // 80 lines, taller than the pane
	h.typ("j")
	m := h.model()
	if m.pane.YOffset > m.blocks[0].start || m.blocks[0].start-m.pane.YOffset > 2 {
		t.Fatalf("a block taller than the pane should show its top: offset=%d start=%d", m.pane.YOffset, m.blocks[0].start)
	}
	before := m.pane.YOffset
	h.key(tea.KeyPgDown)
	if m = h.model(); m.pane.YOffset <= before || m.sel != 0 {
		t.Fatalf("paging should scroll without losing the selection: offset %d -> %d, sel=%d", before, m.pane.YOffset, m.sel)
	}
}

func TestSelectionSurvivesResize(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", proseBlockAnswer(30, 5))
	h.typ("j")
	h.typ("j")
	h.send(tea.WindowSizeMsg{Width: 60, Height: 16})
	m := h.model()
	if m.sel != 1 {
		t.Fatalf("selection lost on resize: %d", m.sel)
	}
	if v := m.View(); !strings.Contains(v, "block2_line0()") {
		t.Fatalf("selected block should still be in view after a resize:\n%s", v)
	}
}

// ---- copying ---------------------------------------------------------------------------------

func TestCopyNeedsASelectionFirst(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)
	h.typ("c")
	if h.copies != 0 || !strings.Contains(h.model().status, "press j or k") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
}

func TestCopyCopiesExactlyTheSelectedBlock(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)

	h.typ("j")
	h.typ("c")
	if h.copied != blockA || h.model().status != "copied block 1/3 (go)" {
		t.Fatalf("copied %q, status %q", h.copied, h.model().status)
	}
	h.typ("j")
	h.typ("c")
	if h.copied != blockB {
		t.Fatalf("copied %q, want block B", h.copied)
	}
	h.typ("j")
	h.typ("c")
	if h.copied != blockC || h.model().status != "copied block 3/3 (sh)" {
		t.Fatalf("copied %q, status %q", h.copied, h.model().status)
	}
	if h.copies != 3 {
		t.Fatalf("each c should copy once, got %d copies", h.copies)
	}
	for _, bad := range []string{f, "Put this in", "Then this in"} {
		if strings.Contains(h.copied, bad) {
			t.Fatalf("copy included fences or explanation: %q", h.copied)
		}
	}
}

func TestCopyKeepsTabsAndBlankLinesExactly(t *testing.T) {
	h := newHarness(t)
	code := "func a() {\n\tif x {\n\t\treturn\n\t}\n\n\tb()\n}"
	h.answerWith("q", "Do this:\n"+f+"go\n"+code+"\n"+f)
	h.typ("j")
	h.typ("c")
	if h.copied != code {
		t.Fatalf("copy altered the code:\n got %q\nwant %q", h.copied, code)
	}
}

func TestCopyIsUnaffectedByWrapping(t *testing.T) {
	h := newHarness(t)
	long := "x := \"" + strings.Repeat("abcdefghij", 30) + "\""
	h.answerWith("q", f+"go\n"+long+"\n"+f)
	h.send(tea.WindowSizeMsg{Width: 40, Height: 20}) // forces heavy wrapping
	h.typ("j")
	h.typ("c")
	if h.copied != long {
		t.Fatalf("wrapping leaked into the copied text: %q", h.copied)
	}
}

func TestCopyWhenThereAreNoBlocks(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", "Just an explanation, no code.")
	h.typ("c")
	if h.copies != 0 || !strings.Contains(h.model().status, "no code blocks") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
}

func TestCopyFailureIsReported(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", multi)
	m := h.model()
	m.copyFn = func(string) error { return fmt.Errorf("no clipboard") }
	h.m = m
	h.typ("j")
	h.typ("c")
	if !strings.Contains(h.model().status, "copy failed") {
		t.Fatalf("status=%q", h.model().status)
	}
}

// ---- when selection must not be offered ---------------------------------------------------------

func TestUpDownScrollWhenThereAreNoBlocks(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", tall)
	h.typ("g")
	if h.model().pane.YOffset != 0 {
		t.Fatal("g should go to the top")
	}
	h.typ("j")
	h.key(tea.KeyDown)
	if h.model().pane.YOffset != 2 || h.model().sel != -1 {
		t.Fatalf("j and down should scroll a line each: offset=%d sel=%d", h.model().pane.YOffset, h.model().sel)
	}
	h.typ("k")
	if h.model().pane.YOffset != 1 {
		t.Fatalf("offset=%d", h.model().pane.YOffset)
	}
	h.typ("G")
	if !h.model().pane.AtBottom() {
		t.Fatal("G should go to the bottom")
	}
}

func TestBlocksCannotBeSelectedWhileStreaming(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("Intro.\n" + f + "go\nfunc partial() {\n")
	h.typ("j")
	h.key(tea.KeyDown)
	if h.model().sel != -1 {
		t.Fatal("a half-written block must not be selectable")
	}
	h.typ("c")
	if h.copies != 0 || !strings.Contains(h.model().status, "still answering") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
	// And once it finishes, it can be.
	h.text("}\n" + f)
	h.done()
	h.typ("j")
	if h.model().sel != 0 {
		t.Fatal("after Done the block should be selectable")
	}
	h.typ("c")
	if h.copied != "func partial() {\n}" {
		t.Fatalf("copied %q", h.copied)
	}
}

func TestBlocksCannotBeSelectedInAnIncompleteAnswer(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("Here:\n" + f + "go\nfunc ok() {}\n" + f + "\nAnd then:\n" + f + "go\nfunc half(")
	h.fail("connection lost")
	h.typ("j")
	h.key(tea.KeyUp)
	if h.model().sel != -1 {
		t.Fatal("an answer that failed must not offer its blocks, even a complete-looking one")
	}
	h.typ("c")
	if h.copies != 0 || !strings.Contains(h.model().status, "incomplete") {
		t.Fatalf("copies=%d status=%q", h.copies, h.model().status)
	}
}

func TestSelectionIsClearedForTheNextAnswer(t *testing.T) {
	h := newHarness(t)
	h.answerWith("one", multi)
	h.typ("j")
	h.key(tea.KeyEsc) // deselect
	h.key(tea.KeyEsc) // back
	h.answerWith("two", multi)
	if h.model().sel != -1 {
		t.Fatal("a new answer must start with nothing selected")
	}
}

// ---- ctrl+c is the stop signal, everywhere ------------------------------------------------------

func TestCtrlCQuitsFromEveryScreen(t *testing.T) {
	tests := map[string]func(*harness){
		"ask screen": func(h *harness) {},
		"answer, finished": func(h *harness) {
			h.answerWith("q", multi)
		},
		"answer, block selected": func(h *harness) {
			h.answerWith("q", multi)
			h.typ("j")
		},
		"answer, still streaming": func(h *harness) {
			h.start("q")
			h.text("partial")
		},
		"save prompt": func(h *harness) {
			h.answerWith("q", multi)
			h.key(tea.KeyCtrlS)
		},
		"history list": func(h *harness) {
			h.answerWith("q", multi)
			h.key(tea.KeyEsc)
			h.key(tea.KeyCtrlH)
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			setup(h)
			if cmd := h.send(tea.KeyMsg{Type: tea.KeyCtrlC}); !isQuit(cmd) {
				t.Fatal("ctrl+c must quit from here")
			}
			if h.copies != 0 {
				t.Fatal("ctrl+c must never copy")
			}
		})
	}
}

func TestCtrlCDuringStreamingDropsTheRequest(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("partial")
	ctx := h.stub.lastCtx()
	if ctx.Err() != nil {
		t.Fatal("setup: request should be live")
	}
	if cmd := h.send(tea.KeyMsg{Type: tea.KeyCtrlC}); !isQuit(cmd) {
		t.Fatal("ctrl+c should quit")
	}
	if ctx.Err() == nil {
		t.Fatal("quitting must cancel the in-flight request so the model stops generating")
	}
}

// ---- saving ----------------------------------------------------------------------------------------

func TestSaveWritesTheSelectedBlockWithItsLanguageExtension(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	h := newHarness(t)
	h.answerWith("add a health endpoint", multi)

	h.typ("j")
	h.key(tea.KeyCtrlS)
	m := h.model()
	if !m.saving || m.saveInput.Value() != "add-a-health-endpoint.go" || !strings.Contains(m.saveInput.Prompt, "block 1/3 (go)") {
		t.Fatalf("prompt=%q value=%q", m.saveInput.Prompt, m.saveInput.Value())
	}
	h.key(tea.KeyEnter)
	b, err := os.ReadFile(filepath.Join(dir, "add-a-health-endpoint.go"))
	if err != nil || string(b) != blockA {
		t.Fatalf("file should hold only block A: %q err=%v", b, err)
	}
	if !strings.Contains(h.model().status, "saved block 1/3 (go)") {
		t.Fatalf("status=%q", h.model().status)
	}

	h.typ("j")
	h.typ("j")
	h.key(tea.KeyCtrlS)
	if h.model().saveInput.Value() != "add-a-health-endpoint.sh" {
		t.Fatalf("shell block should suggest .sh, got %q", h.model().saveInput.Value())
	}
}

func TestSaveWritesTheWholeAnswerWhenNothingIsSelected(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	h := newHarness(t)
	h.answerWith("save everything", multi)
	h.key(tea.KeyCtrlS)
	if h.model().saveInput.Value() != "save-everything.txt" || !strings.Contains(h.model().saveInput.Prompt, "answer") {
		t.Fatalf("prompt=%q value=%q", h.model().saveInput.Prompt, h.model().saveInput.Value())
	}
	h.key(tea.KeyEnter)
	b, _ := os.ReadFile(filepath.Join(dir, "save-everything.txt"))
	if string(b) != multi {
		t.Fatalf("the whole answer should be saved, got %q", b)
	}
}

func TestSavePromptKeepsWhatWasSelectedWhenItOpened(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	h := newHarness(t)
	h.answerWith("q", multi)
	h.typ("j")
	h.key(tea.KeyCtrlS)
	// typing in the prompt must not change what will be written
	h.typ("x")
	h.key(tea.KeyEnter)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("want one file, got %d", len(entries))
	}
	b, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(b) != blockA {
		t.Fatalf("got %q", b)
	}
}

func TestExtFor(t *testing.T) {
	for lang, want := range map[string]string{
		"go": ".go", "python": ".py", "py": ".py", "sh": ".sh", "bash": ".sh", "ts": ".ts", "rust": ".rs", "sql": ".sql",
		"": ".txt", "klingon": ".txt",
		// a model must not be able to steer the filename
		"../../etc/passwd": ".txt", "go/../x": ".txt", ".exe": ".txt", "go\x00": ".txt",
	} {
		if got := extFor(lang); got != want {
			t.Errorf("extFor(%q) = %q, want %q", lang, got, want)
		}
	}
}

// ---- history entries and untrusted text ------------------------------------------------------------

func TestBlocksWorkOnAnswersOpenedFromHistory(t *testing.T) {
	h := newHarness(t)
	h.store.entries = []Entry{{Question: "old question", Answer: multi, At: time.Now().Add(-time.Hour)}}
	h.m = New(h.stub, h.store)
	h.copyHook()
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	h.key(tea.KeyCtrlH)
	h.key(tea.KeyEnter)
	if h.model().screen != screenAnswer {
		t.Fatal("setup: should have opened the entry")
	}
	h.typ("k") // last block
	h.typ("c")
	if h.copied != blockC {
		t.Fatalf("copied %q", h.copied)
	}
	h.key(tea.KeyEsc) // deselect
	h.key(tea.KeyEsc) // back to history
	if h.model().screen != screenHistory {
		t.Fatal("esc should still return to history")
	}
}

// copyHook reinstalls the harness's fake clipboard after the model was replaced.
func (h *harness) copyHook() {
	m := h.model()
	m.copyFn = func(s string) error { h.copied = s; h.copies++; return nil }
	h.m = m
}

func TestHistoryFromDiskIsSanitized(t *testing.T) {
	h := newHarness(t)
	h.store.entries = []Entry{{Question: "q", Answer: "ok\x1b[2J\x1b]52;c;SGVsbG8=\x07 done", At: time.Now()}}
	h.m = New(h.stub, h.store)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.key(tea.KeyCtrlR)
	m := h.model()
	if strings.ContainsAny(m.raw, "\x1b\x07") || strings.ContainsAny(m.View(), "\x07") {
		t.Fatalf("control characters from a history file reached the screen: %q", m.raw)
	}
}

// ---- the demo content matches the real format -----------------------------------------------------------

func TestDemoAnswersAreInTheFormatThePromptAsksFor(t *testing.T) {
	var single, multiBlock, noCode int
	for _, s := range snippets {
		if n := strings.Count(s.Answer, "\n"+f) + boolToInt(strings.HasPrefix(s.Answer, f)); n%2 != 0 {
			t.Errorf("%q has an unclosed fence", s.Question)
		}
		var blocks int
		for _, seg := range parseAnswer(s.Answer) {
			if seg.selectable() {
				blocks++
			}
			if seg.Code && seg.Info == "" {
				t.Errorf("%q has a block without a language", s.Question)
			}
		}
		switch {
		case blocks == 0:
			noCode++
		case blocks == 1:
			single++
		default:
			multiBlock++
		}
	}
	if single < 6 || multiBlock < 5 || noCode < 1 {
		t.Fatalf("want a mix to try the UI on: %d single-block, %d multi-block, %d no-code", single, multiBlock, noCode)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestFakeResearcherStreamsEveryDemoAnswerIntact(t *testing.T) {
	r := &FakeResearcher{}
	for i, s := range snippets {
		got, err := backend.Collect(r.Research(t.Context(), "q"))
		if err != nil || got != s.Answer {
			t.Fatalf("snippet %d (%q) did not stream back intact (err=%v)", i, s.Question, err)
		}
	}
}

// The fake backend serves answers in order, so what you see in your first few
// questions is what you can try the UI on. If multi-block answers only came at
// the end, you'd have to ask a dozen questions before there was anything to cycle
// through.
func TestMultiBlockAnswersComeEarlyInTheCycle(t *testing.T) {
	blocksIn := func(answer string) (n int) {
		for _, seg := range parseAnswer(answer) {
			if seg.selectable() {
				n++
			}
		}
		return n
	}
	if n := blocksIn(snippets[0].Answer); n < 2 {
		t.Fatalf("the very first demo answer should have several code blocks, has %d", n)
	}
	early := 0
	for _, s := range snippets[:3] {
		if blocksIn(s.Answer) >= 2 {
			early++
		}
	}
	if early < 2 {
		t.Fatalf("at least 2 of the first 3 demo answers should be multi-block, got %d", early)
	}
}

func TestTheLastDemoAnswerIsTheTallScrollTest(t *testing.T) {
	last := snippets[len(snippets)-1]
	// 80, not "just taller than a terminal": the longest multi-block answer is
	// ~60 lines, so a lower bar would let a reshuffle slip through unnoticed.
	if n := strings.Count(last.Answer, "\n"); n < 80 {
		t.Fatalf("the last demo answer should be taller than a terminal (%d lines): %q", n, last.Question)
	}
}

// ---- the walkthrough format: words, code, words, code -------------------------------------

// walkthrough builds an answer shaped the way the prompt asks for: a few
// sentences, a short block, a few sentences, the next block, ending on a block.
// Every step has a unique marker at the start of its explanation and a unique
// last line of code, so a test can tell what is on screen.
func walkthrough(steps, codeLines int) string {
	var parts []string
	for i := 1; i <= steps; i++ {
		parts = append(parts, fmt.Sprintf("STEP%d intro. This is the first of three sentences explaining what step %d does and why. "+
			"Here is the second sentence, which adds a little more detail about the approach taken here. "+
			"The third sentence leads into the code that follows.", i, i))
		parts = append(parts, f+"go")
		for l := 0; l < codeLines; l++ {
			parts = append(parts, fmt.Sprintf("\tstep%d_line%d()", i, l))
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, "\n")
}

func TestWalkthroughParsesAsAlternatingWordsAndCode(t *testing.T) {
	segs := parseAnswer(walkthrough(4, 6))
	if len(segs) != 8 {
		t.Fatalf("want 4 explanations + 4 blocks = 8 segments, got %d", len(segs))
	}
	for i, seg := range segs {
		wantCode := i%2 == 1
		if seg.Code != wantCode {
			t.Fatalf("segment %d: code=%v, want %v (words, code, words, code…)", i, seg.Code, wantCode)
		}
	}
	if !strings.HasPrefix(segs[0].Text, "STEP1 intro") || !segs[len(segs)-1].Code {
		t.Fatal("a walkthrough starts with words and ends on a block")
	}
}

func TestBlocksRememberTheExplanationAboveThem(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", walkthrough(3, 6))
	blocks := h.model().blocks
	if len(blocks) != 3 {
		t.Fatalf("blocks: %d", len(blocks))
	}
	for i, b := range blocks {
		if b.intro >= b.start {
			t.Errorf("block %d: its explanation should start above it (intro=%d start=%d)", i+1, b.intro, b.start)
		}
		// the explanation is a paragraph of a few lines, then a blank line, then the block
		if gap := b.start - b.intro; gap < 3 || gap > 8 {
			t.Errorf("block %d: expected a few lines of explanation, got a gap of %d", i+1, gap)
		}
		if i > 0 && b.intro <= blocks[i-1].end {
			t.Errorf("block %d's explanation (%d) overlaps the previous block (ends %d)", i+1, b.intro, blocks[i-1].end)
		}
	}
}

func TestBlocksWithoutExplanationUseTheirOwnStart(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", f+"go\na()\n"+f+"\n"+f+"go\nb()\n"+f) // two blocks back to back, no words
	for i, b := range h.model().blocks {
		if b.intro != b.start {
			t.Errorf("block %d has no explanation, so intro should equal start (intro=%d start=%d)", i+1, b.intro, b.start)
		}
	}
}

func TestEachStepScrollsIntoViewTogetherWithItsExplanation(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", walkthrough(5, 9)) // taller than the 26-row pane, so it must scroll

	if h.model().pane.TotalLineCount() <= h.model().pane.Height {
		t.Fatal("setup: the answer should need scrolling")
	}
	for i := 1; i <= 5; i++ {
		h.typ("j")
		v := h.model().View()
		intro := fmt.Sprintf("STEP%d intro", i)
		last := fmt.Sprintf("step%d_line8()", i)
		if !strings.Contains(v, intro) || !strings.Contains(v, last) || !strings.Contains(v, "▌") {
			t.Fatalf("step %d: its explanation and its whole block should both be on screen (offset %d):\n%s", i, h.model().pane.YOffset, v)
		}
	}
}

func TestSteppingBackwardAlsoShowsTheExplanation(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", walkthrough(5, 9))
	h.typ("k") // from nothing: the last step
	if v := h.model().View(); !strings.Contains(v, "STEP5 intro") || !strings.Contains(v, "step5_line8()") {
		t.Fatalf("the last step should show its explanation and its block:\n%s", v)
	}
	for i := 4; i >= 1; i-- {
		h.typ("k")
		v := h.model().View()
		if !strings.Contains(v, fmt.Sprintf("STEP%d intro", i)) || !strings.Contains(v, fmt.Sprintf("step%d_line8()", i)) {
			t.Fatalf("step %d going backward: explanation and block should both show:\n%s", i, v)
		}
	}
}

func TestSteppingForwardMovesGentlyNeverBackwardsAndNeverMoreThanAPage(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", walkthrough(6, 9))
	h.typ("g")
	prev := h.model().pane.YOffset
	for i := 1; i <= 6; i++ {
		h.typ("j")
		off := h.model().pane.YOffset
		if off < prev {
			t.Fatalf("step %d: scrolled backwards (%d -> %d) while moving forward", i, prev, off)
		}
		if off-prev > h.model().pane.Height {
			t.Fatalf("step %d: jumped %d lines, more than a page (%d)", i, off-prev, h.model().pane.Height)
		}
		prev = off
	}
}

func TestAnExplanationTooLongToShowWithItsBlockStillShowsTheWholeBlock(t *testing.T) {
	h := newHarness(t)
	var long []string
	for i := 0; i < 30; i++ { // a 30-line explanation, taller than the pane with the block
		long = append(long, fmt.Sprintf("explanation line %d", i))
	}
	h.answerWith("q", strings.Join(long, "\n")+"\n"+f+"go\nfirst()\nsecond()\nthird()\n"+f)
	h.typ("j")
	v := h.model().View()
	if !strings.Contains(v, "first()") || !strings.Contains(v, "third()") || !strings.Contains(v, "explanation line 29") {
		t.Fatalf("the whole block, and the end of its explanation, should show:\n%s", v)
	}
}

func TestFirstStepIsAlreadyInViewWhenTheAnswerStartsAtTheTop(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", walkthrough(3, 6))
	h.typ("g")
	before := h.model().pane.YOffset
	h.typ("j")
	if h.model().pane.YOffset != before {
		t.Fatal("when the first step is already fully on screen, pressing j should highlight it without scrolling")
	}
}

// A step whose explanation plus block exactly fills the pane is the tightest case:
// an off-by-one in the scroll would push the first line of the explanation off
// the top.
func TestAStepThatExactlyFillsThePaneKeepsItsFirstLine(t *testing.T) {
	h := newHarness(t)
	h.answerWith("q", walkthrough(3, 9))
	b := h.model().blocks[1]
	span := b.end - b.intro + 1

	h.send(tea.WindowSizeMsg{Width: 100, Height: span + chrome}) // pane height == span
	if got := h.model().pane.Height; got != span {
		t.Fatalf("setup: pane height %d, want %d", got, span)
	}
	h.typ("g")
	h.typ("j")
	h.typ("j") // step 2, which starts below the top of the view
	v := h.model().View()
	if !strings.Contains(v, "STEP2 intro") || !strings.Contains(v, "step2_line8()") {
		t.Fatalf("a step that exactly fits must show its first line and its last line (offset %d):\n%s", h.model().pane.YOffset, v)
	}
}
