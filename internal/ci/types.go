package ci

import (
	"time"

	"tmux-gitlab-pipeline/internal/repository"
)

type Status string

type Provider string

const (
	GitLab Provider = "gitlab"
	GitHub Provider = "github"
)

const (
	Created        Status = "created"
	Pending        Status = "pending"
	Running        Status = "running"
	Success        Status = "success"
	Failed         Status = "failed"
	Canceled       Status = "canceled"
	Skipped        Status = "skipped"
	Manual         Status = "manual"
	Scheduled      Status = "scheduled"
	Waiting        Status = "waiting_for_resource"
	Preparing      Status = "preparing"
	Neutral        Status = "neutral"
	ActionRequired Status = "action_required"
)

func (s Status) Finished() bool {
	return s == Success || s == Failed || s == Canceled || s == Skipped || s == Neutral || s == ActionRequired
}

func (s Status) Active() bool {
	return s == Running || s == Preparing || s == Waiting || s == Pending || s == Created
}

type Pipeline struct {
	Name         string     `json:"name"`
	ID           int64      `json:"id"`
	IID          int64      `json:"iid"`
	ProjectID    int64      `json:"project_id"`
	SHA          string     `json:"sha"`
	Ref          string     `json:"ref"`
	Source       string     `json:"source"`
	Status       Status     `json:"status"`
	WebURL       string     `json:"web_url"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Duration     float64    `json:"duration"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	WorkflowID   int64      `json:"workflow_id,omitempty"`
	Coverage     string     `json:"coverage,omitempty"`
	MergeRequest int64      `json:"context_merge_request,omitempty"`
	SourceSHA    string     `json:"context_source_sha,omitempty"`
}

type Job struct {
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	Stage         string        `json:"stage"`
	Status        Status        `json:"status"`
	AllowFailure  bool          `json:"allow_failure"`
	StartedAt     *time.Time    `json:"started_at"`
	FinishedAt    *time.Time    `json:"finished_at"`
	Duration      float64       `json:"duration"`
	WebURL        string        `json:"web_url"`
	Bridge        bool          `json:"context_bridge,omitempty"`
	Downstream    *Pipeline     `json:"downstream_pipeline"`
	Steps         []Step        `json:"steps,omitempty"`
	ArtifactsFile *ArtifactFile `json:"artifacts_file,omitempty"`
}

type Step struct {
	Name   string
	Number int
	Status Status
}

type Stage struct {
	Name string
	Jobs []Job
}

type Snapshot struct {
	Provider   Provider
	Repository repository.Repository
	Pipeline   *Pipeline
	Candidates []Pipeline
	Stages     []Stage
	UpdatedAt  time.Time
	Notice     string
	Cached     bool
	Refreshing bool
	CacheError string
}

type Trace struct {
	Text      string
	Truncated bool
}

func (s Snapshot) Progress() (finished, total, manual int) {
	for _, stage := range s.Stages {
		done, count, waiting := stage.Progress()
		finished, total, manual = finished+done, total+count, manual+waiting
	}
	return
}

func (s Stage) Progress() (finished, total, manual int) {
	for _, job := range s.Jobs {
		if job.Status == Manual {
			manual++
			continue
		}
		total++
		if job.Status.Finished() {
			finished++
		}
	}
	return
}
