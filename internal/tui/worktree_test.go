package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"tmux-gitlab-pipeline/internal/repository"
)

func TestWorktreeChooserFiltersAndSwitchesBeforeLoadingPipeline(t *testing.T) {
	selected := ""
	p := &fixtureProvider{}
	m := New(t.Context(), p, Options{Theme: "dark", Interval: 5 * time.Second, ChooseWorktree: true, PipelineID: 999, Worktrees: []repository.Worktree{{Directory: "/repo", Branch: "master"}, {Directory: "/repo/.claude/worktrees/feature-db-cleanup", Branch: "feature/database-cleanup"}}, SwitchRepository: func(_ context.Context, directory string) (Provider, error) {
		selected = directory
		return p, nil
	}})
	if m.screen != worktreeScreen || !strings.Contains(view(m), "Выберите worktree") || strings.Contains(view(m), "team/app") {
		t.Fatal(view(m))
	}
	press(m, 'c', "cleanup")
	if strings.Contains(view(m), "master") || !strings.Contains(view(m), "feature/database-cleanup") {
		t.Fatal(view(m))
	}
	cmd := press(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("missing worktree selection command")
	}
	_, load := m.Update(cmd())
	if selected != "/repo/.claude/worktrees/feature-db-cleanup" || m.options.PipelineID != 0 || m.screen != stageScreen || load == nil {
		t.Fatalf("directory=%s pipeline=%d screen=%d", selected, m.options.PipelineID, m.screen)
	}
	m.Update(loadMsg{snapshot: fixtureSnapshot(), generation: 0})
	if m.snapshot.Pipeline != nil {
		t.Fatal("old pipeline replaced the selected worktree")
	}
	for _, size := range [][2]int{{32, 16}, {80, 24}, {140, 30}} {
		m.screen = worktreeScreen
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if content := view(m); !strings.Contains(content, "esc") || len(strings.Split(content, "\n")) > size[1] {
			t.Fatal(content)
		}
	}
}

func TestWorktreeChooserHandlesNoMatchesFailedSelectionAndEscape(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	m.options.Worktrees = []repository.Worktree{{Directory: "/repo", Branch: "master"}, {Directory: "/worktree", SHA: "aabbcc"}}
	m.options.SwitchRepository = func(context.Context, string) (Provider, error) { return nil, errors.New("worktree unavailable") }
	press(m, 'w', "w")
	press(m, 'z', "zzzz")
	if !strings.Contains(view(m), "Нет совпадений") || press(m, tea.KeyEnter, "") != nil {
		t.Fatal(view(m))
	}
	for range 4 {
		press(m, tea.KeyBackspace, "")
	}
	cmd := press(m, tea.KeyEnter, "")
	m.Update(cmd())
	if !strings.Contains(view(m), "worktree unavailable") || m.screen != worktreeScreen {
		t.Fatal(view(m))
	}
	if cmd := press(m, tea.KeyEscape, ""); cmd == nil || m.screen != stageScreen || m.snapshot.Pipeline.ID != 20 {
		t.Fatal("Esc did not restore the existing pipeline")
	}
	initial := New(t.Context(), &fixtureProvider{}, Options{ChooseWorktree: true, Worktrees: m.options.Worktrees})
	if cmd := press(initial, tea.KeyEscape, ""); cmd == nil {
		t.Fatal("initial chooser did not quit on Esc")
	}
}

func TestOpenChangeUsesSelectedRepositoryAndKeepsErrorsAcrossRefreshAndLogs(t *testing.T) {
	for _, logs := range []bool{false, true} {
		m := ready(t, &fixtureProvider{text: "job log"})
		m.options.OpenChange = func(_ context.Context, repo repository.Repository) error {
			if repo.Branch != "feature/test" {
				t.Fatalf("branch=%s", repo.Branch)
			}
			return errors.New("browser unavailable")
		}
		if logs {
			cmd := press(m, tea.KeyEnter, "")
			m.Update(cmd())
		}
		cmd := press(m, 'm', "m")
		if cmd == nil || press(m, 'm', "m") != nil {
			t.Fatal("MR action missing or duplicated")
		}
		m.load()
		m.Update(cmd())
		if !strings.Contains(view(m), "browser unavailable") || m.snapshot.Pipeline.ID != 20 {
			t.Fatal(view(m))
		}
	}
}

func TestWorktreeSelectionDiscardsLateBrowserErrors(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	m.options.OpenChange = func(context.Context, repository.Repository) error { return errors.New("old browser error") }
	old := press(m, 'm', "m")
	m.options.Worktrees = []repository.Worktree{{Directory: "/repo"}, {Directory: "/other"}}
	m.options.SwitchRepository = func(context.Context, string) (Provider, error) { return &fixtureProvider{}, nil }
	press(m, 'w', "w")
	selected := press(m, tea.KeyEnter, "")
	m.Update(selected())
	m.Update(old())
	if m.actionErr != nil || strings.Contains(view(m), "old browser error") {
		t.Fatal(view(m))
	}
}

func TestOpenChangeWorksWithoutPipeline(t *testing.T) {
	m := ready(t, &fixtureProvider{})
	snapshot := fixtureSnapshot()
	snapshot.Pipeline, snapshot.Stages = nil, nil
	snapshot.Notice = "Для ветки нет пайплайнов"
	m.Update(loadMsg{snapshot: snapshot})
	m.options.OpenChange = func(context.Context, repository.Repository) error { return errors.New("нет открытого MR") }
	m.Update(press(m, 'm', "m")())
	if !strings.Contains(view(m), "нет открытого MR") || m.snapshot.Pipeline != nil {
		t.Fatal(view(m))
	}
}
