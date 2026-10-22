package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

type Provider interface {
	Load(context.Context, int64) (ci.Snapshot, error)
	Trace(context.Context, repository.Repository, ci.Job) (ci.Trace, error)
}

type Options struct {
	Interval          time.Duration
	Theme             string
	Icons             string
	Motion            bool
	PipelineID        int64
	Worktrees         []repository.Worktree
	Directory         string
	ChooseWorktree    bool
	SwitchRepository  func(context.Context, string) (Provider, error)
	OpenChange        func(context.Context, repository.Repository) error
	Features          FeatureProvider
	DownloadDirectory string
}

type screen uint8

const (
	stageScreen screen = iota
	logScreen
	worktreeScreen
	toolsScreen
	historyScreen
	diagnosticScreen
	artifactScreen
	reportScreen
	compareScreen
	confirmScreen
	workflowScreen
	inputScreen
)

type Model struct {
	ctx              context.Context
	provider         Provider
	options          Options
	snapshot         ci.Snapshot
	width, height    int
	stage, row       int
	screen           screen
	dark             bool
	frame            int
	now              time.Time
	loading          bool
	err              error
	failures         int
	generation       uint64
	loadCancel       context.CancelFunc
	traceGeneration  uint64
	traceCancel      context.CancelFunc
	traceLoading     bool
	trace            ci.Trace
	traceErr         error
	lines            []string
	offset           int
	horizontal       int
	follow           bool
	searching        bool
	query            string
	searchNotice     string
	worktreeQuery    string
	worktreeRow      int
	worktreeLoading  bool
	previousScreen   screen
	actionErr        error
	actionGeneration uint64
	actionLoading    bool
	feature          featureState
}

type loadMsg struct {
	snapshot   ci.Snapshot
	err        error
	generation uint64
}
type traceMsg struct {
	trace      ci.Trace
	err        error
	generation uint64
}
type pollMsg uint64
type tracePollMsg uint64
type frameMsg time.Time

