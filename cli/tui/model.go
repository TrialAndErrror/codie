// Package tui holds the Bubble Tea model for codie's interactive mode.
//
// Screens:
//
//	ask      empty text box                  enter: ask • ctrl+r: last answer • ctrl+h: history • esc: quit
//	answer   scrollable response pane        j/k: pick a code block • c: copy it • ctrl+s: save • esc: back
//
// ctrl+c quits from every screen.
//
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
	blocks      []blockRef // selectable code blocks in the answer, in order; rebuilt on every render
	sel         int        // index into blocks of the highlighted one, or -1 for none
	cancel      context.CancelFunc
	reqID       int
	fromHistory bool // esc returns to history instead of the ask screen
	saving      bool
	saveInput   textinput.Model
	saveText    string // exactly what the save prompt will write
	saveWhat    string // what that is, for the prompt: "answer" or "block 2/3 (go)"

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
		sel:        -1,
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
		if msg.String() == "ctrl+c" {
			return m.quit() // the regular stop signal, on every screen
		}
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

// quit stops the program, first cancelling any in-flight request so the
// connection to the model is dropped rather than left generating.
func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	return m, tea.Quit
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
	case "esc":
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
		if m.sel >= 0 { // first esc leaves block selection and shows the whole answer again
			m.sel = -1
			m.renderPane()
			return m, nil
		}
		if m.loading {
			m.cancelRequest()
		}
		m.screen = screenAsk
		if m.fromHistory {
			m.screen = screenHistory
		}
		return m, nil

	// With code blocks to pick from, down/j and up/k move between them (first
	// press: the first / the last block). Otherwise they fall through and scroll.
	case "j", "down":
		if m.canSelect() {
			m.moveSelection(+1)
			return m, nil
		}
	case "k", "up":
		if m.canSelect() {
			m.moveSelection(-1)
			return m, nil
		}

	case "c":
		m.copySelected(false) // with begin/end markers
		return m, nil
	case "C":
		m.copySelected(true) // plain code, exactly as written
		return m, nil

	case "ctrl+s":
		return m.startSave()

	case "g":
		m.pane.GotoTop()
		return m, nil
	case "G":
		m.pane.GotoBottom()
		return m, nil
	}

	// Everything else (pgup/pgdn, space, u/d, ...) scrolls the viewport. These
	// keep working while a block is selected, so a tall block can be read.
	var cmd tea.Cmd
	m.pane, cmd = m.pane.Update(msg)
	return m, cmd
}

// canSelect reports whether code blocks can be stepped through: the answer is
// complete and has at least one. A half-finished answer's blocks may be
// half-finished too, so those are never offered.
func (m Model) canSelect() bool {
	return !m.loading && m.paneErr == "" && len(m.blocks) > 0
}

// moveSelection moves the highlight to the next (+1) or previous (-1) code
// block, wrapping at the ends. From "nothing selected", +1 picks the first
// block and -1 picks the last.
func (m *Model) moveSelection(dir int) {
	n := len(m.blocks)
	switch {
	case m.sel < 0 && dir > 0:
		m.sel = 0
	case m.sel < 0:
		m.sel = n - 1
	default:
		m.sel = (m.sel + dir + n) % n
	}
	m.renderPane() // redraw with the new highlight (this also refreshes block positions)
	m.revealSelection()
}

// revealSelection scrolls the minimum needed to show the selected step: the
// sentences that introduce the block, then the block itself. Answers are written
// as a walkthrough (words, code, words, code), so stepping with j/k should read
// like turning a page, not drop you onto bare code with its explanation off the
// top of the screen.
//
//   - If the explanation and the block both fit, both are brought into view.
//   - If the block fits but explanation and block together don't, the block is
//     shown whole, with as much of the explanation above it as there is room for.
//   - A block taller than the pane shows its top; page down to read the rest.
func (m *Model) revealSelection() {
	if m.sel < 0 || m.sel >= len(m.blocks) {
		return
	}
	b := m.blocks[m.sel]
	top, height := m.pane.YOffset, m.pane.Height
	blockHeight := b.end - b.start + 1
	span := b.end - b.intro + 1
	switch {
	case blockHeight > height:
		m.pane.SetYOffset(max(b.start-1, 0))
	case span > height:
		m.pane.SetYOffset(b.end - height + 1)
	case b.intro < top:
		m.pane.SetYOffset(b.intro)
	case b.end >= top+height:
		// Scroll down just far enough, keeping a line of whatever follows in view
		// as a cue that there is more, without pushing the explanation off the top.
		m.pane.SetYOffset(min(b.end-height+2, b.intro))
	}
}

