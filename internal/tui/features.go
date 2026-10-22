package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

type FeatureProvider interface {
	History(context.Context, repository.Repository, ci.Pipeline) ([]ci.Pipeline, error)
	Mutate(context.Context, repository.Repository, ci.Mutation) error
	Workflows(context.Context, repository.Repository) ([]ci.Workflow, error)
	Artifacts(context.Context, ci.Snapshot) ([]ci.Artifact, error)
	Download(context.Context, repository.Repository, ci.Artifact, string) (string, error)
	Report(context.Context, ci.Snapshot) (ci.Report, error)
}
type featureState struct {
	snapshot    ci.Snapshot
	generation  uint64
	cancel      context.CancelFunc
	loading     bool
	err         error
	row, offset int
	lines       []string
	history     []ci.Pipeline
	artifacts   []ci.Artifact
	workflows   []ci.Workflow
	mutation    ci.Mutation
	repo        repository.Repository
	input       string
	job         ci.Job
}
type featureMsg struct {
	generation uint64
	lines      []string
	history    []ci.Pipeline
	artifacts  []ci.Artifact
	workflows  []ci.Workflow
	err        error
	mutated    bool
}

func (m *Model) featureCommand(screen screen, work func(context.Context) featureMsg) tea.Cmd {
	m.cancelFeature()
	m.feature.generation++
	generation := m.feature.generation
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
	m.feature.cancel = cancel
	m.feature.loading = true
	m.feature.err = nil
	m.feature.row, m.feature.offset = 0, 0
	m.feature.lines = nil
	m.feature.history, m.feature.artifacts, m.feature.workflows = nil, nil, nil
	m.screen = screen
	return func() tea.Msg { defer cancel(); result := work(ctx); result.generation = generation; return result }
}
func (m *Model) cancelFeature() {
	if m.feature.cancel != nil {
		m.feature.cancel()
		m.feature.cancel = nil
	}
	m.feature.generation++
	m.feature.loading = false
}
func (m *Model) featureUpdate(msg featureMsg) tea.Cmd {
	if msg.generation != m.feature.generation {
		return nil
	}
	m.feature.loading = false
	m.feature.err = msg.err
	if msg.mutated {
		m.screen = stageScreen
		m.actionErr = msg.err
		return m.loadWithRefresh(true)
	}
	if msg.err != nil {
		return nil
	}
	m.feature.lines, m.feature.history, m.feature.artifacts, m.feature.workflows = msg.lines, msg.history, msg.artifacts, msg.workflows
	return nil
}
func (m *Model) openFeature(key string) (tea.Cmd, bool) {
	if m.options.Features == nil || m.snapshot.Repository.Project == "" {
		return nil, false
	}
	if m.snapshot.Pipeline == nil && key != "?" && (key != "s" || m.snapshot.Provider != ci.GitHub) {
		return nil, false
	}
	m.feature.snapshot = m.snapshot
	snapshot, provider := m.snapshot, m.options.Features
	job, selected := m.selected()
	switch key {
	case "?":
		m.cancelFeature()
		m.feature = featureState{generation: m.feature.generation, snapshot: m.snapshot}
		m.screen = toolsScreen
		return nil, true
	case "d":
		if !selected {
			return nil, true
		}
		m.feature.job = job
		loader := m.provider
		return m.featureCommand(diagnosticScreen, func(ctx context.Context) featureMsg {
			trace, err := loader.Trace(ctx, snapshot.Repository, job)
			return featureMsg{lines: ci.Diagnose(clean(trace.Text)), err: err}
		}), true
	case "H", "c":
		loader := m.provider
		return m.featureCommand(historyScreen, func(ctx context.Context) featureMsg {
			history, err := provider.History(ctx, snapshot.Repository, *snapshot.Pipeline)
			if err != nil {
				return featureMsg{err: err}
			}
			if key == "c" {
				for _, previous := range history {
					if previous.ID < snapshot.Pipeline.ID {
						before, err := loader.Load(ctx, previous.ID)
						if err != nil {
							return featureMsg{err: err}
						}
						return featureMsg{lines: comparisonLines(ci.Compare(before, snapshot))}
					}
				}
				return featureMsg{err: fmt.Errorf("предыдущий запуск не найден")}
			}
			return featureMsg{history: history}
		}), true
	case "x":
		return m.featureCommand(artifactScreen, func(ctx context.Context) featureMsg {
			items, err := provider.Artifacts(ctx, snapshot)
			return featureMsg{artifacts: items, err: err}
		}), true
	case "t":
		return m.featureCommand(reportScreen, func(ctx context.Context) featureMsg {
			report, err := provider.Report(ctx, snapshot)
			return featureMsg{lines: reportLines(report), err: err}
		}), true
	case "R", "F", "s":
		if snapshot.CacheError != "" || m.err != nil {
			m.actionErr = fmt.Errorf("сначала обновите данные: r")
			return nil, true
		}
		if key == "s" && snapshot.Provider == ci.GitHub {
			return m.featureCommand(workflowScreen, func(ctx context.Context) featureMsg {
				items, err := provider.Workflows(ctx, snapshot.Repository)
				return featureMsg{workflows: items, err: err}
			}), true
		}
		mutation := ci.Mutation{Pipeline: *snapshot.Pipeline, Job: job}
		switch key {
		case "R":
			if !selected || !job.Status.Finished() {
				m.actionErr = fmt.Errorf("выберите завершённую job")
				return nil, true
			}
			mutation.Action = ci.RetryJob
		case "F":
			if snapshot.Pipeline.Status != ci.Failed {
				m.actionErr = fmt.Errorf("пайплайн не упал")
				return nil, true
			}
			mutation.Action = ci.RetryFailed
		case "s":
			if !selected || job.Status != ci.Manual {
				m.actionErr = fmt.Errorf("выберите manual job")
				return nil, true
			}
			mutation.Action = ci.PlayJob
		}
		m.confirm(mutation, snapshot.Repository)
		return nil, true
	}
	return nil, false
}
func (m *Model) confirm(mutation ci.Mutation, repo repository.Repository) {
	m.cancelFeature()
	m.feature.mutation, m.feature.repo = mutation, repo
	m.feature.err = nil
	m.feature.lines = nil
	m.screen = confirmScreen
}
func (m *Model) featureKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "esc" {
		m.cancelFeature()
		m.feature = featureState{generation: m.feature.generation}
		m.screen = stageScreen
		return nil
	}
	if key == "q" && m.screen != inputScreen {
		m.close()
		return tea.Quit
	}
	if m.feature.loading {
		return nil
	}
	if m.screen == confirmScreen {
		if key != "y" {
			return nil
		}
		mutation, repo, provider, loader := m.feature.mutation, m.feature.repo, m.options.Features, m.provider
		return m.featureCommand(confirmScreen, func(ctx context.Context) featureMsg {
			if err := provider.Mutate(ctx, repo, mutation); err != nil {
				return featureMsg{err: err}
			}
			if cache, ok := loader.(interface {
				Invalidate(context.Context, repository.Repository) error
			}); ok {
				if err := cache.Invalidate(ctx, repo); err != nil {
					return featureMsg{mutated: true, err: fmt.Errorf("действие выполнено, кеш не обновлён: %w", err)}
				}
			}
			return featureMsg{mutated: true}
		})
	}
	if m.screen == inputScreen {
		switch key {
		case "backspace":
			if len(m.feature.input) > 0 {
				_, size := utf8.DecodeLastRuneInString(m.feature.input)
				m.feature.input = m.feature.input[:len(m.feature.input)-size]
			}
		case "enter":
			inputs := map[string]string{}
			if strings.TrimSpace(m.feature.input) != "" {
				if err := json.Unmarshal([]byte(m.feature.input), &inputs); err != nil {
					m.feature.err = fmt.Errorf("inputs: нужен JSON-объект со строковыми значениями")
					return nil
				}
			}
			mutation := m.feature.mutation
			mutation.Inputs = inputs
			m.confirm(mutation, m.snapshot.Repository)
		default:
			if len(m.feature.input)+len(msg.Text) <= 8192 {
				m.feature.input += msg.Text
			}
		}
		return nil
	}
	length := len(m.feature.lines)
	switch m.screen {
	case toolsScreen:
		length = len(toolKeys)
	case historyScreen:
		if m.feature.lines == nil {
			length = len(m.feature.history)
		}
	case artifactScreen:
		length = len(m.feature.artifacts)
	case workflowScreen:
		length = len(m.feature.workflows)
	}
	if len(m.feature.lines) > 0 {
		length = len(m.feature.lines)
	}
	switch key {
	case "up", "k":
		m.feature.row = max(0, m.feature.row-1)
	case "down", "j":
		m.feature.row = min(max(0, length-1), m.feature.row+1)
	case "pgup":
		m.feature.row = max(0, m.feature.row-max(1, m.height-10))
	case "pgdown":
		m.feature.row = min(max(0, length-1), m.feature.row+max(1, m.height-10))
	case "home":
		m.feature.row = 0
	case "end":
		m.feature.row = max(0, length-1)
	case "enter", "c":
		index := m.feature.row
		switch m.screen {
		case toolsScreen:
			command, _ := m.openFeature(toolKeys[index])
			return command
		case diagnosticScreen:
			for i, stage := range m.snapshot.Stages {
				for j, job := range stage.Jobs {
					if job.ID == m.feature.job.ID {
						m.stage, m.row = i, j
						m.screen = logScreen
						m.clearTrace()
						return m.loadTrace()
					}
				}
			}
		case historyScreen:
			if len(m.feature.history) == 0 || index >= len(m.feature.history) {
				return nil
			}
			previous := m.feature.history[index]
			if key == "enter" {
				m.options.PipelineID = previous.ID
				m.screen = stageScreen
				m.cancelFeature()
				return m.load()
			}
			loader, snapshot := m.provider, m.snapshot
			return m.featureCommand(compareScreen, func(ctx context.Context) featureMsg {
				before, err := loader.Load(ctx, previous.ID)
				if err != nil {
					return featureMsg{err: err}
				}
				return featureMsg{lines: comparisonLines(ci.Compare(before, snapshot))}
			})
		case artifactScreen:
			if index >= len(m.feature.artifacts) {
				return nil
			}
			item, repo, provider, directory := m.feature.artifacts[index], m.feature.snapshot.Repository, m.options.Features, m.options.DownloadDirectory
			return m.featureCommand(artifactScreen, func(ctx context.Context) featureMsg {
				path, err := provider.Download(ctx, repo, item, directory)
				return featureMsg{lines: []string{"Сохранено", path}, err: err}
			})
		case workflowScreen:
			if index >= len(m.feature.workflows) {
				return nil
			}
			m.feature.mutation = ci.Mutation{Action: ci.DispatchWorkflow, Workflow: m.feature.workflows[index]}
			m.feature.input = ""
			m.feature.err = nil
			m.screen = inputScreen
		}
	default:
		if m.screen == toolsScreen {
			command, handled := m.openFeature(key)
			if handled {
				return command
			}
		}
	}
	return nil
}

