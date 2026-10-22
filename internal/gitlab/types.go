package gitlab

import "tmux-gitlab-pipeline/internal/ci"

type Status = ci.Status
type Pipeline = ci.Pipeline
type Job = ci.Job
type Step = ci.Step
type Stage = ci.Stage
type Snapshot = ci.Snapshot
type Trace = ci.Trace

const (
	Created   = ci.Created
	Pending   = ci.Pending
	Running   = ci.Running
	Success   = ci.Success
	Failed    = ci.Failed
	Canceled  = ci.Canceled
	Skipped   = ci.Skipped
	Manual    = ci.Manual
	Scheduled = ci.Scheduled
	Waiting   = ci.Waiting
	Preparing = ci.Preparing
)
