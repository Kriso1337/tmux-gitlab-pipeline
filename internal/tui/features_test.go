package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

type featureFixture struct{ mutations []ci.Mutation }

func (f *featureFixture) History(context.Context, repository.Repository, ci.Pipeline) ([]ci.Pipeline, error) {
	return []ci.Pipeline{{ID: 20, Status: ci.Failed}, {ID: 19, Status: ci.Success}}, nil
}
func (f *featureFixture) Mutate(_ context.Context, _ repository.Repository, mutation ci.Mutation) error {
	f.mutations = append(f.mutations, mutation)
	return nil
}
func (f *featureFixture) Workflows(context.Context, repository.Repository) ([]ci.Workflow, error) {
	return []ci.Workflow{{ID: 7, Name: "Release", State: "active"}}, nil
}
func (f *featureFixture) Artifacts(context.Context, ci.Snapshot) ([]ci.Artifact, error) {
	return []ci.Artifact{{ID: 1, Name: "report", Size: 1024}}, nil
}
func (f *featureFixture) Download(context.Context, repository.Repository, ci.Artifact, string) (string, error) {
	return "/downloads/report.zip", nil
}
func (f *featureFixture) Report(context.Context, ci.Snapshot) (ci.Report, error) {
	value := 87.5
	return ci.Report{Total: 3, Passed: 2, Failed: 1, Coverage: &value}, nil
}
func withFeatures(t *testing.T) (*Model, *featureFixture) {
	t.Helper()
	m := ready(t, &fixtureProvider{text: "before\nfatal: connection refused\nafter"})
	f := &featureFixture{}
	m.options.Features = f
	return m, f
}
func TestMutationRequiresExplicitConfirmationAndCanBeCancelled(t *testing.T) {
	m, f := withFeatures(t)
	m.stage, m.row = 0, 0
	if cmd := press(m, 'R', "R"); cmd != nil || m.screen != confirmScreen || len(f.mutations) != 0 {
		t.Fatal("mutation ran before confirmation")
	}
	press(m, tea.KeyEnter, "")
	if len(f.mutations) != 0 {
		t.Fatal("Enter implicitly confirmed mutation")
	}
	press(m, tea.KeyEscape, "")
	if len(f.mutations) != 0 || m.screen != stageScreen {
		t.Fatal("cancelled mutation ran")
	}
	press(m, 'R', "R")
	cmd := press(m, 'y', "y")
	if cmd == nil {
		t.Fatal("missing confirmed command")
	}
	m.Update(cmd())
	if len(f.mutations) != 1 || f.mutations[0].Job.ID != 1 || m.screen != stageScreen {
		t.Fatalf("mutations=%+v screen=%d", f.mutations, m.screen)
	}
}
func TestHistoryPinsRunAndDiagnosticOpensSelectedJobLog(t *testing.T) {
	m, _ := withFeatures(t)
	cmd := press(m, 'H', "H")
	m.Update(cmd())
	press(m, tea.KeyDown, "")
	cmd = press(m, tea.KeyEnter, "")
	if cmd == nil || m.options.PipelineID != 19 {
		t.Fatal("history did not pin selected run")
	}
	m.Update(cmd())
	cmd = press(m, 'd', "d")
	m.Update(cmd())
	if !strings.Contains(view(m), "connection refused") {
		t.Fatal(view(m))
	}
	cmd = press(m, tea.KeyEnter, "")
	if cmd == nil || m.screen != logScreen {
		t.Fatal("diagnosis cannot open log")
	}
}
func TestReportsAndArtifactsShowResultsAndRejectLateResponses(t *testing.T) {
	m, _ := withFeatures(t)
	cmd := press(m, 't', "t")
	m.Update(cmd())
	if !strings.Contains(view(m), "87.50%") {
		t.Fatal(view(m))
	}
	press(m, tea.KeyEscape, "")
	cmd = press(m, 'x', "x")
	press(m, tea.KeyEscape, "")
	m.Update(cmd())
	if m.screen != stageScreen || strings.Contains(view(m), "report.zip") {
		t.Fatal("late response changed screen")
	}
	cmd = press(m, 'x', "x")
	m.Update(cmd())
	cmd = press(m, tea.KeyEnter, "")
	m.Update(cmd())
	if !strings.Contains(view(m), "/downloads/report.zip") {
		t.Fatal(view(m))
	}
}
func TestWorkflowDispatchValidatesInputsBeforeConfirmation(t *testing.T) {
	m, f := withFeatures(t)
	m.snapshot.Provider = ci.GitHub
	cmd := press(m, 's', "s")
	m.Update(cmd())
	press(m, tea.KeyEnter, "")
	m.feature.input = "invalid"
	press(m, tea.KeyEnter, "")
	if m.feature.err == nil || m.screen != inputScreen {
		t.Fatal("invalid inputs accepted")
	}
	m.feature.input = `{"environment":"stage"}`
	press(m, tea.KeyEnter, "")
	if m.screen != confirmScreen || len(f.mutations) != 0 {
		t.Fatal("dispatch skipped confirmation")
	}
	cmd = press(m, 'y', "y")
	m.Update(cmd())
	if len(f.mutations) != 1 || f.mutations[0].Inputs["environment"] != "stage" {
		t.Fatalf("mutations=%+v", f.mutations)
	}
}
func TestCompletedMutationIsNotOfferedAgainAfterCacheFailure(t *testing.T) {
	m, _ := withFeatures(t)
	m.screen = confirmScreen
	m.feature.generation = 4
	m.Update(featureMsg{generation: 4, mutated: true, err: fmt.Errorf("cache unavailable")})
	if m.screen != stageScreen || m.actionErr == nil {
		t.Fatal("successful remote mutation could be repeated")
	}
}

func TestNewBranchCanDispatchWorkflowBeforeItsFirstRun(t *testing.T) {
	m, _ := withFeatures(t)
	m.snapshot.Pipeline = nil
	m.snapshot.Provider = ci.GitHub
	cmd := press(m, 's', "s")
	if cmd == nil || m.screen != workflowScreen {
		t.Fatal("first workflow run cannot be started")
	}
	m.Update(cmd())
	if len(m.feature.workflows) != 1 {
		t.Fatal("workflows missing")
	}
}
func TestFailedFeatureReloadCannotOfferOldArtifacts(t *testing.T) {
	m, _ := withFeatures(t)
	cmd := press(m, 'x', "x")
	m.Update(cmd())
	if len(m.feature.artifacts) != 1 {
		t.Fatal("fixture missing")
	}
	m.featureCommand(artifactScreen, func(context.Context) featureMsg { return featureMsg{err: fmt.Errorf("unavailable")} })
	m.Update(featureMsg{generation: m.feature.generation, err: fmt.Errorf("unavailable")})
	if len(m.feature.artifacts) != 0 || press(m, tea.KeyEnter, "") != nil {
		t.Fatal("old artifacts remained actionable")
	}
}
