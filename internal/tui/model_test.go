package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

type fixtureProvider struct {
	text          string
	completedText string
	jobs          []int64
}

func (p *fixtureProvider) Load(context.Context, int64) (ci.Snapshot, error) {
	return fixtureSnapshot(), nil
}
func (p *fixtureProvider) Trace(_ context.Context, _ repository.Repository, job ci.Job) (ci.Trace, error) {
	p.jobs = append(p.jobs, job.ID)
	if job.Status.Finished() && p.completedText != "" {
		return ci.Trace{Text: p.completedText}, nil
	}
	return ci.Trace{Text: p.text}, nil
}

func fixtureSnapshot() ci.Snapshot {
	return ci.Snapshot{Repository: repository.Repository{Project: "team/app", Branch: "feature/test", SHA: "a8c43f1029"}, Pipeline: &ci.Pipeline{ID: 20, Status: ci.Running, Source: "push"}, UpdatedAt: time.Now(), Stages: []ci.Stage{{Name: "build", Jobs: []ci.Job{{ID: 1, Name: "image", Stage: "build", Status: ci.Success}}}, {Name: "test", Jobs: []ci.Job{{ID: 2, Name: "integration", Stage: "test", Status: ci.Running}, {ID: 3, Name: "unit", Stage: "test", Status: ci.Pending}}}}}
}

func ready(t *testing.T, p *fixtureProvider) *Model {
	t.Helper()
	m := New(t.Context(), p, Options{Interval: 5 * time.Second, Theme: "dark", Icons: "unicode"})
	m.Update(loadMsg{snapshot: fixtureSnapshot()})
	return m
}

func press(m *Model, code rune, text string) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	return cmd
}

func view(m *Model) string { return ansi.Strip(m.View().Content) }

func TestEnterLoadsSelectedJobAndEscapePreservesSelection(t *testing.T) {
	p := &fixtureProvider{text: "line one\nline two"}
	m := ready(t, p)
	press(m, tea.KeyDown, "")
	cmd := press(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("missing trace command")
	}
	m.Update(cmd())
	if len(p.jobs) != 1 || p.jobs[0] != 3 || !strings.Contains(view(m), "ЛОГ  unit") || !strings.Contains(view(m), "line one") {
		t.Fatalf("jobs=%v view=%s", p.jobs, view(m))
	}
	press(m, tea.KeyEscape, "")
	if strings.Contains(view(m), "ЛОГ") || !strings.Contains(view(m), "unit  ·  в очереди") {
		t.Fatal(view(m))
	}
	if cmd := press(m, tea.KeyEscape, ""); cmd == nil {
		t.Fatal("missing quit command")
	}
}

func TestRefreshPreservesJobAndIgnoresStaleResponses(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	press(m, tea.KeyDown, "")
	snapshot := fixtureSnapshot()
	snapshot.Stages[1].Jobs = []ci.Job{snapshot.Stages[1].Jobs[1], snapshot.Stages[1].Jobs[0]}
	m.Update(loadMsg{snapshot: snapshot})
	if !strings.Contains(view(m), "unit  ·  в очереди") {
		t.Fatal(view(m))
	}
	press(m, 'r', "r")
	m.Update(loadMsg{snapshot: ci.Snapshot{}, generation: 0})
	if !strings.Contains(view(m), "unit  ·  в очереди") {
		t.Fatal("stale response replaced selection")
	}
}

func TestClosedLogRejectsLateResponse(t *testing.T) {
	m := ready(t, &fixtureProvider{text: "late trace"})
	cmd := press(m, tea.KeyEnter, "")
	press(m, tea.KeyEscape, "")
	m.Update(cmd())
	if strings.Contains(view(m), "late trace") || strings.Contains(view(m), "ЛОГ") {
		t.Fatal(view(m))
	}
}