// blockLabel names the selected block for the footer and messages,
// e.g. "block 2/3 (go)".
func (m Model) blockLabel() string {
	label := fmt.Sprintf("block %d/%d", m.sel+1, len(m.blocks))
	if lang := m.blocks[m.sel].lang; lang != "" {
		label += " (" + lang + ")"
	}
	return label
}

// copySelected copies the selected code block, and only that: no fences, no
// explanation, nothing from neighbouring blocks, and never the line wrapping seen
// on screen. By default the code is wrapped in Codie begin/end banners (see
// snippet.go) so it is clear where it starts and ends once pasted; plain copies
// exactly the code and nothing else.
func (m *Model) copySelected(plain bool) {
	if why := m.exportBlocked("copy"); why != "" {
		m.status = why
		return
	}
	switch {
	case len(m.blocks) == 0:
		m.status = "this answer has no code blocks to copy"
		return
	case m.sel < 0:
		m.status = "press j or k to select a code block first"
		return
	}

	b := m.blocks[m.sel]
	text, note := b.text, " as plain code"
	if !plain {
		if wrapped, ok := withBanner(b.lang, b.text); ok {
			text, note = wrapped, " with begin/end markers"
		} else {
			note = " (" + b.lang + " has no comments, so no markers)"
		}
	}
	if err := m.copyFn(text); err != nil {
		m.status = "copy failed: " + err.Error()
		return
	}
	m.status = "copied " + m.blockLabel() + note
}

// startSave opens the filename prompt. With a block selected it saves that
// block (named for its language); otherwise it saves the whole answer.
func (m Model) startSave() (tea.Model, tea.Cmd) {
	if why := m.exportBlocked("save"); why != "" {
		m.status = why
		return m, nil
	}
	m.saveText, m.saveWhat = m.raw, "answer"
	name := slug(m.paneTitle) + ".txt"
	if m.sel >= 0 && m.sel < len(m.blocks) {
		b := m.blocks[m.sel]
		m.saveText, m.saveWhat = b.text, m.blockLabel()
		name = slug(m.paneTitle) + extFor(b.lang)
	}
	m.saving = true
	m.saveInput.Prompt = "Save " + m.saveWhat + " as: "
	m.saveInput.SetValue(name)
	m.saveInput.CursorEnd()
	m.saveInput.Focus()
	return m, textinput.Blink
}

func (m Model) updateSaving(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.saving = false
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.saveInput.Value())
		if name == "" {
			return m, nil
		}
		if err := saveFile(name, m.saveText); err != nil {
			m.status = errorStyle.Render("save failed: " + err.Error())
		} else {
			m.status = "saved " + m.saveWhat + " to " + name
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
	// History comes from a file on disk, so sanitize it like anything else
	// shown in the terminal.
	m.showPane(e.Question, backend.Sanitize(e.Answer), "")
	m.fromHistory = fromHistory
	m.screen = screenAnswer
}

func (m *Model) showPane(title, raw, errText string) {
	m.paneTitle, m.raw, m.paneErr = title, raw, errText
	m.saving, m.sel = false, -1
	m.renderPane()
	m.pane.GotoTop()
}

// blockRef is a selectable code block and where it sits in the rendered pane.
type blockRef struct {
	text, lang string
	start, end int // first and last line of the block in the rendered content
	// intro is the first line of the prose paragraph directly above the block (the
	// sentences that introduce this step), or start if the block has none.
	intro int
}

var (
	gutterStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	gutterSelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	codeStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	codeSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("236"))
)

