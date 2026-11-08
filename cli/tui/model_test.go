package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codie/backend"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// ---- harness ---------------------------------------------------------------

// stubResearcher records what it was asked and hands back a channel the test
// never writes to: tests inject events with emit() instead, so they control
// exactly what arrives and when.
type stubResearcher struct {
	mu   sync.Mutex
	qs   []string
	ctxs []context.Context
}

func (s *stubResearcher) Research(ctx context.Context, q string) <-chan backend.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qs = append(s.qs, q)
	s.ctxs = append(s.ctxs, ctx)
	return make(chan backend.Event)
}

func (s *stubResearcher) lastCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctxs[len(s.ctxs)-1]
}

type harness struct {
	t      *testing.T
	m      tea.Model
	store  *memStore
	stub   *stubResearcher
	copied string
	copies int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	stub := &stubResearcher{}
	return newHarnessWith(t, stub, stub)
}

func newHarnessWith(t *testing.T, r backend.Researcher, stub *stubResearcher) *harness {
	t.Helper()
	h := &harness{t: t, store: &memStore{}, stub: stub}
	m := New(r, h.store)
	m.copyFn = func(s string) error { h.copied = s; h.copies++; return nil }
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

// emit delivers a backend event for the current request.
func (h *harness) emit(ev backend.Event) {
	h.send(eventMsg{id: h.model().reqID, ev: ev, ok: true})
}
func (h *harness) text(s string) { h.emit(backend.Event{Kind: backend.Text, Text: s}) }
func (h *harness) done()         { h.emit(backend.Event{Kind: backend.Done}) }
func (h *harness) fail(msg string) {
	h.emit(backend.Event{Kind: backend.Error, Err: fmt.Errorf("%s", msg)})
}

// start types a question and presses enter, leaving the request in flight.
func (h *harness) start(q string) {
	h.t.Helper()
	h.typ(q)
	h.key(tea.KeyEnter)
	if m := h.model(); m.screen != screenAnswer || !m.loading {
		h.t.Fatalf("after enter: screen=%v loading=%v", m.screen, m.loading)
	}
}

// tall is a long answer with no code blocks, so the pane has plain text to
// scroll (with blocks present, up/down select blocks instead).
var tall = func() string {
	var b strings.Builder
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&b, "line %d of a long plain answer\n", i)
	}
	return strings.TrimRight(b.String(), "\n")
}()

// ask runs a whole question to completion with the tall answer.
func (h *harness) ask(q string) {
	h.t.Helper()
	h.start(q)
	h.text(tall)
	h.done()
}

// pump drives a real stream into the model: it waits for each event from the
// model's own channel and feeds it to Update, until the request ends.
func (h *harness) pump() {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for h.model().loading {
		m := h.model()
		got := make(chan tea.Msg, 1)
		go func() { got <- waitEvent(m.reqID, m.events)() }()
		select {
		case msg := <-got:
			h.send(msg)
		case <-deadline:
			h.t.Fatal("the stream never finished")
		}
	}
}

// step delivers exactly one event from a real stream.
func (h *harness) step() {
	h.t.Helper()
	m := h.model()
	got := make(chan tea.Msg, 1)
	go func() { got <- waitEvent(m.reqID, m.events)() }()
	select {
	case msg := <-got:
		h.send(msg)
	case <-time.After(5 * time.Second):
		h.t.Fatal("no event arrived")
	}
}

// ---- asking, answering, going back -----------------------------------------

func TestAskFlowAndBackToEmptyBox(t *testing.T) {
	h := newHarness(t)
	h.ask("how do I log")

	m := h.model()
	if m.loading || m.screen != screenAnswer || m.raw != tall {
		t.Fatalf("answer not shown: screen=%v loading=%v", m.screen, m.loading)
	}
	if len(h.store.entries) != 1 || h.store.entries[0].Question != "how do I log" || h.store.entries[0].Answer != tall {
		t.Fatalf("answer was not persisted correctly: %+v", h.store.entries)
	}
	if m.pane.TotalLineCount() <= m.pane.Height {
		t.Fatal("expected scrollable content")
	}
	if got := h.stub.qs; len(got) != 1 || got[0] != "how do I log" {
		t.Fatalf("backend was asked %q", got)
	}

	h.key(tea.KeyUp)
	h.key(tea.KeyPgUp)
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

func TestBlankQuestionIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.typ("   ")
	h.key(tea.KeyEnter)
	if h.model().screen != screenAsk || len(h.stub.qs) != 0 {
		t.Fatal("a blank question must not be sent")
	}
}