func New(ctx context.Context, provider Provider, options Options) *Model {
	m := &Model{ctx: ctx, provider: provider, options: options, width: 90, height: 28, dark: options.Theme != "light", now: time.Now(), follow: true}
	if options.ChooseWorktree && len(options.Worktrees) > 1 {
		m.screen = worktreeScreen
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	if m.screen == worktreeScreen {
		return tea.Batch(m.tick(), tea.RequestBackgroundColor)
	}
	return tea.Batch(m.load(), m.tick(), tea.RequestBackgroundColor)
}

func (m *Model) tick() tea.Cmd {
	delay := time.Second
	if m.options.Motion {
		delay = 120 * time.Millisecond
	}
	return tea.Tick(delay, func(t time.Time) tea.Msg { return frameMsg(t) })
}

func (m *Model) load() tea.Cmd { return m.loadWithRefresh(false) }

func (m *Model) loadWithRefresh(force bool) tea.Cmd {
	if m.loadCancel != nil {
		m.loadCancel()
	}
	ctx, cancel := context.WithTimeout(m.ctx, 90*time.Second)
	m.loadCancel = cancel
	m.generation++
	generation, pipelineID, provider := m.generation, m.options.PipelineID, m.provider
	m.loading = true
	return func() tea.Msg {
		defer cancel()
		var snapshot ci.Snapshot
		var err error
		if refresher, ok := provider.(interface {
			Refresh(context.Context, int64) (ci.Snapshot, error)
		}); force && ok {
			snapshot, err = refresher.Refresh(ctx, pipelineID)
		} else {
			snapshot, err = provider.Load(ctx, pipelineID)
		}
		return loadMsg{snapshot: snapshot, err: err, generation: generation}
	}
}

func (m *Model) loadTrace() tea.Cmd {
	job, ok := m.selected()
	if !ok {
		return nil
	}
	if m.traceCancel != nil {
		m.traceCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.traceCancel = cancel
	m.traceGeneration++
	generation, repo, provider := m.traceGeneration, m.snapshot.Repository, m.provider
	m.traceLoading = true
	return func() tea.Msg {
		defer cancel()
		trace, err := provider.Trace(ctx, repo, job)
		return traceMsg{trace: trace, err: err, generation: generation}
	}
}

func (m *Model) selected() (ci.Job, bool) {
	if m.stage >= len(m.snapshot.Stages) {
		return ci.Job{}, false
	}
	jobs := m.snapshot.Stages[m.stage].Jobs
	if m.row >= len(jobs) {
		return ci.Job{}, false
	}
	return jobs[m.row], true
}

func (m *Model) restoreSelection(previous ci.Job) {
	for i, stage := range m.snapshot.Stages {
		for j, job := range stage.Jobs {
			if job.ID == previous.ID {
				m.stage, m.row = i, j
				return
			}
		}
	}
	for i, stage := range m.snapshot.Stages {
		for j, job := range stage.Jobs {
			if job.Name == previous.Name && job.Stage == previous.Stage {
				m.stage, m.row = i, j
				return
			}
		}
	}
	m.stage, m.row = 0, 0
	for i, stage := range m.snapshot.Stages {
		for j, job := range stage.Jobs {
			if job.Status == ci.Failed || job.Status == ci.Running {
				m.stage, m.row = i, j
				return
			}
		}
	}
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case featureMsg:
		return m, m.featureUpdate(msg)
	case changeOpenedMsg:
		if msg.generation == m.actionGeneration {
			m.actionErr = msg.err
			m.actionLoading = false
		}
	case worktreeSelectedMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		m.worktreeLoading = false
		m.actionErr = msg.err
		if msg.err == nil {
			m.provider = msg.provider
			m.options.Directory = msg.directory
			m.options.PipelineID = 0
			m.snapshot = ci.Snapshot{Repository: repository.Repository{Directory: msg.directory}}
			m.err = nil
			m.screen = stageScreen
			m.clearTrace()
			return m, m.load()
		}
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case tea.BackgroundColorMsg:
		if m.options.Theme == "auto" {
			m.dark = msg.IsDark()
		}
	case frameMsg:
		m.now = time.Time(msg)
		if m.options.Motion {
			m.frame++
		}
		return m, m.tick()
	case pollMsg:
		if uint64(msg) == m.generation {
			return m, m.load()
		}
	case tracePollMsg:
		if uint64(msg) == m.traceGeneration && m.screen == logScreen {
			return m, m.loadTrace()
		}
	case loadMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		m.loading = false
		m.err = msg.err
		delay := m.options.Interval
		if msg.snapshot.Refreshing {
			delay = 250 * time.Millisecond
		}
		var traceCmd tea.Cmd
		if msg.err == nil {
			previous, _ := m.selected()
			m.snapshot = msg.snapshot
			m.restoreSelection(previous)
			m.failures = 0
			selected, ok := m.selected()
			if m.screen == logScreen && (!ok || selected.ID != previous.ID) {
				m.clearTrace()
				if ok {
					traceCmd = m.loadTrace()
				} else {
					m.screen = stageScreen
				}
			} else if m.screen == logScreen && selected.Status.Finished() != previous.Status.Finished() {
				traceCmd = m.loadTrace()
			}
		} else {
			m.failures++
			delay = min(time.Minute, m.options.Interval*time.Duration(1<<min(m.failures, 4)))
		}
		generation := m.generation
		return m, tea.Batch(traceCmd, tea.Tick(delay, func(time.Time) tea.Msg { return pollMsg(generation) }))
	case traceMsg:
		if msg.generation != m.traceGeneration || m.screen != logScreen {
			return m, nil
		}
		m.traceLoading = false
		m.traceErr = msg.err
		if msg.err == nil {
			m.trace = msg.trace
			m.lines = strings.Split(clean(msg.trace.Text), "\n")
			m.offset = min(m.offset, max(0, len(m.lines)-m.logHeight()))
		}
		job, ok := m.selected()
		if ok && (!job.Status.Finished() || msg.err != nil) {
			generation := m.traceGeneration
			delay := m.options.Interval
			if msg.err != nil {
				delay = max(delay, 15*time.Second)
			}
			return m, tea.Tick(delay, func(time.Time) tea.Msg { return tracePollMsg(generation) })
		}
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) clearTrace() {
	if m.traceCancel != nil {
		m.traceCancel()
	}
	m.traceGeneration++
	m.trace, m.traceErr = ci.Trace{}, nil
	m.lines = nil
	m.offset, m.horizontal = 0, 0
	m.follow = true
	m.traceLoading = false
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		m.close()
		return tea.Quit
	}
	if m.screen == worktreeScreen {
		return m.worktreeKey(msg)
	}
	if m.screen >= toolsScreen {
		return m.featureKey(msg)
	}
	if m.screen == stageScreen {
		if command, handled := m.openFeature(key); handled {
			return command
		}
	}
	if !m.searching && key == "w" && len(m.options.Worktrees) > 1 && m.options.SwitchRepository != nil {
		m.previousScreen = m.screen
		m.screen = worktreeScreen
		m.worktreeQuery, m.worktreeRow = "", 0
		for index, tree := range m.options.Worktrees {
			if tree.Directory == m.snapshot.Repository.Directory {
				m.worktreeRow = index
				break
			}
		}
		m.actionErr = nil
		return nil
	}
	if !m.searching && key == "m" && m.options.OpenChange != nil && m.snapshot.Repository.Project != "" {
		if m.actionLoading {
			return nil
		}
		m.actionErr = nil
		m.actionGeneration++
		m.actionLoading = true
		generation, repo, open := m.actionGeneration, m.snapshot.Repository, m.options.OpenChange
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
			defer cancel()
			return changeOpenedMsg{generation: generation, err: open(ctx, repo)}
		}
	}
	if m.searching {
		switch key {
		case "esc":
			m.searching = false
		case "enter":
			m.searching = false
			m.find(false)
		case "backspace":
			if m.query != "" {
				_, size := utf8.DecodeLastRuneInString(m.query)
				m.query = m.query[:len(m.query)-size]
			}
		default:
			if msg.Text != "" {
				m.query += msg.Text
			}
		}
		return nil
	}
	if key == "q" {
		m.close()
		return tea.Quit
	}
	if key == "esc" {
		if m.screen == logScreen {
			m.screen = stageScreen
			m.clearTrace()
			return nil
		}
		m.close()
		return tea.Quit
	}
	if m.screen == logScreen {
		switch key {
		case "up", "k":
			m.scroll(-1)
		case "down", "j":
			m.scroll(1)
		case "pgup", "ctrl+u":
			m.scroll(-m.logHeight())
		case "pgdown", "ctrl+d":
			m.scroll(m.logHeight())
		case "home", "g":
			m.follow = false
			m.offset = 0
		case "end", "G":
			m.follow = true
		case "left", "h":
			m.horizontal = max(0, m.horizontal-8)
		case "right", "l":
			m.horizontal += 8
		case "f":
			if m.follow {
				m.scroll(0)
			} else {
				m.follow = true
			}
		case "/":
			m.searching = true
			m.query = ""
			m.searchNotice = ""
		case "n":
			m.find(true)
		case "r":
			return m.loadTrace()
		}
		return nil
	}
	switch key {
	case "r":
		return m.loadWithRefresh(true)
	case "p":
		if len(m.snapshot.Candidates) > 1 && m.snapshot.Pipeline != nil {
			index := 0
			for i, pipeline := range m.snapshot.Candidates {
				if pipeline.ID == m.snapshot.Pipeline.ID {
					index = (i + 1) % len(m.snapshot.Candidates)
					break
				}
			}
			m.options.PipelineID = m.snapshot.Candidates[index].ID
			return m.load()
		}
	case "a":
		m.options.PipelineID = 0
		return m.load()
	case "left", "h":
		m.stage = max(0, m.stage-1)
		m.clampRow()
	case "right", "l":
		m.stage = min(max(0, len(m.snapshot.Stages)-1), m.stage+1)
		m.clampRow()
	case "up", "k":
		m.row = max(0, m.row-1)
	case "down", "j":
		m.row++
		m.clampRow()
	case "enter":
		if _, ok := m.selected(); ok {
			m.screen = logScreen
			m.clearTrace()
			return m.loadTrace()
		}
	}
	return nil
}

func (m *Model) close() {
	m.cancelFeature()
	if m.loadCancel != nil {
		m.loadCancel()
	}
	if m.traceCancel != nil {
		m.traceCancel()
	}
}

func (m *Model) clampRow() {
	if m.stage < len(m.snapshot.Stages) {
		m.row = min(m.row, max(0, len(m.snapshot.Stages[m.stage].Jobs)-1))
	}
}

func (m *Model) logHeight() int { return max(1, m.height-10) }

func (m *Model) scroll(delta int) {
	if m.follow {
		m.offset = max(0, len(m.lines)-m.logHeight())
	}
	m.follow = false
	m.offset = max(0, min(max(0, len(m.lines)-m.logHeight()), m.offset+delta))
}

func (m *Model) find(next bool) {
	if m.query == "" || len(m.lines) == 0 {
		return
	}
	start := m.offset
	if next {
		start++
	}
	for i := range len(m.lines) {
		index := (start + i) % len(m.lines)
		if strings.Contains(strings.ToLower(m.lines[index]), strings.ToLower(m.query)) {
			m.follow = false
			m.offset = index
			m.horizontal = 0
			m.searchNotice = fmt.Sprintf("строка %d", index+1)
			return
		}
	}
	m.searchNotice = "не найдено"
}
