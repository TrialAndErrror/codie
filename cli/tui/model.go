// Package tui holds the Bubble Tea model for codie's interactive mode.
//
// Screens:
//
//	ask      empty text box                  enter: ask • ctrl+r: last answer • ctrl+h: history • esc: quit
//	answer   scrollable response pane        esc: back • ctrl+c: copy • ctrl+s: save
//	history  searchable list of past asks    enter: open • /: search • esc: back
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codie/backend"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenAsk screen = iota
	screenAnswer
	screenHistory
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("212")).Padding(0, 1)
)

// eventMsg carries one backend event into Update. id identifies the request it
// belongs to, so events from a request the user already cancelled are ignored.
// ok is false when the stream closed without a terminal event.
type eventMsg struct {
	id int
	ev backend.Event
	ok bool
}

// Model is the root TUI state. It owns every screen and routes events to the
// active one.
type Model struct {
	researcher backend.Researcher
	store      Store
	copyFn     func(string) error // injectable so tests don't touch the clipboard

	screen screen
	w, h   int
	status string // one-line feedback ("copied", "saved to x.txt", errors)
	label  string // which backend is in use, shown on the ask screen

	// ask screen
	input textinput.Model

	// answer screen
	pane        viewport.Model
	spin        spinner.Model
	paneTitle   string // the question being shown
	raw         string // the answer exactly as generated (what copy/save use); partial while streaming
	paneErr     string // set when the request failed; raw then holds whatever arrived before the failure
	loading     bool   // a request is in flight (streaming)
	activity    string // latest status from the backend, e.g. "reading main.go"
	events      <-chan backend.Event
	cancel      context.CancelFunc
	reqID       int
	fromHistory bool // esc returns to history instead of the ask screen
	saving      bool
	saveInput   textinput.Model

	// history + "last response"
	entries []Entry // oldest first, as stored
	hist    historyView
}

// New returns a Model ready to be passed to tea.NewProgram. Existing history is
// loaded from the store; a load failure is reported in the status line.
func New(r backend.Researcher, s Store) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask a question…"
	ti.Prompt = "› "
	ti.CharLimit = 500
	ti.Focus()

	si := textinput.New()
	si.Prompt = "Save as: "
	si.CharLimit = 200

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

	m := Model{
		researcher: r,
		store:      s,
		copyFn:     copyToClipboard,
		input:      ti,
		saveInput:  si,
		spin:       sp,
		pane:       viewport.New(80, 20),
		hist:       newHistoryView(),
	}
	entries, err := s.Load()
	if err != nil {
		m.status = "could not load history: " + err.Error()
	}
	m.entries = entries
	return m
}

// WithLabel sets the text shown under the title on the ask screen, so the user
// always knows whether answers are real or samples.
func (m Model) WithLabel(label string) Model {
	m.label = label
	return m
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

// waitEvent returns a tea.Cmd that blocks (in Bubble Tea's own goroutine) until
// the next event arrives, then delivers it to Update. Update re-issues it after
// each non-terminal event, which is how a stream becomes a series of messages.
func waitEvent(id int, ch <-chan backend.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		return eventMsg{id: id, ev: ev, ok: ok}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil

	case eventMsg:
		return m.handleEvent(msg)

	case spinner.TickMsg:
		if !m.loading {
			return m, nil // stop ticking once the request is done
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		m.status = ""
		switch m.screen {
		case screenAsk:
			return m.updateAsk(msg)
		case screenAnswer:
			return m.updateAnswer(msg)
		case screenHistory:
			return m.updateHistory(msg)
		}
	}

	// Non-key messages (cursor blink, list internals) go to the active widget.
	return m.forward(msg)
}

// forward hands a message to whichever widget is active.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenAsk:
		m.input, cmd = m.input.Update(msg)
	case screenAnswer:
		if m.saving {
			m.saveInput, cmd = m.saveInput.Update(msg)
		} else {
			m.pane, cmd = m.pane.Update(msg)
		}
	case screenHistory:
		m.hist.list, cmd = m.hist.list.Update(msg)
	}
	return m, cmd
}

// ---- ask screen -----------------------------------------------------------