func TestQuestionIsTrimmedBeforeSending(t *testing.T) {
	h := newHarness(t)
	h.start("  padded  ")
	if h.stub.qs[0] != "padded" {
		t.Fatalf("sent %q", h.stub.qs[0])
	}
}

func TestLabelShownOnAskScreen(t *testing.T) {
	h := newHarness(t)
	h.m = h.model().WithLabel("OpenRouter · some/model")
	if v := h.model().View(); !strings.Contains(v, "OpenRouter · some/model") {
		t.Fatalf("label missing from ask screen:\n%s", v)
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

// ---- streaming -------------------------------------------------------------

func TestSpinnerShownBeforeFirstChunk(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	if v := h.model().View(); !strings.Contains(v, "researching…") || !strings.Contains(v, "esc cancel") {
		t.Fatalf("expected a waiting indicator:\n%s", v)
	}
}

func TestStreamingAppendsAndFollowsBottom(t *testing.T) {
	h := newHarness(t)
	h.start("q")

	var want strings.Builder
	for i := 1; i <= 80; i++ {
		line := fmt.Sprintf("line %d\n", i)
		want.WriteString(line)
		h.text(line)
		if got := h.model().raw; got != want.String() {
			t.Fatalf("after chunk %d raw = %q", i, got)
		}
	}
	m := h.model()
	if !m.loading {
		t.Fatal("still streaming until Done")
	}
	if !m.pane.AtBottom() || m.pane.YOffset == 0 {
		t.Fatalf("pane should follow the newest text: atBottom=%v offset=%d", m.pane.AtBottom(), m.pane.YOffset)
	}
	if !strings.Contains(m.View(), "line 80") {
		t.Fatal("newest line should be visible")
	}
	h.done()
	if h.model().loading || len(h.store.entries) != 1 {
		t.Fatal("Done should finish and store the answer")
	}
}

func TestScrollingUpStopsFollowing(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	for i := 1; i <= 80; i++ {
		h.text(fmt.Sprintf("line %d\n", i))
	}
	h.key(tea.KeyPgUp)
	offset := h.model().pane.YOffset
	if h.model().pane.AtBottom() {
		t.Fatal("test setup: should have scrolled off the bottom")
	}
	h.text("one more line\n")
	if got := h.model().pane.YOffset; got != offset {
		t.Fatalf("the view jumped while the user was reading: %d -> %d", offset, got)
	}
}

func TestPartialAnswerVisibleWhileStreaming(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("first part")
	v := h.model().View()
	if !strings.Contains(v, "first part") || !strings.Contains(v, "esc cancel") {
		t.Fatalf("partial text and cancel hint should both show:\n%s", v)
	}
}

func TestActivityShownWhileWorking(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.emit(backend.Event{Kind: backend.Activity, Text: "reading main.go"})
	if v := h.model().View(); !strings.Contains(v, "reading main.go") {
		t.Fatalf("activity missing before any text:\n%s", v)
	}
	h.text("code")
	if v := h.model().View(); !strings.Contains(v, "reading main.go") {
		t.Fatalf("activity should stay in the footer while streaming:\n%s", v)
	}
	h.done()
	if v := h.model().View(); strings.Contains(v, "reading main.go") {
		t.Fatalf("activity should clear when finished:\n%s", v)
	}
}

func TestCopyAndSaveAreBlockedWhileStreaming(t *testing.T) {
	t.Chdir(t.TempDir())
	h := newHarness(t)
	h.start("q")
	h.text("partial")

	h.typ("c")
	if h.copies != 0 || !strings.Contains(h.model().status, "still answering") {
		t.Fatalf("copy must wait: copies=%d status=%q", h.copies, h.model().status)
	}
	h.key(tea.KeyCtrlS)
	if h.model().saving || !strings.Contains(h.model().status, "still answering") {
		t.Fatalf("save must wait: saving=%v status=%q", h.model().saving, h.model().status)
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Fatal("nothing should have been written")
	}
}

func TestEventsAfterDoneAreIgnored(t *testing.T) {
	h := newHarness(t)
	h.ask("q")
	before := h.model().raw
	h.text("late text")
	h.done()
	h.fail("late error")
	m := h.model()
	if m.raw != before || m.paneErr != "" || len(h.store.entries) != 1 {
		t.Fatalf("events after Done must be ignored: raw changed=%v err=%q stored=%d", m.raw != before, m.paneErr, len(h.store.entries))
	}
}

// ---- failures ----------------------------------------------------------------

func TestErrorBeforeAnyTextIsShownNotStored(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.fail("OpenRouter rejected your API key (401)")
	m := h.model()
	if m.loading || m.paneErr == "" || len(h.store.entries) != 0 {
		t.Fatalf("loading=%v err=%q stored=%d", m.loading, m.paneErr, len(h.store.entries))
	}
	if v := m.View(); !strings.Contains(v, "rejected your API key") {
		t.Fatalf("error not shown:\n%s", v)
	}
}

func TestMidStreamErrorKeepsPartialAndDoesNotStore(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("func half() {\n")
	h.fail("connection lost")

	m := h.model()
	v := m.View()
	if !strings.Contains(v, "func half()") || !strings.Contains(v, "connection lost") {
		t.Fatalf("partial answer and error should both be visible:\n%s", v)
	}
	if len(h.store.entries) != 0 {
		t.Fatal("an incomplete answer must not enter history")
	}
	h.typ("c")
	if h.copies != 0 || !strings.Contains(h.model().status, "incomplete") {
		t.Fatalf("an incomplete answer must not be copied: status=%q", h.model().status)
	}
	h.key(tea.KeyCtrlS)
	if h.model().saving {
		t.Fatal("an incomplete answer must not be saved")
	}
}

func TestEmptyAnswerIsAnError(t *testing.T) {
	for name, body := range map[string]string{"nothing": "", "whitespace": " \n\t "} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.start("q")
			if body != "" {
				h.text(body)
			}
			h.done()
			if h.model().paneErr == "" || len(h.store.entries) != 0 {
				t.Fatalf("empty answers are failures: err=%q stored=%d", h.model().paneErr, len(h.store.entries))
			}
		})
	}
}