// renderPane rebuilds the pane's content from the raw answer: prose wrapped to
// the width, each code block drawn with a left gutter, the selected block
// highlighted. It also records where each block landed so selection can scroll
// to it. Copy and save always use the unwrapped text, never this rendering.
func (m *Model) renderPane() {
	width := max(m.pane.Width, 1)
	prose := lipgloss.NewStyle().Width(width)

	var (
		parts      []string
		blocks     []blockRef
		line       int
		proseStart = -1 // first line of the prose part just before this one, or -1
	)
	for _, seg := range parseAnswer(m.raw) {
		var out string
		if seg.Code {
			idx := len(blocks)
			out = renderCode(seg.Text, width, seg.selectable() && idx == m.sel)
			if seg.selectable() {
				intro := line
				if proseStart >= 0 {
					intro = proseStart
				}
				blocks = append(blocks, blockRef{text: seg.Text, lang: seg.Lang(), start: line, end: line + strings.Count(out, "\n"), intro: intro})
			}
			proseStart = -1
		} else {
			out = prose.Render(seg.Text)
			proseStart = line
		}
		parts = append(parts, out)
		line += strings.Count(out, "\n") + 2 // this part's lines, plus the blank line after it
	}
	if m.paneErr != "" {
		parts = append(parts, errorStyle.Render(prose.Render("error: "+m.paneErr)))
	}

	m.blocks = blocks
	if m.sel >= len(blocks) {
		m.sel = -1
	}
	m.pane.SetContent(strings.Join(parts, "\n\n"))
}

// renderCode draws a code block with a gutter. The selected block gets a heavier
// gutter and a background, so it stands out even without colour support.
func renderCode(text string, width int, selected bool) string {
	gutter, gs, body := "│ ", gutterStyle, codeStyle
	if selected {
		gutter, gs, body = "▌ ", gutterSelStyle, codeSelStyle
	}
	lines := strings.Split(body.Width(max(width-2, 1)).Render(text), "\n")
	for i := range lines {
		lines[i] = gs.Render(gutter) + lines[i]
	}
	return strings.Join(lines, "\n")
}

// langExt maps the language on a code fence to a file extension for saving.
var langExt = map[string]string{
	"go": ".go", "golang": ".go", "python": ".py", "py": ".py",
	"javascript": ".js", "js": ".js", "typescript": ".ts", "ts": ".ts", "jsx": ".jsx", "tsx": ".tsx",
	"rust": ".rs", "rs": ".rs", "bash": ".sh", "sh": ".sh", "shell": ".sh", "zsh": ".sh",
	"sql": ".sql", "java": ".java", "kotlin": ".kt", "swift": ".swift", "ruby": ".rb", "rb": ".rb",
	"php": ".php", "lua": ".lua", "haskell": ".hs", "hs": ".hs",
	"c": ".c", "cpp": ".cpp", "c++": ".cpp", "csharp": ".cs", "cs": ".cs",
	"html": ".html", "css": ".css", "json": ".json", "yaml": ".yaml", "yml": ".yaml",
	"toml": ".toml", "markdown": ".md", "md": ".md",
}

// extFor returns the extension for a fence language. It only ever returns
// values from the table above (or .txt), so a model can't smuggle a path or odd
// extension into the suggested filename.
func extFor(lang string) string {
	if ext, ok := langExt[lang]; ok {
		return ext
	}
	return ".txt"
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
	m.revealSelection()
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
		footer = helpStyle.Render(m.answerHelp())
	}

	return title + "\n\n" + body + "\n" + statusStyle.Render(m.status) + "\n" + footer
}

// ---- helpers ---------------------------------------------------------------

// answerHelp is the footer for a finished answer. It changes with what you can
// do: with a block selected, with blocks to select, or just scrolling.
func (m Model) answerHelp() string {
	pct := m.pane.ScrollPercent() * 100
	switch {
	case m.sel >= 0:
		return m.blockLabel() + " • j/k move • c copy • C plain • ctrl+s save • esc deselect"
	case m.canSelect():
		plural := "s"
		if len(m.blocks) == 1 {
			plural = ""
		}
		return fmt.Sprintf("%d code block%s • j/k select • pgup/pgdn scroll • ctrl+s save • esc back • %3.0f%%", len(m.blocks), plural, pct)
	case m.paneErr != "":
		return fmt.Sprintf("↑/↓ scroll • esc back • %3.0f%%", pct)
	}
	return fmt.Sprintf("↑/↓ scroll • ctrl+s save • esc back • %3.0f%%", pct)
}

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
