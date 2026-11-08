package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type harness struct {
	t      *testing.T
	m      tea.Model
	store  *memStore
	copied string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, store: &memStore{}}
	m := New(&FakeResearcher{Delay: time.Millisecond}, h.store)
	m.copyFn = func(s string) error { h.copied = s; return nil }
	h.m = m
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

func (h *harness) send(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	h.m, cmd = h.m.Update(msg)
	return cmd
}
func (h *harness) typ(s string)      { h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }
func (h *harness) key(k tea.KeyType) { h.send(tea.KeyMsg{Type: k}) }
func (h *harness) model() Model      { return h.m.(Model) }

// ask submits a question and delivers the fake backend's answer.
func (h *harness) ask(q string) {
	h.typ(q)
	h.key(tea.KeyEnter)
	m := h.model()
	if m.screen != screenAnswer || !m.loading {
		h.t.Fatalf("after enter: screen=%v loading=%v", m.screen, m.loading)
	}
	a := snippets[len(snippets)-1].Code // the long one, so the pane scrolls
	h.send(resultMsg{id: m.reqID, question: q, answer: a})
}

func TestAskFlowAndBackToEmptyBox(t *testing.T) {
	h := newHarness(t)
	h.ask("how do I log")

	m := h.model()
	if m.loading || m.screen != screenAnswer || m.raw == "" {
		t.Fatalf("answer not shown: %+v", m.screen)
	}
	if len(h.store.entries) != 1 {
		t.Fatal("answer was not persisted")
	}
	if m.pane.TotalLineCount() <= m.pane.Height {
		t.Fatal("expected scrollable content")
	}

	h.key(tea.KeyDown)
	h.key(tea.KeyPgDown)
	if h.model().pane.YOffset == 0 {
		t.Fatal("pane did not scroll")
	}

	h.key(tea.KeyEsc)
	m = h.model()
	if m.screen != screenAsk || m.input.Value() != "" {
		t.Fatal("esc should return to an empty ask box")
	}
}

func TestCtrlRJumpsToLastResponse(t *testing.T) {
	h := newHarness(t)
	h.key(tea.KeyCtrlR)
	if h.model().screen != screenAsk || h.model().status == "" {
		t.Fatal("ctrl+r with no history should stay put and say so")
	}
	h.ask("first")
	h.key(tea.KeyEsc)
	h.ask("second")
	h.key(tea.KeyEsc)
	h.key(tea.KeyCtrlR)
	m := h.model()
	if m.screen != screenAnswer || m.paneTitle != "second" {
		t.Fatalf("want last answer, got screen=%v title=%q", m.screen, m.paneTitle)
	}
}

func TestHistoryListAndDetail(t *testing.T) {
	h := newHarness(t)
	h.ask("alpha question")
	h.key(tea.KeyEsc)
	h.ask("beta question")
	h.key(tea.KeyEsc)

	h.key(tea.KeyCtrlH)
	m := h.model()
	if m.screen != screenHistory || len(m.hist.list.Items()) != 2 {
		t.Fatalf("history not shown: %v / %d items", m.screen, len(m.hist.list.Items()))
	}
	if first := m.hist.list.Items()[0].(historyItem); first.entry.Question != "beta question" {
		t.Fatal("history should be newest first")
	}
	view := m.View()
	if !strings.Contains(view, "beta question") || !strings.Contains(view, time.Now().Format("2006-01-02")) {
		t.Fatalf("history view missing question/date:\n%s", view)
	}

	h.key(tea.KeyDown)
	h.key(tea.KeyEnter) // open "alpha question"
	m = h.model()
	if m.screen != screenAnswer || m.paneTitle != "alpha question" || !m.fromHistory {
		t.Fatalf("detail not opened: %v %q", m.screen, m.paneTitle)
	}
	h.key(tea.KeyEsc)
	if h.model().screen != screenHistory {
		t.Fatal("esc from detail should return to history")
	}
	h.key(tea.KeyEsc)
	if h.model().screen != screenAsk {
		t.Fatal("esc from history should return to ask")
	}
}

func TestHistorySearch(t *testing.T) {
	h := newHarness(t)
	h.ask("alpha question")
	h.key(tea.KeyEsc)
	h.ask("beta question")
	h.key(tea.KeyEsc)
	h.key(tea.KeyCtrlH)

	h.typ("/")
	for _, r := range "alpha" {
		h.deliver(h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}))
	}
	h.key(tea.KeyEnter) // apply filter
	vis := h.model().hist.list.VisibleItems()
	if len(vis) != 1 || vis[0].(historyItem).entry.Question != "alpha question" {
		t.Fatalf("filter failed: %d visible", len(vis))
	}
}

func TestCopyWholePane(t *testing.T) {
	h := newHarness(t)
	h.ask("copy me")
	h.send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if h.copied != h.model().raw || h.copied == "" {
		t.Fatal("ctrl+c should copy the raw, unwrapped answer")
	}
	if h.model().screen != screenAnswer {
		t.Fatal("ctrl+c on the pane must copy, not quit")
	}
}

func TestSaveToFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	h := newHarness(t)
	h.ask("save me")

	h.key(tea.KeyCtrlS)
	if !h.model().saving || h.model().saveInput.Value() != "save-me.txt" {
		t.Fatalf("expected filename prompt prefilled, got %q", h.model().saveInput.Value())
	}
	h.key(tea.KeyEnter)
	b, err := os.ReadFile(filepath.Join(dir, "save-me.txt"))
	if err != nil || string(b) != h.model().raw {
		t.Fatalf("file not written correctly: %v", err)
	}

	// Saving again must not clobber the existing file.
	h.key(tea.KeyCtrlS)
	h.key(tea.KeyEnter)
	if !strings.Contains(h.model().status, "already exists") {
		t.Fatalf("expected overwrite refusal, status=%q", h.model().status)
	}

	h.key(tea.KeyCtrlS)
	h.key(tea.KeyEsc)
	if h.model().saving {
		t.Fatal("esc should cancel the save prompt")
	}
}

func TestCancelIgnoresStaleResult(t *testing.T) {
	h := newHarness(t)
	h.typ("slow")
	h.key(tea.KeyEnter)
	id := h.model().reqID
	h.key(tea.KeyEsc) // cancel while loading
	if h.model().screen != screenAsk || h.model().loading {
		t.Fatal("esc should cancel and return to ask")
	}
	h.send(resultMsg{id: id, question: "slow", answer: "late"})
	if len(h.store.entries) != 0 || h.model().screen != screenAsk {
		t.Fatal("stale result must be ignored")
	}
}

func TestErrorIsShownNotStored(t *testing.T) {
	h := newHarness(t)
	h.typ("fail")
	h.key(tea.KeyEnter)
	_, err := (&FakeResearcher{}).Research(nil2(), "fail")
	h.send(resultMsg{id: h.model().reqID, question: "fail", err: err})
	if h.model().paneErr == "" || len(h.store.entries) != 0 {
		t.Fatal("errors should show in the pane and not enter history")
	}
}

func TestJSONStoreRoundTrip(t *testing.T) {
	s := JSONStore{Path: filepath.Join(t.TempDir(), "sub", "history.json")}
	if got, err := s.Load(); err != nil || got != nil {
		t.Fatalf("missing file should load empty: %v %v", got, err)
	}
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, q := range []string{"a", "b"} {
		if err := s.Append(Entry{Question: q, Answer: q + "!", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load()
	if err != nil || len(got) != 2 || got[1].Answer != "b!" || !got[0].At.Equal(at) {
		t.Fatalf("round trip failed: %+v %v", got, err)
	}
}

func nil2() context.Context { return context.Background() }

// deliver runs a returned command and feeds list-filter results back into the
// model. The list computes search matches asynchronously via a tea.Cmd. Other
// commands (cursor blink, spinner ticks) sleep, so they run with a short timeout
// and are dropped.
func (h *harness) deliver(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				h.deliver(c)
			}
			return
		}
		if _, ok := msg.(list.FilterMatchesMsg); ok {
			h.send(msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFakeResearcherCyclesAndWraps(t *testing.T) {
	r := &FakeResearcher{}
	seen := map[string]bool{}
	var first, afterWrap string
	for i := 0; i <= len(snippets); i++ {
		a, err := r.Research(context.Background(), "q")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = a
		}
		if i == len(snippets) {
			afterWrap = a
		}
		seen[a] = true
	}
	if len(seen) != len(snippets) {
		t.Fatalf("want %d distinct responses, got %d", len(snippets), len(seen))
	}
	if first != afterWrap {
		t.Fatal("cycle should wrap back to the first snippet")
	}
	// "fail" errors without consuming a snippet.
	if _, err := r.Research(context.Background(), "please fail"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDemoEntries(t *testing.T) {
	now := time.Now()
	es := DemoEntries(now)
	if len(es) < 10 {
		t.Fatalf("want a good sample, got %d", len(es))
	}
	for i, e := range es {
		if e.Question == "" || e.Answer == "" || !e.At.Before(now) {
			t.Fatalf("bad entry %d: %+v", i, e)
		}
		if i > 0 && !e.At.After(es[i-1].At) {
			t.Fatalf("entries must be oldest-first with increasing time (at %d)", i)
		}
	}
}

func TestDemoHistoryShowsInUI(t *testing.T) {
	h := newHarness(t)
	h.store.entries = DemoEntries(time.Now())
	m := New(&FakeResearcher{}, h.store)
	h.m = m
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.key(tea.KeyCtrlH)
	if got := len(h.model().hist.list.Items()); got != len(snippets) {
		t.Fatalf("history shows %d items, want %d", got, len(snippets))
	}
	h.key(tea.KeyEsc)
	h.key(tea.KeyCtrlR) // last response comes from seeded history
	if h.model().screen != screenAnswer {
		t.Fatal("ctrl+r should open the newest seeded entry")
	}
}
