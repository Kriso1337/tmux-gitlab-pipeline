package tui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"tmux-gitlab-pipeline/internal/ci"
)

type palette struct{ foreground, muted, accent, green, red, yellow, line color.Color }

func (m *Model) colors() palette {
	if !m.dark {
		return palette{lipgloss.Color("#263247"), lipgloss.Color("#68758B"), lipgloss.Color("#315FA8"), lipgloss.Color("#28795C"), lipgloss.Color("#B94455"), lipgloss.Color("#95651E"), lipgloss.Color("#D4DCE9")}
	}
	return palette{lipgloss.Color("#D9E1EC"), lipgloss.Color("#8290A6"), lipgloss.Color("#82ADFA"), lipgloss.Color("#7BC9A3"), lipgloss.Color("#EE8493"), lipgloss.Color("#D9B779"), lipgloss.Color("#384354")}
}

func paint(value string, c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render(value)
}

func (m *Model) statusColor(status ci.Status) color.Color {
	p := m.colors()
	switch status {
	case ci.Success:
		return p.green
	case ci.Failed:
		return p.red
	case ci.Manual, ci.Scheduled, ci.ActionRequired:
		return p.yellow
	case ci.Running, ci.Preparing:
		return p.accent
	default:
		return p.muted
	}
}

func (m *Model) icon(status ci.Status) string {
	if m.options.Icons == "ascii" {
		switch status {
		case ci.Success:
			return "+"
		case ci.Failed:
			return "x"
		case ci.Canceled:
			return "!"
		case ci.Manual:
			return ">"
		case ci.Skipped:
			return "-"
		default:
			if status.Active() {
				return "*"
			}
			return "."
		}
	}
	switch status {
	case ci.Success:
		return "✓"
	case ci.Failed:
		return "×"
	case ci.Canceled:
		return "⊘"
	case ci.Manual:
		return "▷"
	case ci.Skipped:
		return "−"
	case ci.Running, ci.Preparing:
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		return frames[m.frame%len(frames)]
	default:
		return "○"
	}
}

func statusLabel(status ci.Status) string {
	switch status {
	case ci.Created:
		return "создан"
	case ci.Pending:
		return "в очереди"
	case ci.Running:
		return "выполняется"
	case ci.Success:
		return "успешно"
	case ci.Failed:
		return "ошибка"
	case ci.Canceled:
		return "отменён"
	case ci.Skipped:
		return "пропущено"
	case ci.Manual:
		return "ручной запуск"
	case ci.Scheduled:
		return "запланирован"
	case ci.Waiting:
		return "ожидает ресурс"
	case ci.Preparing:
		return "подготовка"
	case ci.Neutral:
		return "нейтрально"
	case ci.ActionRequired:
		return "нужно действие"
	default:
		return oneLine(string(status))
	}
}

