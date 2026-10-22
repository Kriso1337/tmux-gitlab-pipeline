package gitlab

import (
	"context"
	"fmt"
	"time"

	"tmux-gitlab-pipeline/internal/repository"
)

type DemoSource struct{ Started time.Time }

func (d DemoSource) Load(ctx context.Context, pipelineID int64) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	age := time.Since(d.Started)
	started := d.Started.Add(-134 * time.Second)
	jobStarted := d.Started.Add(-73 * time.Second)
	finished := d.Started.Add(-20 * time.Second)
	status := Running
	if age > 32*time.Second {
		status = Success
	}
	pipeline := Pipeline{ID: 1842, IID: 42, ProjectID: 1, SHA: "a8c43f1029fabcde1234567890abcdef1234567890", Ref: "feat/payment-retry", Source: "demo", Status: status, StartedAt: &started}
	if status == Success {
		end := d.Started.Add(32 * time.Second)
		pipeline.FinishedAt = &end
	}
	jobs := []Job{
		{ID: 1, Name: "lint", Stage: "validate", Status: Success, StartedAt: &started, FinishedAt: &finished},
		{ID: 2, Name: "types", Stage: "validate", Status: Success, Duration: 18},
		{ID: 3, Name: "image", Stage: "build", Status: Success, Duration: 43},
		{ID: 4, Name: "assets", Stage: "build", Status: Success, Duration: 26},
		{ID: 5, Name: "unit", Stage: "test", Status: Running, StartedAt: &jobStarted},
		{ID: 6, Name: "integration", Stage: "test", Status: Running, StartedAt: &jobStarted},
		{ID: 7, Name: "staging", Stage: "deploy", Status: Pending},
		{ID: 8, Name: "smoke", Stage: "deploy", Status: Pending},
		{ID: 9, Name: "production", Stage: "deploy", Status: Manual, AllowFailure: true},
	}
	for i := 4; i < 8; i++ {
		threshold := time.Duration(i-3) * 8 * time.Second
		if age >= threshold {
			end := d.Started.Add(threshold)
			jobs[i].Status = Success
			jobs[i].FinishedAt = &end
		}
		if age >= threshold-8*time.Second && age < threshold {
			jobs[i].Status = Running
			start := d.Started.Add(threshold - 8*time.Second)
			jobs[i].StartedAt = &start
		}
	}
	repo := repository.Repository{Directory: ".", Host: "gitlab.example.com", Project: "acme/payments", Branch: pipeline.Ref, SHA: pipeline.SHA, Remote: "origin"}
	return Snapshot{Repository: repo, Pipeline: &pipeline, Candidates: []Pipeline{pipeline}, Stages: GroupJobs([]string{"validate", "build", "test", "deploy"}, jobs), UpdatedAt: time.Now(), Notice: "ДЕМО · вымышленные данные"}, nil
}

func (d DemoSource) Trace(ctx context.Context, repo repository.Repository, job Job) (Trace, error) {
	if err := ctx.Err(); err != nil {
		return Trace{}, err
	}
	if job.Status == Manual || job.Status == Pending {
		return Trace{Text: "Job ещё не запускалась."}, nil
	}
	text := fmt.Sprintf("$ run %s\n\n✓ retry after timeout\n✓ reject duplicate payment\n✓ preserve request key\n", job.Name)
	if job.Status == Success {
		text += "✓ payment flow\n\n4 tests passed · exit code 0"
	} else {
		text += fmt.Sprintf("\nRunning payment flow… %ds", int(time.Since(d.Started).Seconds()))
	}
	return Trace{Text: text}, nil
}