func (m Model) updateAsk(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit

	case "enter":
		q := strings.TrimSpace(m.input.Value())
		if q == "" {
			return m, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.reqID++
		m.cancel, m.loading, m.activity = cancel, true, ""
		m.input.Reset()
		m.showPane(q, "", "")
		m.fromHistory = false
		m.screen = screenAnswer
		m.events = m.researcher.Research(ctx, q)
		return m, tea.Batch(m.spin.Tick, waitEvent(m.reqID, m.events))

	case "ctrl+r":
		if len(m.entries) == 0 {
			m.status = "no previous response yet"
			return m, nil
		}
		last := m.entries[len(m.entries)-1]
		m.openEntry(last, false)
		return m, nil

	case "ctrl+h":
		m.hist.setEntries(m.entries)
		m.layout()
		m.screen = screenHistory
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// ---- answer screen --------------------------------------------------------

func (m Model) updateAnswer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.saving {
		return m.updateSaving(msg)
	}

	switch msg.String() {
	case "esc":
		if m.loading {
			m.cancelRequest()
		}
		m.screen = screenAsk
		if m.fromHistory {
			m.screen = screenHistory
		}
		return m, nil

	case "ctrl+c":
		if why := m.exportBlocked("copy"); why != "" {
			m.status = why
			return m, nil
		}
		if err := m.copyFn(m.raw); err != nil {
			m.status = "copy failed: " + err.Error()
		} else {
			m.status = "copied response to clipboard"
		}
		return m, nil

	case "ctrl+s":
		if why := m.exportBlocked("save"); why != "" {
			m.status = why
			return m, nil
		}
		m.saving = true
		m.saveInput.SetValue(slug(m.paneTitle) + ".txt")
		m.saveInput.CursorEnd()
		m.saveInput.Focus()
		return m, textinput.Blink
	}

	// Everything else (arrows, pgup/pgdn, j/k, ...) scrolls the viewport.
	var cmd tea.Cmd
	m.pane, cmd = m.pane.Update(msg)
	return m, cmd
}

func (m Model) updateSaving(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.saving = false
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.saveInput.Value())
		if name == "" {
			return m, nil
		}
		if err := saveFile(name, m.raw); err != nil {
			m.status = errorStyle.Render("save failed: " + err.Error())
		} else {
			m.status = "saved to " + name
		}
		m.saving = false
		return m, nil
	}
	var cmd tea.Cmd
	m.saveInput, cmd = m.saveInput.Update(msg)
	return m, cmd
}

// exportBlocked says why the answer can't be copied or saved right now, or ""
// if it can. verb is "copy" or "save".
func (m Model) exportBlocked(verb string) string {
	switch {
	case m.loading:
		return "still answering: wait for it to finish to " + verb + " (esc cancels)"
	case m.paneErr != "":
		return "that answer is incomplete, so Codie won't " + verb + " it"
	case m.raw == "":
		return "nothing to " + verb
	}
	return ""
}

// handleEvent applies one backend event to the answer pane.
func (m Model) handleEvent(msg eventMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.reqID || !m.loading {
		return m, nil // stale: that request was cancelled or already finished
	}
	if !msg.ok {
		m.fail("the connection closed before the answer finished")
		return m, nil
	}
	switch msg.ev.Kind {
	case backend.Text:
		// Keep following the newest text, unless the user has scrolled up to read.
		follow := m.pane.AtBottom()
		m.raw += msg.ev.Text
		m.renderPane()
		if follow {
			m.pane.GotoBottom()
		}
	case backend.Activity:
		m.activity = msg.ev.Text
	case backend.Done:
		m.finish()
		return m, nil
	case backend.Error:
		m.fail(msg.ev.Err.Error())
		return m, nil
	}
	return m, waitEvent(msg.id, m.events)
}

// finish completes a successful answer and records it in history.
func (m *Model) finish() {
	if strings.TrimSpace(m.raw) == "" {
		m.fail("the model returned an empty answer")
		return
	}
	m.endRequest()
	e := Entry{Question: m.paneTitle, Answer: m.raw, At: time.Now()}
	m.entries = append(m.entries, e)
	if err := m.store.Append(e); err != nil {
		m.status = "could not save to history: " + err.Error()
	}
}