func TestStreamClosedWithoutResultIsAnError(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("partial")
	h.send(eventMsg{id: h.model().reqID, ok: false})
	m := h.model()
	if m.loading || !strings.Contains(m.paneErr, "connection closed") || len(h.store.entries) != 0 {
		t.Fatalf("loading=%v err=%q stored=%d", m.loading, m.paneErr, len(h.store.entries))
	}
}

// ---- cancelling --------------------------------------------------------------

func TestEscDuringStreamCancelsTheRequest(t *testing.T) {
	h := newHarness(t)
	h.start("q")
	h.text("partial")
	ctx := h.stub.lastCtx()
	if ctx.Err() != nil {
		t.Fatal("test setup: request should still be live")
	}

	h.key(tea.KeyEsc)
	if ctx.Err() == nil {
		t.Fatal("esc must cancel the request's context so the HTTP call is dropped")
	}
	m := h.model()
	if m.screen != screenAsk || m.loading {
		t.Fatalf("esc should return to the ask screen: screen=%v loading=%v", m.screen, m.loading)
	}

	h.send(eventMsg{id: m.reqID - 1, ev: backend.Event{Kind: backend.Text, Text: "late"}, ok: true})
	h.send(eventMsg{id: m.reqID - 1, ev: backend.Event{Kind: backend.Done}, ok: true})
	if len(h.store.entries) != 0 || h.model().screen != screenAsk {
		t.Fatal("events from a cancelled request must be ignored")
	}
}

func TestNewQuestionAfterCancelIgnoresOldEvents(t *testing.T) {
	h := newHarness(t)
	h.start("first")
	oldID := h.model().reqID
	h.key(tea.KeyEsc)
	h.start("second")

	h.send(eventMsg{id: oldID, ev: backend.Event{Kind: backend.Text, Text: "STALE"}, ok: true})
	h.text("fresh answer")
	h.done()
	m := h.model()
	if strings.Contains(m.raw, "STALE") || m.raw != "fresh answer" {
		t.Fatalf("old request leaked into the new answer: %q", m.raw)
	}
	if len(h.store.entries) != 1 || h.store.entries[0].Question != "second" {
		t.Fatalf("stored: %+v", h.store.entries)
	}
}

func TestSpinnerStopsWhenFinished(t *testing.T) {
	h := newHarness(t)
	h.ask("q")
	if cmd := h.send(h.model().spin.Tick()); cmd != nil {
		t.Fatal("spinner should stop ticking once the answer is complete")
	}
}

// ---- history -----------------------------------------------------------------

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

