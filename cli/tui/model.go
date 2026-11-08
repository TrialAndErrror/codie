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

// resultMsg is delivered to Update when a Research call finishes. id lets us
// ignore results from requests the user already cancelled.
type resultMsg struct {
	id       int
	question string
	answer   string
	err      error
}

// Model is the root TUI state. It owns every screen and routes events to the
// active one.
type Model struct {
	researcher Researcher
	store      Store
	copyFn     func(string) error // injectable so tests don't touch the clipboard

	screen screen
	w, h   int
	status string // one-line feedback ("copied", "saved to x.txt", errors)

	// ask screen
	input textinput.Model

	// answer screen
	pane        viewport.Model
	spin        spinner.Model
	paneTitle   string // the question being shown
	raw         string // the answer exactly as generated (what copy/save use)
	paneErr     string // set instead of raw when the request failed
	loading     bool
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
func New(r Researcher, s Store) Model {
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

func (m Model) Init() tea.Cmd { return textinput.Blink }

// research returns a tea.Cmd: a function Bubble Tea runs in a goroutine. Its
// returned Msg is fed back into Update when it completes.
func research(ctx context.Context, r Researcher, id int, q string) tea.Cmd {
	return func() tea.Msg {
		a, err := r.Research(ctx, q)
		return resultMsg{id: id, question: q, answer: a, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil

	case resultMsg:
		return m.handleResult(msg)

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
		m.cancel, m.loading = cancel, true
		m.input.Reset()
		m.showPane(q, "", "")
		m.fromHistory = false
		m.screen = screenAnswer
		return m, tea.Batch(m.spin.Tick, research(ctx, m.researcher, m.reqID, q))

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
		if m.raw == "" {
			m.status = "nothing to copy"
			return m, nil
		}
		if err := m.copyFn(m.raw); err != nil {
			m.status = "copy failed: " + err.Error()
		} else {
			m.status = "copied response to clipboard"
		}
		return m, nil

	case "ctrl+s":
		if m.raw == "" {
			m.status = "nothing to save"
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

func (m Model) handleResult(msg resultMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.reqID || !m.loading {
		return m, nil // stale: that request was cancelled
	}
	m.loading = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if msg.err != nil {
		m.showPane(msg.question, "", msg.err.Error())
		return m, nil
	}
	e := Entry{Question: msg.question, Answer: msg.answer, At: time.Now()}
	m.entries = append(m.entries, e)
	if err := m.store.Append(e); err != nil {
		m.status = "could not save to history: " + err.Error()
	}
	m.showPane(msg.question, msg.answer, "")
	return m, nil
}

// cancelRequest abandons the in-flight request; its eventual result is ignored.
func (m *Model) cancelRequest() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.loading = false
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
	body := m.raw
	if m.paneErr != "" {
		body = errorStyle.Render("error: " + m.paneErr)
	}
	// Wrap to the pane width for display; copy/save still use m.raw untouched.
	m.pane.SetContent(lipgloss.NewStyle().Width(max(m.pane.Width, 1)).Render(body))
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
	body := titleStyle.Render("codie") + "\n\n" + box + "\n\n" + hint
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
	if m.loading {
		body = lipgloss.NewStyle().Height(m.pane.Height).Render(m.spin.View() + helpStyle.Render(" researching…"))
	} else {
		body = m.pane.View()
	}

	var footer string
	switch {
	case m.saving:
		footer = m.saveInput.View()
	case m.loading:
		footer = helpStyle.Render("esc cancel")
	default:
		footer = helpStyle.Render(fmt.Sprintf("↑/↓ scroll • ctrl+c copy • ctrl+s save • esc back • %3.0f%%", m.pane.ScrollPercent()*100))
	}

	return title + "\n\n" + body + "\n" + statusStyle.Render(m.status) + "\n" + footer
}

// ---- helpers ---------------------------------------------------------------

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
