package cli

import (
	"fmt"
	"io"

	"github.com/azhar/cerebro/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// menuItem is one selectable row in the startup menu.
type menuItem struct {
	choice runChoice
	title  string
	desc   string
}

var menuItems = []menuItem{
	{choiceTUI, "Terminal UI", "rich TUI on this terminal + web dashboard"},
	{choiceWeb, "Web only", "logs on this terminal + web dashboard"},
	{choiceBackground, "Background", "detach and keep running after you log off (web dashboard)"},
	{choiceQuit, "Quit", "abort startup"},
}

var (
	menuTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	menuCursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	menuSelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	menuDescStyle     = lipgloss.NewStyle().Faint(true)
	menuHintStyle     = lipgloss.NewStyle().Faint(true).MarginTop(1)
	menuDefStyle      = lipgloss.NewStyle().Faint(true).Italic(true)
)

// menuModel is the Bubble Tea model backing the arrow-key startup picker.
type menuModel struct {
	cursor   int
	def      domain.RunMode
	selected runChoice
	quitting bool
}

func newMenuModel(def domain.RunMode) menuModel {
	// Start the cursor on the configured default so [enter] matches the hint.
	cursor := 0
	if def == domain.RunModeWeb {
		cursor = 1
	}
	return menuModel{cursor: cursor, def: def}
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(menuItems)-1 {
			m.cursor++
		}
	case "1", "2", "3", "4":
		m.cursor = int(key.String()[0] - '1')
		m.selected = menuItems[m.cursor].choice
		return m, tea.Quit
	case "enter", " ":
		m.selected = menuItems[m.cursor].choice
		return m, tea.Quit
	case "q":
		m.selected = choiceQuit
		m.quitting = true
		return m, tea.Quit
	case "ctrl+c", "esc":
		m.selected = choiceQuit
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m menuModel) View() string {
	if m.quitting {
		return ""
	}

	var b []byte
	b = append(b, menuTitleStyle.Render("Select run mode")...)
	b = append(b, '\n', '\n')

	for i, it := range menuItems {
		cursor := "  "
		title := it.title
		if i == m.cursor {
			cursor = menuCursorStyle.Render("▶ ")
			title = menuSelTitleStyle.Render(title)
		}
		row := fmt.Sprintf("%s%d. %-12s %s", cursor, i+1, title, menuDescStyle.Render(it.desc))
		b = append(b, row...)
		b = append(b, '\n')
	}

	hint := fmt.Sprintf("↑/↓ move • 1-4 jump • enter select • q quit   %s",
		menuDefStyle.Render(fmt.Sprintf("(default: %s)", m.def)))
	b = append(b, menuHintStyle.Render(hint)...)
	b = append(b, '\n')
	return string(b)
}

// runStartupMenu renders the interactive picker on the given streams and
// returns the operator's choice. It satisfies menuFunc. The program reads keys
// from in and draws to out so tests can drive it without a real terminal.
func runStartupMenu(in io.Reader, out io.Writer) menuFunc {
	return func(def domain.RunMode) (runChoice, error) {
		p := tea.NewProgram(newMenuModel(def), tea.WithInput(in), tea.WithOutput(out))
		final, err := p.Run()
		if err != nil {
			return "", err
		}
		m, ok := final.(menuModel)
		if !ok || m.selected == "" {
			return choiceQuit, nil
		}
		return m.selected, nil
	}
}