func TestFinishingJobFetchesFinalLogAndRejectsEarlierLiveResponse(t *testing.T) {
	p := &fixtureProvider{text: "live steps", completedText: "final job log"}
	m := ready(t, p)
	previous := press(m, tea.KeyEnter, "")
	snapshot := fixtureSnapshot()
	snapshot.Stages[1].Jobs[0].Status = ci.Success
	_, cmd := m.Update(loadMsg{snapshot: snapshot})
	if cmd == nil {
		t.Fatal("missing final log fetch")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatal("missing final log command")
	}
	m.Update(previous())
	if strings.Contains(view(m), "live steps") {
		t.Fatal("late live response replaced final fetch")
	}
	m.Update(batch[0]())
	if !strings.Contains(view(m), "final job log") {
		t.Fatal(view(m))
	}
}

func TestSearchScrollAndFollow(t *testing.T) {
	var lines []string
	for i := range 80 {
		lines = append(lines, fmt.Sprintf("output line %02d", i))
	}
	m := ready(t, &fixtureProvider{text: strings.Join(lines, "\n")})
	cmd := press(m, tea.KeyEnter, "")
	m.Update(cmd())
	if !strings.Contains(view(m), "output line 79") {
		t.Fatal("not following tail")
	}
	press(m, 'f', "f")
	if !strings.Contains(view(m), "output line 79") || strings.Contains(view(m), "output line 00") {
		t.Fatal("pausing follow moved the viewport")
	}
	m.Update(traceMsg{trace: ci.Trace{Text: strings.Join(append(lines, "new output"), "\n")}, generation: m.traceGeneration})
	if !strings.Contains(view(m), "output line 79") || strings.Contains(view(m), "new output") {
		t.Fatal("paused viewport followed new output")
	}
	press(m, tea.KeyHome, "")
	if !strings.Contains(view(m), "output line 00") || strings.Contains(view(m), "output line 79") {
		t.Fatal("home did not scroll")
	}
	press(m, '/', "/")
	for _, r := range "line 40" {
		press(m, r, string(r))
	}
	press(m, tea.KeyEnter, "")
	if !strings.Contains(view(m), "output line 40") || !strings.Contains(view(m), "строка 41") {
		t.Fatal(view(m))
	}
	press(m, 'f', "f")
	if !strings.Contains(view(m), "output line 79") {
		t.Fatal("follow did not resume")
	}
}

func TestTraceDiscardsTerminalControlSequences(t *testing.T) {
	text := "\x1b]52;c;private\a\x1b[2J\x1b[31mred\x1b[0m\nold\rnew\n\u202Ereversed\x00"
	m := ready(t, &fixtureProvider{text: text})
	cmd := press(m, tea.KeyEnter, "")
	m.Update(cmd())
	content := m.View().Content
	for _, forbidden := range []string{"\x1b]52", "\x1b[2J", "\u202E", "\x00", "private", "old"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("unsafe content %q", forbidden)
		}
	}
	for _, want := range []string{"red", "new", "reversed"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestViewsFitTerminalAndKeepNavigationVisible(t *testing.T) {
	for _, size := range [][2]int{{32, 12}, {32, 16}, {40, 16}, {82, 16}, {140, 16}, {80, 24}, {120, 35}, {140, 34}, {12, 5}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := ready(t, &fixtureProvider{text: strings.Repeat("very long line ", 100)})
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, logs := range []bool{false, true} {
				if logs {
					cmd := press(m, tea.KeyEnter, "")
					m.Update(cmd())
				}
				content := m.View().Content
				lines := strings.Split(content, "\n")
				if len(lines) > size[1] {
					t.Fatalf("height %d > %d", len(lines), size[1])
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("width=%d line=%q", ansi.StringWidth(line), line)
					}
				}
				if size[0] >= 32 && size[1] >= 16 && !strings.Contains(ansi.Strip(content), "esc") {
					t.Fatal("navigation was cropped")
				}
			}
		})
	}
}

func TestLongJobNamesDoNotWrapOrHideNavigation(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	snapshot := fixtureSnapshot()
	for i := range 50 {
		snapshot.Stages[1].Jobs = append(snapshot.Stages[1].Jobs, ci.Job{ID: int64(i + 10), Name: strings.Repeat("long_job_name_", 8), Stage: "test", Status: ci.Success})
	}
	m.Update(loadMsg{snapshot: snapshot})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	content := view(m)
	if !strings.Contains(content, "esc выйти") || !strings.Contains(content, "JOBS · 52") {
		t.Fatal(content)
	}
}

