package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// historyItem adapts an Entry to list.Item. The list's built-in "/" search
// matches against FilterValue, so searching hits the words in the question.
type historyItem struct {
	entry Entry
}

func (i historyItem) Title() string       { return i.entry.Question }
func (i historyItem) Description() string { return i.entry.At.Format("Mon 2006-01-02 15:04:05") }
func (i historyItem) FilterValue() string { return i.entry.Question }

type historyView struct {
	list list.Model
}

func newHistoryView() historyView {
	l := list.New(nil, list.NewDefaultDelegate(), 80, 20)
	l.Title = "History"
	l.SetShowHelp(true)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings() // we own the keys: esc navigates, ctrl+c quits (see Model.Update)
	return historyView{list: l}
}

// setEntries shows entries newest-first.
func (h *historyView) setEntries(entries []Entry) {
	items := make([]list.Item, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		items = append(items, historyItem{entry: entries[i]})
	}
	h.list.ResetFilter()
	h.list.SetItems(items)
	h.list.Select(0)
}

func (m Model) updateHistory(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the user is typing a search, every key belongs to the list.
	if m.hist.list.FilterState() != list.Filtering {
		switch msg.String() {
		case "esc":
			// First esc clears an applied search; the next one leaves the page.
			if m.hist.list.FilterState() == list.FilterApplied {
				break
			}
			m.screen = screenAsk
			return m, nil
		case "enter":
			if it, ok := m.hist.list.SelectedItem().(historyItem); ok {
				m.openEntry(it.entry, true)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.hist.list, cmd = m.hist.list.Update(msg)
	return m, cmd
}

func (m Model) viewHistory() string {
	if len(m.hist.list.Items()) == 0 {
		return titleStyle.Render("History") + "\n\n" + helpStyle.Render("No questions yet.  esc to go back") + "\n"
	}
	return m.hist.list.View() + "\n" + helpStyle.Render("enter open • / search • esc back")
}
