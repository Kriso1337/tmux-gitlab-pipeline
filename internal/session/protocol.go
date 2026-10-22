package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

const Protocol = 1
const MaxRequestBytes = 512 << 10
const MaxResponseBytes = (8 << 20) + (16 << 10)

type Operation string

const (
	SnapshotOperation   Operation = "snapshot"
	TraceOperation      Operation = "trace"
	PingOperation       Operation = "ping"
	StatsOperation      Operation = "stats"
	InvalidateOperation Operation = "invalidate"
	ClearOperation      Operation = "clear"
	ShutdownOperation   Operation = "shutdown"
)

type Target struct {
	Repository repository.Repository
	Provider   ci.Provider
}

type Request struct {
	Operation  Operation
	Target     Target
	PipelineID int64
	Job        ci.Job
	Force      bool
	Quick      bool
}

type Response struct {
	Protocol   int
	Version    string
	Data       json.RawMessage
	Error      string
	Cached     bool
	Refreshing bool
}

func (t Target) Prefix() string {
	data, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + ":"
}

func (r Request) key() string {
	return fmt.Sprintf("%s%s:%d:%d:%s", r.Target.Prefix(), r.Operation, r.PipelineID, r.Job.ID, r.Job.Status)
}

func (r Request) validate() error {
	if r.Operation == PingOperation || r.Operation == StatsOperation || r.Operation == ClearOperation || r.Operation == ShutdownOperation {
		return nil
	}
	if r.Operation != SnapshotOperation && r.Operation != TraceOperation && r.Operation != InvalidateOperation {
		return fmt.Errorf("unknown cache operation")
	}
	repo := r.Target.Repository
	if !filepath.IsAbs(repo.Directory) || len(repo.Directory) > 4096 || strings.ContainsAny(repo.Directory, "\x00\r\n") || len(repo.Branch) > 1024 || len(repo.SHA) > 128 || len(repo.Project) > 4096 || len(repo.Remote) > 1024 {
		return fmt.Errorf("invalid cache repository context")
	}
	if err := repository.ValidateHost(repo.Host); err != nil {
		return err
	}
	if r.Target.Provider != ci.GitLab && r.Target.Provider != ci.GitHub {
		return fmt.Errorf("invalid cache provider")
	}
	parts := strings.Split(repo.Project, "/")
	if len(parts) < 2 {
		return fmt.Errorf("invalid cache project")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\x00\r\n\\") {
			return fmt.Errorf("invalid cache project path")
		}
	}
	if r.PipelineID < 0 || len(r.Job.Steps) > 1024 || len(r.Job.Name) > 4096 {
		return fmt.Errorf("invalid cache request")
	}
	if r.Operation == TraceOperation && r.Job.ID <= 0 {
		return fmt.Errorf("invalid cached job")
	}
	return nil
}