// ---- copy and save -------------------------------------------------------------

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
	if err != nil || string(b) != tall {
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

// ---- the fake backend ------------------------------------------------------------

func TestFakeResearcherCyclesAndWraps(t *testing.T) {
	r := &FakeResearcher{}
	seen := map[string]bool{}
	var first, afterWrap string
	for i := 0; i <= len(snippets); i++ {
		a, err := backend.Collect(r.Research(context.Background(), "q"))
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
}

func TestFakeResearcherStreamsInChunksAndReassembles(t *testing.T) {
	r := &FakeResearcher{}
	var chunks int
	var b strings.Builder
	for ev := range r.Research(context.Background(), "q") {
		if ev.Kind == backend.Text {
			chunks++
			b.WriteString(ev.Text)
		}
	}
	if chunks < 5 {
		t.Fatalf("expected a multi-chunk stream, got %d chunk(s)", chunks)
	}
	if b.String() != snippets[0].Answer {
		t.Fatal("chunks must reassemble to the original snippet exactly")
	}
}

func TestFakeResearcherFailureModes(t *testing.T) {
	r := &FakeResearcher{}

	evs := collectEvents(r.Research(context.Background(), "please fail"))
	if len(evs) != 1 || evs[0].Kind != backend.Error {
		t.Fatalf("'fail' should error before any text: %+v", evs)
	}

	evs = collectEvents(r.Research(context.Background(), "midfail"))
	if len(evs) < 2 || evs[len(evs)-1].Kind != backend.Error || evs[0].Kind != backend.Text {
		t.Fatalf("'midfail' should stream some text then error: %d events", len(evs))
	}

	// Neither failure consumed a snippet.
	got, _ := backend.Collect(r.Research(context.Background(), "ok"))
	if got != snippets[0].Answer {
		t.Fatal("failed requests must not advance the cycle")
	}
}

func collectEvents(ch <-chan backend.Event) []backend.Event {
	var evs []backend.Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

func TestFakeResearcherCancelClosesTheChannel(t *testing.T) {
	r := &FakeResearcher{Delay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	ch := r.Research(ctx, "q")
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected the channel to close without sending")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not stop the fake backend")
	}
}

func TestFakeResearcherCancelWhileNobodyReads(t *testing.T) {
	r := &FakeResearcher{}
	ctx, cancel := context.WithCancel(context.Background())
	ch := r.Research(ctx, "q") // goroutine blocks on its first send
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-ch: // closed (or one buffered-looking value): either way it must unblock
	case <-time.After(2 * time.Second):
		t.Fatal("producer stayed blocked after cancel")
	}
}

// ---- demo history ------------------------------------------------------------------

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
	m := New(h.stub, h.store)
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

// ---- end to end: real OpenRouter client -> real model, fake server --------------------
//
// These wire the actual backend.OpenRouter client into the actual UI model and
// point it at a local server that speaks OpenRouter's protocol. They prove the
// pieces fit together, with no network and no real key.

const e2eKey = "sk-or-test-SECRET-KEY-1234"

func e2eServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body) // as a real server does; lets net/http detect disconnects
		r.Body = io.NopCloser(bytes.NewReader(b))
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func e2eHarness(t *testing.T, srv *httptest.Server) *harness {
	t.Helper()
	o, err := backend.NewOpenRouter(backend.Config{APIKey: e2eKey, Model: "test/model", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return newHarnessWith(t, o, nil)
}

func e2eSSE(w http.ResponseWriter, payloads ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, p := range payloads {
		fmt.Fprintf(w, "data: %s\n\n", p)
	}
	w.(http.Flusher).Flush()
}

func e2eDelta(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%s}}]}`, b)
}

const e2eStop = `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`

func TestEndToEndStreamIntoPane(t *testing.T) {
	var asked string
	srv := e2eServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		asked = req.Messages[len(req.Messages)-1].Content
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, p := range []string{e2eDelta("```go\n"), e2eDelta("fmt.Println(\"hi\")\n"), e2eDelta("```"), e2eStop, "[DONE]"} {
			fmt.Fprintf(w, "data: %s\n\n", p)
			f.Flush()
			time.Sleep(5 * time.Millisecond)
		}
	})
	h := e2eHarness(t, srv)
	h.start("  say hi in go  ")

	h.step() // first chunk only
	if got := h.model().raw; got != "```go\n" || !h.model().loading {
		t.Fatalf("first chunk should appear while still streaming: raw=%q loading=%v", got, h.model().loading)
	}

	h.pump()
	m := h.model()
	want := "```go\nfmt.Println(\"hi\")\n```"
	if m.raw != want || m.loading || m.paneErr != "" {
		t.Fatalf("raw=%q loading=%v err=%q", m.raw, m.loading, m.paneErr)
	}
	if asked != "say hi in go" {
		t.Fatalf("server received question %q", asked)
	}
	if len(h.store.entries) != 1 || h.store.entries[0].Answer != want || h.store.entries[0].Question != "say hi in go" {
		t.Fatalf("history: %+v", h.store.entries)
	}
	if !strings.Contains(m.View(), "fmt.Println") {
		t.Fatal("answer not rendered")
	}
	h.typ("j") // select the one code block
	h.typ("c")
	if h.copied != "fmt.Println(\"hi\")" {
		t.Fatalf("copied %q: only the block's code, without fences, should be copied", h.copied)
	}
}