// fail ends a request that did not complete. Whatever text arrived is kept on
// screen (so the user can read it) but is never stored or exported.
func (m *Model) fail(reason string) {
	m.endRequest()
	m.paneErr = reason
	m.renderPane()
	m.pane.GotoBottom()
}

// endRequest clears the in-flight state after a request finishes.
func (m *Model) endRequest() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading, m.activity, m.events = false, "", nil
}

// cancelRequest abandons the in-flight request; its remaining events are ignored.
func (m *Model) cancelRequest() {
	m.endRequest()
	m.reqID++
}

func (m *Model) openEntry(e Entry, fromHistory bool) {
	m.showPane(e.Question, e.Answer, "")
	m.fromHistory = fromHistory
	m.screen = screenAnswer
}

func (m *Model) showPane(title, raw, errText string) {
	m.paneTitle, m.raw, m.paneErr = title, raw, errText
	m.saving = false
	m.renderPane()
	m.pane.GotoTop()
}

func (m *Model) renderPane() {
	// Wrap to the pane width for display; copy/save still use m.raw untouched.
	wrap := lipgloss.NewStyle().Width(max(m.pane.Width, 1))
	body := ""
	if m.raw != "" {
		body = wrap.Render(m.raw)
	}
	if m.paneErr != "" {
		if body != "" {
			body += "\n\n"
		}
		body += errorStyle.Render(wrap.Render("error: " + m.paneErr))
	}
	m.pane.SetContent(body)
}

// ---- layout + view --------------------------------------------------------

// chrome is the number of non-viewport rows on the answer screen:
// title, blank line, status, footer.
const chrome = 4

func (m *Model) layout() {
	m.input.Width = max(min(m.w-12, 100), 10)
	m.pane.Width = max(m.w, 1)
	m.pane.Height = max(m.h-chrome, 1)
	m.saveInput.Width = max(m.w-12, 10)
	m.hist.list.SetSize(m.w, max(m.h-2, 1))
	m.renderPane()
}

func (m Model) View() string {
	switch m.screen {
	case screenAnswer:
		return m.viewAnswer()
	case screenHistory:
		return m.viewHistory()
	}
	return m.viewAsk()
}

func (m Model) viewAsk() string {
	box := boxStyle.Width(max(min(m.w-6, 104), 20)).Render(m.input.View())
	hint := helpStyle.Render("enter ask • ctrl+r last answer • ctrl+h history • esc quit")
	head := titleStyle.Render("codie")
	if m.label != "" {
		head += helpStyle.Render("  ·  " + m.label)
	}
	body := head + "\n\n" + box + "\n\n" + hint
	if m.status != "" {
		body += "\n" + statusStyle.Render(m.status)
	}
	if m.w == 0 || m.h == 0 {
		return body
	}
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, body)
}

func (m Model) viewAnswer() string {
	title := titleStyle.Render(truncate("› "+m.paneTitle, max(m.w, 10)))

	var body string
	if m.loading && m.raw == "" {
		// Nothing has streamed in yet: show the spinner where the answer will go.
		body = lipgloss.NewStyle().Height(m.pane.Height).Render(m.spin.View() + helpStyle.Render(" "+m.progressText()))
	} else {
		body = m.pane.View()
	}

	var footer string
	switch {
	case m.saving:
		footer = m.saveInput.View()
	case m.loading:
		footer = m.spin.View() + helpStyle.Render(" "+m.progressText()+" • esc cancel")
	default:
		footer = helpStyle.Render(fmt.Sprintf("↑/↓ scroll • ctrl+c copy • ctrl+s save • esc back • %3.0f%%", m.pane.ScrollPercent()*100))
	}

	return title + "\n\n" + body + "\n" + statusStyle.Render(m.status) + "\n" + footer
}

// ---- helpers ---------------------------------------------------------------

// progressText is what we say while waiting: the backend's latest activity if
// it gave one, otherwise a plain "researching…".
func (m Model) progressText() string {
	if m.activity != "" {
		return m.activity
	}
	return "researching…"
}

func truncate(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:max(n-1, 0)]) + "…"
}

// slug turns a question into a safe default filename stem.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "codie-answer"
	}
	return out
}

var errExists = errors.New("file already exists")
