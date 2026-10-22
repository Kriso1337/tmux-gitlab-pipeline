package ci

import "time"

type Action string

const (
	RetryJob         Action = "retry_job"
	RetryFailed      Action = "retry_failed"
	PlayJob          Action = "play_job"
	DispatchWorkflow Action = "dispatch_workflow"
)

type Mutation struct {
	Action   Action
	Pipeline Pipeline
	Job      Job
	Workflow Workflow
	Inputs   map[string]string
}

type Workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type ArtifactFile struct {
	Name string `json:"filename"`
	Size int64  `json:"size"`
}

type Artifact struct {
	ID      int64
	JobID   int64
	Name    string
	Size    int64
	Expired bool
}

type TestFailure struct {
	Name    string
	Suite   string
	Message string
}

type Report struct {
	Total    int
	Passed   int
	Failed   int
	Skipped  int
	Duration float64
	Coverage *float64
	Failures []TestFailure
	Notice   string
}

type JobComparison struct {
	Name           string
	Stage          string
	Before         Status
	After          Status
	BeforeDuration float64
	AfterDuration  float64
}

type Comparison struct {
	Before    Pipeline
	After     Pipeline
	Jobs      []JobComparison
	UpdatedAt time.Time
}