var toolKeys = []string{"d", "H", "c", "x", "t", "R", "F", "s"}
var toolLabels = []string{"Найти сообщения об ошибках в логе", "История запусков этой ветки", "Сравнить с предыдущим запуском", "Скачать артефакты", "Тесты и покрытие", "Повторить выбранную job", "Повторить все упавшие jobs", "Запустить manual job / workflow"}

func (m *Model) featureView(width int) string {
	p := m.colors()
	title := "Действия"
	switch m.screen {
	case diagnosticScreen:
		title = "Разбор ошибки"
	case historyScreen:
		title = "История запусков"
	case compareScreen:
		title = "Сравнение запусков"
	case artifactScreen:
		title = "Артефакты"
	case reportScreen:
		title = "Тесты и покрытие"
	case confirmScreen:
		title = "Подтверждение действия"
	case workflowScreen:
		title = "Запуск workflow"
	case inputScreen:
		title = "Параметры workflow"
	}
	lines := []string{lipgloss.NewStyle().Foreground(p.foreground).Bold(true).Render(title), paint(clip(oneLine(m.feature.snapshot.Repository.Project+" · "+m.feature.snapshot.Repository.Branch), width), p.muted), paint(strings.Repeat("─", width), p.line), ""}
	rows := []string{}
	selectable := false
	switch m.screen {
	case toolsScreen:
		selectable = true
		for i, label := range toolLabels {
			rows = append(rows, fmt.Sprintf("%-3s %s", toolKeys[i], label))
		}
	case historyScreen:
		selectable = true
		for _, pipeline := range m.feature.history {
			rows = append(rows, fmt.Sprintf("#%-10d %-18s %s  ·  %s", pipeline.ID, statusLabel(pipeline.Status), elapsed(pipeline.StartedAt, pipeline.FinishedAt, pipeline.Duration, m.now), pipeline.SHA[:min(8, len(pipeline.SHA))]))
		}
	case artifactScreen:
		selectable = true
		for _, item := range m.feature.artifacts {
			label := fmt.Sprintf("%s  ·  %.1f MiB", item.Name, float64(item.Size)/(1<<20))
			if item.Expired {
				label += "  ·  истёк"
			}
			rows = append(rows, label)
		}
	case workflowScreen:
		selectable = true
		for _, workflow := range m.feature.workflows {
			rows = append(rows, workflow.Name+"  ·  "+workflow.Path)
		}
	case inputScreen:
		rows = []string{"Inputs в формате JSON, пустая строка — значения по умолчанию.", "Пример: {\"environment\":\"staging\"}", "", m.feature.input + "▏"}
	case confirmScreen:
		mutation := m.feature.mutation
		label := ""
		switch mutation.Action {
		case ci.RetryJob:
			label = "Повторить job: " + mutation.Job.Name
		case ci.RetryFailed:
			label = "Повторить все упавшие jobs"
		case ci.PlayJob:
			label = "Запустить manual job: " + mutation.Job.Name
		case ci.DispatchWorkflow:
			label = "Запустить workflow: " + mutation.Workflow.Name
		}
		rows = []string{label, "", "Ветка: " + m.feature.repo.Branch, "SHA: " + m.feature.repo.SHA, fmt.Sprintf("Запуск: #%d", mutation.Pipeline.ID)}
		if m.snapshot.Provider == ci.GitHub && mutation.Action == ci.RetryJob {
			rows = append(rows, "GitHub также повторит зависимые jobs.")
		}
		if len(mutation.Inputs) > 0 {
			data, err := json.Marshal(mutation.Inputs)
			if err != nil {
				rows = append(rows, err.Error())
			} else {
				rows = append(rows, "Inputs: "+string(data))
			}
		}
	}
	if len(m.feature.lines) > 0 {
		rows = m.feature.lines
		selectable = false
	}
	if m.feature.loading {
		rows = []string{m.icon(ci.Running) + "  Загрузка…"}
		selectable = false
	}
	if len(rows) == 0 {
		rows = []string{"Нет данных"}
	}
	height := max(1, m.height-9)
	start := max(0, m.feature.row-height+1)
	for i := range height {
		line := ""
		if index := start + i; index < len(rows) {
			line = clip(oneLine(rows[index]), width-2)
			if selectable && index == m.feature.row {
				line = paint("› "+line, p.accent)
			} else {
				line = "  " + line
			}
		}
		lines = append(lines, line)
	}
	problem := ""
	if m.feature.err != nil {
		problem = paint(clip(oneLine(m.feature.err.Error()), width), p.red)
	}
	hint := m.keyHint("esc", "назад") + "   " + m.keyHint("↑↓", "выбор / прокрутка")
	switch m.screen {
	case confirmScreen:
		hint = m.keyHint("y", "подтвердить") + "   " + m.keyHint("esc", "отменить")
	case historyScreen:
		hint += "   " + m.keyHint("enter", "открыть") + "   " + m.keyHint("c", "сравнить")
	case artifactScreen:
		hint += "   " + m.keyHint("enter", "скачать")
	case diagnosticScreen:
		hint += "   " + m.keyHint("enter", "лог")
	case workflowScreen, inputScreen, toolsScreen:
		hint += "   " + m.keyHint("enter", "выбрать")
	}
	lines = append(lines, problem, "", clip(hint, width))
	return lipgloss.NewStyle().Padding(0, 2).Render(strings.Join(lines, "\n"))
}
func reportLines(report ci.Report) []string {
	lines := []string{fmt.Sprintf("Тесты  %d  ·  успешно %d  ·  ошибки %d  ·  пропущено %d", report.Total, report.Passed, report.Failed, report.Skipped), fmt.Sprintf("Время  %.2f s", report.Duration)}
	if report.Coverage != nil {
		lines = append(lines, fmt.Sprintf("Покрытие строк  %.2f%%", *report.Coverage))
	}
	if report.Notice != "" {
		lines = append(lines, "", report.Notice)
	}
	for _, failure := range report.Failures {
		lines = append(lines, "", failure.Suite+" · "+failure.Name)
		lines = append(lines, strings.Split(clean(failure.Message), "\n")...)
	}
	return lines
}
func comparisonLines(comparison ci.Comparison) []string {
	lines := []string{fmt.Sprintf("#%d → #%d", comparison.Before.ID, comparison.After.ID), "JOB  ·  СТАТУС  ·  ВРЕМЯ  ·  Δ", ""}
	for _, job := range comparison.Jobs {
		before, after := statusLabel(job.Before), statusLabel(job.After)
		if job.Before == "" {
			before = "новая"
		}
		if job.After == "" {
			after = "удалена"
		}
		lines = append(lines, fmt.Sprintf("%s / %s  ·  %s → %s  ·  %.0fs → %.0fs  ·  %+.0fs", job.Stage, job.Name, before, after, job.BeforeDuration, job.AfterDuration, job.AfterDuration-job.BeforeDuration))
	}
	return lines
}
