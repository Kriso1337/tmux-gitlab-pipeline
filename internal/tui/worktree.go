package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tmux-gitlab-pipeline/internal/repository"
)

type worktreeSelectedMsg struct {
	provider   Provider
	directory  string
	err        error
	generation uint64
}

type changeOpenedMsg struct {
	err        error
	generation uint64
}

func (m *Model) filteredWorktrees() []repository.Worktree {
	var result []repository.Worktree
	for _, tree := range m.options.Worktrees {
		if strings.Contains(strings.ToLower(tree.Branch+" "+tree.Directory), strings.ToLower(m.worktreeQuery)) {
			result = append(result, tree)
		}
	}
	return result
}

func (m *Model) worktreeKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.worktreeLoading {
		return nil
	}
	trees := m.filteredWorktrees()
	switch msg.String() {
	case "esc":
		if m.snapshot.Repository.Project == "" {
			m.close()
			return tea.Quit
		}
		m.screen = m.previousScreen
		if m.screen == logScreen {
			return tea.Batch(m.load(), m.loadTrace())
		}
		return m.load()
	case "up":
		m.worktreeRow = max(0, m.worktreeRow-1)
	case "down":
		m.worktreeRow = min(max(0, len(trees)-1), m.worktreeRow+1)
	case "backspace":
		if m.worktreeQuery != "" {
			_, size := utf8.DecodeLastRuneInString(m.worktreeQuery)
			m.worktreeQuery = m.worktreeQuery[:len(m.worktreeQuery)-size]
			m.worktreeRow = 0
		}
	case "ctrl+u":
		m.worktreeQuery, m.worktreeRow = "", 0
	case "enter":
		if len(trees) > 0 && m.options.SwitchRepository != nil {
			directory := trees[m.worktreeRow].Directory
			m.close()
			m.generation++
			m.traceGeneration++
			m.actionGeneration++
			m.actionLoading = false
			m.worktreeLoading = true
			generation, switchRepo := m.generation, m.options.SwitchRepository
			return func() tea.Msg {
				provider, err := switchRepo(m.ctx, directory)
				return worktreeSelectedMsg{provider: provider, directory: directory, err: err, generation: generation}
			}
		}
	default:
		if msg.Text != "" {
			m.worktreeQuery += msg.Text
			m.worktreeRow = 0
		}
	}
	return nil
}

func (m *Model) worktreeView(width int) string {
	p := m.colors()
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(p.foreground).Render("Выберите worktree"), paint("Каталог Claude/Codex может отличаться от каталога панели tmux", p.muted), "", paint("Фильтр  "+oneLine(m.worktreeQuery)+"▏", p.accent), paint(strings.Repeat("─", width), p.line)}
	trees := m.filteredWorktrees()
	rows := max(1, m.height-9)
	start := max(0, m.worktreeRow-rows+1)
	for i := range rows {
		line := ""
		if index := start + i; index < len(trees) {
			tree := trees[index]
			branch := tree.Branch
			if branch == "" {
				branch = "detached " + tree.SHA[:min(8, len(tree.SHA))]
			}
			label := "  " + branch + "  ·  " + tree.Directory
			style := lipgloss.NewStyle().Foreground(p.muted)
			if index == m.worktreeRow {
				label = "› " + branch + "  ·  " + tree.Directory
				style = style.Foreground(p.accent).Bold(true)
			}
			line = style.Render(clip(oneLine(label), width))
		} else if len(trees) == 0 && i == 0 {
			line = paint("Нет совпадений", p.muted)
		}
		lines = append(lines, line)
	}
	status := "Выбор сохраняется для текущего процесса в этой панели"
	if m.worktreeLoading {
		status = "Переключение…"
	}
	if m.actionErr != nil {
		status = m.actionErr.Error()
	}
	lines = append(lines, "", paint(clip(oneLine(status), width), p.muted), clip(m.keyHint("esc", "назад")+"   "+m.keyHint("↑↓", "выбор")+"   "+m.keyHint("enter", "открыть")+"   "+m.keyHint("ctrl+u", "очистить"), width))
	return lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(lines, "\n"))
}