func TestEndToEndBadKeyShowsAClearErrorAndNeverTheKey(t *testing.T) {
	srv := e2eServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"invalid key %s"}}`, e2eKey)
	})
	h := e2eHarness(t, srv)
	h.start("q")
	h.pump()

	m := h.model()
	view := m.View()
	if !strings.Contains(view, "rejected your API key") || !strings.Contains(view, "OPENROUTER_API_KEY") {
		t.Fatalf("error should say what to do:\n%s", view)
	}
	if strings.Contains(view, e2eKey) || strings.Contains(m.paneErr, e2eKey) || strings.Contains(m.status, e2eKey) {
		t.Fatal("the API key leaked into the UI")
	}
	if len(h.store.entries) != 0 {
		t.Fatal("a failed request must not be stored")
	}
}

func TestEndToEndMidStreamFailure(t *testing.T) {
	srv := e2eServer(t, func(w http.ResponseWriter, r *http.Request) {
		e2eSSE(w, e2eDelta("func half() {\n"), `{"error":{"code":502,"message":"provider fell over"}}`)
	})
	h := e2eHarness(t, srv)
	h.start("q")
	h.pump()

	m := h.model()
	v := m.View()
	if !strings.Contains(v, "func half()") || !strings.Contains(v, "provider fell over") {
		t.Fatalf("partial answer and reason should show:\n%s", v)
	}
	if len(h.store.entries) != 0 {
		t.Fatal("a broken answer must not be stored")
	}
}

func TestEndToEndTruncatedAnswerIsNotStored(t *testing.T) {
	srv := e2eServer(t, func(w http.ResponseWriter, r *http.Request) {
		e2eSSE(w, e2eDelta("func cut"), `{"choices":[{"delta":{"content":""},"finish_reason":"length"}]}`, "[DONE]")
	})
	h := e2eHarness(t, srv)
	h.start("q")
	h.pump()
	if !strings.Contains(h.model().paneErr, "length limit") || len(h.store.entries) != 0 {
		t.Fatalf("err=%q stored=%d", h.model().paneErr, len(h.store.entries))
	}
}

func TestEndToEndEscDropsTheHTTPConnection(t *testing.T) {
	disconnected := make(chan struct{})
	srv := e2eServer(t, func(w http.ResponseWriter, r *http.Request) {
		e2eSSE(w, e2eDelta("starting…"))
		<-r.Context().Done()
		close(disconnected)
	})
	h := e2eHarness(t, srv)
	h.start("q")
	h.step()
	if h.model().raw != "starting…" {
		t.Fatalf("raw=%q", h.model().raw)
	}

	h.key(tea.KeyEsc)
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("esc did not drop the connection to OpenRouter (it would keep generating and billing)")
	}
	if h.model().screen != screenAsk || len(h.store.entries) != 0 {
		t.Fatal("cancel should return to the ask screen and store nothing")
	}
}

func TestEndToEndSecondQuestionAfterFailureWorks(t *testing.T) {
	var calls int
	srv := e2eServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		e2eSSE(w, e2eDelta("fine now"), e2eStop, "[DONE]")
	})
	h := e2eHarness(t, srv)
	h.start("one")
	h.pump()
	if !strings.Contains(h.model().paneErr, "rate limiting") {
		t.Fatalf("err=%q", h.model().paneErr)
	}
	h.key(tea.KeyEsc)
	h.start("two")
	h.pump()
	if h.model().raw != "fine now" || h.model().paneErr != "" || len(h.store.entries) != 1 {
		t.Fatalf("recovery failed: raw=%q err=%q stored=%d", h.model().raw, h.model().paneErr, len(h.store.entries))
	}
}