func TestGitHubViewShowsWorkflowControlsWithoutStageNavigation(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	snapshot := fixtureSnapshot()
	snapshot.Provider = ci.GitHub
	snapshot.Stages = snapshot.Stages[1:]
	snapshot.Stages[0].Name = "Jobs"
	snapshot.Pipeline.Name = "Tests"
	snapshot.Candidates = []ci.Pipeline{{ID: 20, Name: "Tests"}, {ID: 21, Name: "Lint"}}
	m.Update(loadMsg{snapshot: snapshot})
	content := view(m)
	if !strings.Contains(content, "JOBS · 2") || !strings.Contains(content, "p workflow") || !strings.Contains(content, "2 workflows") {
		t.Fatal(content)
	}
	if strings.Contains(content, "←→ стадия") || strings.Contains(content, "01 JOBS") {
		t.Fatal("GitHub jobs show stage navigation")
	}
}

func TestStageAndJobSelectionStayVisibleWhenScrolling(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	snapshot := fixtureSnapshot()
	for i := 2; i < 7; i++ {
		snapshot.Stages = append(snapshot.Stages, ci.Stage{Name: fmt.Sprintf("stage-%d", i), Jobs: []ci.Job{{ID: int64(100 + i), Name: fmt.Sprintf("selected-job-%d", i), Status: ci.Pending}}})
	}
	m.Update(loadMsg{snapshot: snapshot})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	for range 6 {
		press(m, tea.KeyRight, "")
	}
	content := view(m)
	if !strings.Contains(content, "07 STAGE-6") || !strings.Contains(content, "›○ selected-job-6") || !strings.Contains(content, "7 стадий") {
		t.Fatal(content)
	}
}

func TestThemesAvoidBackgroundFillsAndTerminalColorChanges(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		m := New(t.Context(), &fixtureProvider{}, Options{Theme: theme})
		m.Update(loadMsg{snapshot: fixtureSnapshot()})
		rendered := m.View()
		if rendered.BackgroundColor != nil || rendered.ForegroundColor != nil {
			t.Fatal("view changes terminal color defaults")
		}
		if strings.Contains(rendered.Content, "48;") {
			t.Fatal("view contains a background fill")
		}
	}
}

func TestAPIErrorKeepsLastSuccessfulData(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	m.Update(loadMsg{err: errors.New("HTTP 429")})
	content := view(m)
	if !strings.Contains(content, "team/app") || !strings.Contains(content, "Данные устарели") || !strings.Contains(content, "429") {
		t.Fatal(content)
	}
}

func TestLogShowsPipelineRefreshErrorsWithoutLosingTrace(t *testing.T) {
	m := ready(t, &fixtureProvider{text: "saved output"})
	cmd := press(m, tea.KeyEnter, "")
	m.Update(cmd())
	m.Update(loadMsg{err: errors.New("HTTP 500")})
	content := view(m)
	if !strings.Contains(content, "saved output") || !strings.Contains(content, "Данные устарели") || !strings.Contains(content, "500") {
		t.Fatal(content)
	}
}

func TestEmptyPipelineAndManualOnlyPipelineRender(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	m.Update(loadMsg{snapshot: ci.Snapshot{Notice: "Нет пайплайнов"}})
	if !strings.Contains(view(m), "Нет пайплайнов") {
		t.Fatal(view(m))
	}
	snapshot := fixtureSnapshot()
	snapshot.Stages = []ci.Stage{{Name: "deploy", Jobs: []ci.Job{{ID: 1, Name: "release", Status: ci.Manual}}}}
	m.Update(loadMsg{snapshot: snapshot})
	if !strings.Contains(view(m), "0/0 завершено") || !strings.Contains(view(m), "1 ручных") {
		t.Fatal(view(m))
	}
}