func elapsed(start, finish *time.Time, duration float64, now time.Time) string {
	seconds := max(0, int(duration))
	if start != nil {
		if finish != nil {
			seconds = max(0, int(finish.Sub(*start).Seconds()))
		} else {
			seconds = max(0, int(now.Sub(*start).Seconds()))
		}
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func (m *Model) View() tea.View {
	content := m.render()
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = clip(line, m.width)
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	p := m.colors()
	content = lipgloss.NewStyle().Foreground(p.foreground).Width(m.width).Height(m.height).Render(strings.Join(lines, "\n"))
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func (m *Model) render() string {
	p := m.colors()
	if m.width < 32 || m.height < 16 {
		return paint("Увеличьте окно до 32×16\nEsc — закрыть", p.muted)
	}
	w := m.width - 4
	if m.screen == worktreeScreen {
		return m.worktreeView(w)
	}
	if m.screen >= toolsScreen {
		return m.featureView(w)
	}
	logo, branch, provider := "⑂", "⑂", "GitLab CI"
	if m.options.Icons == "nerd" {
		logo, branch = "", ""
	}
	if m.options.Icons == "ascii" {
		logo, branch = "CI", "git"
	}
	if m.snapshot.Provider == ci.GitHub {
		provider = "GitHub Actions"
		if m.options.Icons == "nerd" {
			logo = ""
		}
	}
	title := logo + "  " + oneLine(m.snapshot.Repository.Project)
	if m.snapshot.Repository.Project == "" {
		title += "CI pipelines"
	}
	heading := lipgloss.NewStyle().Foreground(p.foreground).Bold(true).Render(clip(title, w))
	header := []string{beside(heading, paint(provider, p.muted), w)}
	ref := m.snapshot.Repository.Branch
	if ref == "" {
		ref = "detached HEAD"
	}
	sha := m.snapshot.Repository.SHA
	if len(sha) > 8 {
		sha = sha[:8]
	}
	header = append(header, paint(clip(branch+" "+oneLine(ref), w), p.accent)+paint("  ·  "+sha, p.muted))
	if m.snapshot.Pipeline == nil {
		message := "Загрузка пайплайна…"
		if !m.loading {
			message = m.snapshot.Notice
		}
		if m.err != nil {
			message = "Не удалось загрузить пайплайн\n\n" + clean(m.err.Error())
		}
		if m.actionErr != nil {
			message += "\n\n" + clean(m.actionErr.Error())
		}
		keys := m.keyHint("esc", "выйти") + "   " + m.keyHint("r", "повторить")
		if m.options.OpenChange != nil {
			keys += "   " + m.keyHint("m", "MR / PR")
		}
		if m.options.Features != nil {
			keys += "   " + m.keyHint("?", "действия")
			if m.snapshot.Provider == ci.GitHub {
				keys += "   " + m.keyHint("s", "workflow")
			}
		}
		if len(m.options.Worktrees) > 1 {
			keys += "   " + m.keyHint("w", "worktree")
		}
		return lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(header, "\n") + "\n\n" + lipgloss.NewStyle().Width(w).Render(message) + "\n\n" + clip(keys, w))
	}
	pipeline := m.snapshot.Pipeline
	header[1] = beside(header[1], paint(m.icon(pipeline.Status)+" "+statusLabel(pipeline.Status), m.statusColor(pipeline.Status)), w)
	mode := pipeline.Source
	if pipeline.Name != "" {
		mode = oneLine(pipeline.Name) + " · " + mode
	}
	if pipeline.MergeRequest != 0 {
		mode = fmt.Sprintf("MR !%d · %s", pipeline.MergeRequest, mode)
	}
	if m.options.PipelineID != 0 {
		mode += " · закреплён [a: авто]"
	}
	header = append(header, beside(paint(fmt.Sprintf("#%d  ·  %s", pipeline.ID, oneLine(mode)), p.muted), paint(elapsed(pipeline.StartedAt, pipeline.FinishedAt, pipeline.Duration, m.now), p.muted), w))
	finished, total, manual := m.snapshot.Progress()
	var jobs []ci.Job
	for _, stage := range m.snapshot.Stages {
		for _, job := range stage.Jobs {
			if job.Status != ci.Manual {
				jobs = append(jobs, job)
			}
		}
	}
	progress := fmt.Sprintf("%d/%d завершено", finished, total)
	if manual > 0 {
		progress += fmt.Sprintf(" · %d ручных", manual)
	}
	barWidth := min(24, max(3, w-ansi.StringWidth(progress)-3))
	header = append(header, clip(m.progressBar(jobs, barWidth)+"   "+paint(progress, p.muted), w))
	stageInfo := fmt.Sprintf("%d стадий", len(m.snapshot.Stages))
	if m.snapshot.Provider == ci.GitHub {
		stageInfo = "Workflow · " + oneLine(pipeline.Name)
		if len(m.snapshot.Candidates) > 1 {
			stageInfo = fmt.Sprintf("%d workflows  ·  p выбрать workflow", len(m.snapshot.Candidates))
		}
	}
	if len(m.snapshot.Stages) > 1 {
		stageInfo += "  ·  ←→ выбрать стадию"
	}
	notice := paint(stageInfo, p.muted)
	if m.snapshot.Notice != "" {
		notice = paint(m.snapshot.Notice, p.yellow)
	}
	header = append(header, clip(notice, w))
	if m.screen == logScreen {
		return m.logView(header, w)
	}
	body := m.stagesView(w, max(5, m.height-11))
	detail := "Нет jobs"
	if job, ok := m.selected(); ok {
		detail = fmt.Sprintf("%s  ·  %s  ·  %s", oneLine(job.Name), statusLabel(job.Status), elapsed(job.StartedAt, job.FinishedAt, job.Duration, m.now))
		if job.AllowFailure {
			detail += "  ·  допустимая ошибка"
		}
		if job.Bridge {
			detail += "  ·  trigger"
		}
	}
	updated := "обновлено " + m.snapshot.UpdatedAt.Format("15:04:05")
	if m.loading || m.snapshot.Refreshing {
		updated += "  ·  обновление…"
	}
	if m.snapshot.Cached {
		updated += "  ·  RAM"
	}
	if len(m.snapshot.Candidates) > 1 {
		updated += "  ·  p: другой запуск"
	}
	errorLine := ""
	if m.snapshot.CacheError != "" {
		errorLine = paint("Данные устарели: "+oneLine(m.snapshot.CacheError), p.red)
	}
	if m.err != nil {
		errorLine = paint("Данные устарели: "+oneLine(m.err.Error()), p.red)
	}
	if m.actionErr != nil {
		errorLine = paint(oneLine(m.actionErr.Error()), p.red)
	}
	if m.actionLoading {
		errorLine = paint("Открываю MR / PR в браузере…", p.muted)
	}
	keys := []string{m.keyHint("esc", "выйти"), m.keyHint("enter", "лог")}
	if m.options.Features != nil {
		keys = append(keys, m.keyHint("?", "действия"))
	}
	if len(m.snapshot.Stages) > 1 {
		keys = append(keys, m.keyHint("←→", "стадия"))
	}
	keys = append(keys, m.keyHint("↑↓", "job"))
	if len(m.snapshot.Candidates) > 1 {
		label := "запуск"
		if m.snapshot.Provider == ci.GitHub {
			label = "workflow"
		}
		keys = append(keys, m.keyHint("p", label))
	}
	keys = append(keys, m.keyHint("r", "обновить"))
	if m.options.OpenChange != nil {
		label := "MR"
		if m.snapshot.Provider == ci.GitHub {
			label = "PR"
		}
		keys = append(keys, m.keyHint("m", label))
	}
	if len(m.options.Worktrees) > 1 {
		keys = append(keys, m.keyHint("w", "worktree"))
	}
	footer := []string{paint(clip(detail, w), p.foreground), paint(clip(updated, w), p.muted), clip(errorLine, w), "", clip(strings.Join(keys, "   "), w)}
	return lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(header, "\n") + "\n\n" + body + "\n" + strings.Join(footer, "\n"))
}

func beside(left, right string, width int) string {
	rightWidth := ansi.StringWidth(right)
	if rightWidth+4 > width {
		return clip(left, width)
	}
	left = clip(left, width-rightWidth-2)
	return left + strings.Repeat(" ", max(2, width-ansi.StringWidth(left)-rightWidth)) + right
}

func (m *Model) keyHint(key, label string) string {
	p := m.colors()
	return lipgloss.NewStyle().Foreground(p.foreground).Bold(true).Render(key) + paint(" "+label, p.muted)
}

func (m *Model) progressBar(jobs []ci.Job, width int) string {
	p := m.colors()
	done := 0
	for _, job := range jobs {
		if job.Status.Finished() {
			done++
		}
	}
	filled := 0
	if len(jobs) > 0 {
		filled = done * width / len(jobs)
	}
	full, empty := "━", "─"
	if m.options.Icons == "ascii" {
		full, empty = "#", "-"
	}
	return paint(strings.Repeat(full, filled), p.accent) + paint(strings.Repeat(empty, width-filled), p.line)
}

func (m *Model) jobColumns(name, status, duration string, width int) string {
	if width < 60 {
		return beside(name, duration, width)
	}
	right := status + strings.Repeat(" ", max(2, 17-ansi.StringWidth(status))) + duration
	return beside(name, right, width)
}

func (m *Model) stagesView(width, height int) string {
	p := m.colors()
	if len(m.snapshot.Stages) == 0 {
		return lipgloss.NewStyle().Height(height).Render("В пайплайне нет jobs")
	}
	if len(m.snapshot.Stages) > 1 && width >= 82 {
		return m.stageSidebar(width, height)
	}
	count := min(4, len(m.snapshot.Stages), max(1, (width+3)/24))
	start := min(max(0, m.stage-count+1), max(0, len(m.snapshot.Stages)-count))
	tabWidth := (width - 3*(count-1)) / count
	tabs := make([]string, 0, count)
	for index := start; index < start+count; index++ {
		label := fmt.Sprintf("%02d %s", index+1, strings.ToUpper(oneLine(m.snapshot.Stages[index].Name)))
		style := lipgloss.NewStyle().Foreground(p.muted)
		if index == m.stage {
			style = style.Foreground(p.accent).Bold(true).Underline(true)
		}
		tabs = append(tabs, style.Width(tabWidth).Render(clip(label, tabWidth)))
	}
	stage := m.snapshot.Stages[m.stage]
	heading := m.jobColumns(paint(fmt.Sprintf("JOBS · %d", len(stage.Jobs)), p.muted), paint("STATUS", p.muted), paint("TIME", p.muted), width)
	mark := "─"
	if m.options.Icons == "ascii" {
		mark = "-"
	}
	lines := []string{heading, paint(strings.Repeat(mark, width), p.line)}
	if len(m.snapshot.Stages) > 1 {
		lines = append([]string{strings.Join(tabs, "   ")}, lines...)
	}
	rows := max(1, height-len(lines))
	offset := max(0, m.row-rows+1)
	for index := offset; index < min(len(stage.Jobs), offset+rows); index++ {
		job := stage.Jobs[index]
		prefix := " "
		style := lipgloss.NewStyle().Foreground(p.foreground)
		if index == m.row {
			prefix = "›"
			style = style.Foreground(p.accent).Bold(true)
			if m.options.Icons == "ascii" {
				prefix = ">"
			}
		}
		name := prefix + m.icon(job.Status) + " " + oneLine(job.Name)
		if job.AllowFailure && job.Status == ci.Failed {
			name += " !"
		}
		duration := elapsed(job.StartedAt, job.FinishedAt, job.Duration, m.now)
		if job.StartedAt == nil && job.Duration == 0 {
			duration = "—"
			if m.options.Icons == "ascii" {
				duration = "-"
			}
		}
		lines = append(lines, m.jobColumns(style.Render(name), paint(statusLabel(job.Status), m.statusColor(job.Status)), paint(duration, p.muted), width))
	}
	return lipgloss.NewStyle().Height(height).Render(strings.Join(lines, "\n"))
}

func (m *Model) stageSidebar(width, height int) string {
	p := m.colors()
	leftWidth := min(30, width/3)
	rightWidth := width - leftWidth - 3
	rows := height - 2
	stage := m.snapshot.Stages[m.stage]
	left := paint("STAGES", p.muted)
	right := m.jobColumns(paint(fmt.Sprintf("JOBS · %d", len(stage.Jobs)), p.muted), paint("STATUS", p.muted), paint("TIME", p.muted), rightWidth)
	lines := []string{lipgloss.NewStyle().Width(leftWidth).Render(left) + "   " + right, paint(strings.Repeat("─", width), p.line)}
	stageStart := max(0, m.stage-rows+1)
	jobStart := max(0, m.row-rows+1)
	for i := range rows {
		left, right = "", ""
		if index := stageStart + i; index < len(m.snapshot.Stages) {
			s := m.snapshot.Stages[index]
			done, total, manual := s.Progress()
			progress := fmt.Sprintf("%d/%d", done, total)
			if manual > 0 {
				progress += fmt.Sprintf("+%d", manual)
			}
			name := fmt.Sprintf("  %02d %s", index+1, strings.ToUpper(oneLine(s.Name)))
			style := lipgloss.NewStyle().Foreground(p.muted)
			if index == m.stage {
				name = "›" + strings.TrimPrefix(name, " ")
				style = style.Foreground(p.accent).Bold(true)
			}
			left = style.Render(beside(name, progress, leftWidth))
		}
		if index := jobStart + i; index < len(stage.Jobs) {
			job := stage.Jobs[index]
			prefix := " "
			style := lipgloss.NewStyle().Foreground(p.foreground)
			if index == m.row {
				prefix = "›"
				style = style.Foreground(p.accent).Bold(true)
			}
			name := prefix + m.icon(job.Status) + " " + oneLine(job.Name)
			if job.AllowFailure && job.Status == ci.Failed {
				name += " !"
			}
			duration := elapsed(job.StartedAt, job.FinishedAt, job.Duration, m.now)
			if job.StartedAt == nil && job.Duration == 0 {
				duration = "—"
			}
			right = m.jobColumns(style.Render(name), paint(statusLabel(job.Status), m.statusColor(job.Status)), paint(duration, p.muted), rightWidth)
		}
		lines = append(lines, lipgloss.NewStyle().Width(leftWidth).Render(left)+"   "+right)
	}
	return strings.Join(lines, "\n")
}

func (m *Model) logView(header []string, width int) string {
	p := m.colors()
	job, ok := m.selected()
	if !ok {
		return strings.Join(header, "\n")
	}
	header[4] = paint(clip("ЛОГ  "+oneLine(job.Name)+"  ·  "+statusLabel(job.Status), width), m.statusColor(job.Status))
	height := m.logHeight()
	start := m.offset
	if m.follow {
		start = max(0, len(m.lines)-height)
	}
	lines := make([]string, 0, height)
	for i := start; i < min(len(m.lines), start+height); i++ {
		line := m.lines[i]
		if m.search.current > 0 && i == m.search.line {
			first, last := m.searchSpan()
			line = line[:first] + lipgloss.NewStyle().Foreground(p.accent).Bold(true).Underline(true).Render(line[first:last]) + line[last:]
		}
		line = ansi.TruncateLeft(line, m.horizontal, "")
		lines = append(lines, paint(fmt.Sprintf("%5d │ ", i+1), p.muted)+clip(line, width-8))
	}
	if len(m.lines) == 0 {
		label := "Лог пуст"
		if m.traceLoading {
			label = "Загрузка лога…"
		}
		lines = append(lines, paint(label, p.muted))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	info := fmt.Sprintf("строки %d–%d / %d", min(start+1, len(m.lines)), min(start+height, len(m.lines)), len(m.lines))
	if m.follow {
		info += "  ·  следование"
	} else {
		info += "  ·  пауза"
	}
	if m.trace.Truncated {
		info += "  ·  последние 1 МиБ"
	}
	var problems []string
	if m.err != nil {
		problems = append(problems, "Данные устарели: "+oneLine(m.err.Error()))
	}
	if m.traceErr != nil {
		problems = append(problems, "Лог: "+oneLine(m.traceErr.Error()))
	}
	if m.actionErr != nil {
		problems = append(problems, oneLine(m.actionErr.Error()))
	}
	errorLine := paint(strings.Join(problems, " · "), p.red)
	searchLine := ""
	if m.query != "" || m.searching {
		query := oneLine(m.query)
		if m.searching {
			query += "▏"
		}
		searchLine = paint(m.searchStatus()+"  /"+query, p.accent)
		if m.search.current > 0 {
			searchLine = beside(searchLine, paint(fmt.Sprintf("строка %d", m.search.line+1), p.muted), width)
		}
	}
	keys := []string{m.keyHint("esc", "назад"), m.keyHint("/", "поиск"), m.keyHint("n", "далее"), m.keyHint("Shift+N", "назад")}
	if width < 64 {
		keys = []string{m.keyHint("esc", ""), m.keyHint("/", ""), m.keyHint("n", "→"), m.keyHint("Shift+N", "←")}
	}
	if m.options.OpenChange != nil {
		label := "MR"
		if m.snapshot.Provider == ci.GitHub {
			label = "PR"
		}
		keys = append(keys, m.keyHint("m", label))
	}
	keys = append(keys, m.keyHint("f", "следовать"), m.keyHint("↑↓", "прокрутка"), m.keyHint("←→", "строка"))
	footer := []string{paint(clip(info, width), p.muted), clip(errorLine, width), clip(searchLine, width), clip(strings.Join(keys, "  "), width)}
	return lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(header, "\n") + "\n\n" + strings.Join(lines, "\n") + "\n" + strings.Join(footer, "\n"))
}

func RenderSnapshot(snapshot ci.Snapshot, options Options, width, height int) string {
	m := New(context.Background(), nil, options)
	m.snapshot, m.width, m.height = snapshot, width, height
	m.restoreSelection(ci.Job{})
	return m.View().Content
}
