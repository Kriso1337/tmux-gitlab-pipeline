package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
	"tmux-gitlab-pipeline/internal/source"
)

type Client struct{ Socket, Version string }

func (c Client) Call(ctx context.Context, request Request) (Response, error) {
	if err := request.validate(); err != nil {
		return Response{}, err
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return Response{}, fmt.Errorf("connect session cache: %w", err)
	}
	defer closeSocket(conn)
	deadline := time.Now().Add(95 * time.Second)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return Response{}, err
	}
	stop := context.AfterFunc(ctx, func() { closeSocket(conn) })
	defer stop()
	data, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	if len(data) > MaxRequestBytes {
		return Response{}, fmt.Errorf("session request too large")
	}
	if _, err = conn.Write(data); err != nil {
		return Response{}, err
	}
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return Response{}, fmt.Errorf("session cache requires a Unix socket")
	}
	if err := unix.CloseWrite(); err != nil {
		return Response{}, err
	}
	data, err = io.ReadAll(io.LimitReader(conn, MaxResponseBytes+1))
	if err != nil {
		return Response{}, err
	}
	if len(data) > MaxResponseBytes {
		return Response{}, fmt.Errorf("session response too large")
	}
	var response Response
	if err := json.Unmarshal(data, &response); err != nil {
		return Response{}, fmt.Errorf("decode session response: %w", err)
	}
	if response.Protocol != Protocol || (c.Version != "" && response.Version != c.Version) {
		return Response{}, fmt.Errorf("session cache version changed; reopen the popup")
	}
	return response, nil
}
func (c Client) Check(ctx context.Context) error {
	response, err := c.Call(ctx, Request{Operation: PingOperation})
	if err != nil {
		return err
	}
	if response.Error != "" {
		return fmt.Errorf("session cache: %s", response.Error)
	}
	return nil
}

type Provider struct {
	Client Client
	Source source.Source
}

func (p Provider) Target(ctx context.Context) (Target, error) {
	repo, err := repository.Resolve(ctx, p.Source.Options)
	if err != nil {
		return Target{}, err
	}
	return Target{Repository: repo, Provider: source.Detect(repo.Host, p.Source.Provider)}, nil
}
func (p Provider) Load(ctx context.Context, id int64) (ci.Snapshot, error) {
	return p.load(ctx, id, false, false)
}
func (p Provider) Refresh(ctx context.Context, id int64) (ci.Snapshot, error) {
	return p.load(ctx, id, true, false)
}
func (p Provider) Quick(ctx context.Context, id int64) (ci.Snapshot, error) {
	return p.load(ctx, id, false, true)
}
func (p Provider) load(ctx context.Context, id int64, force, quick bool) (ci.Snapshot, error) {
	target, err := p.Target(ctx)
	if err != nil {
		return ci.Snapshot{}, err
	}
	response, err := p.Client.Call(ctx, Request{Operation: SnapshotOperation, Target: target, PipelineID: id, Force: force, Quick: quick})
	if err != nil {
		return ci.Snapshot{}, err
	}
	if len(response.Data) == 0 {
		if response.Error != "" {
			return ci.Snapshot{}, fmt.Errorf("CI refresh: %s", response.Error)
		}
		return ci.Snapshot{Repository: target.Repository, Provider: target.Provider, Refreshing: response.Refreshing}, nil
	}
	var snapshot ci.Snapshot
	if err := json.Unmarshal(response.Data, &snapshot); err != nil {
		return snapshot, err
	}
	snapshot.Cached, snapshot.Refreshing, snapshot.CacheError = response.Cached, response.Refreshing, response.Error
	return snapshot, nil
}
func (p Provider) Trace(ctx context.Context, repo repository.Repository, job ci.Job) (ci.Trace, error) {
	response, err := p.Client.Call(ctx, Request{Operation: TraceOperation, Target: Target{Repository: repo, Provider: source.Detect(repo.Host, p.Source.Provider)}, Job: job})
	if err != nil {
		return ci.Trace{}, err
	}
	var trace ci.Trace
	if len(response.Data) == 0 {
		return trace, fmt.Errorf("CI log: %s", response.Error)
	}
	if err := json.Unmarshal(response.Data, &trace); err != nil {
		return trace, err
	}
	if response.Error != "" {
		trace.Text += "\n[refresh failed: " + response.Error + "]"
	}
	return trace, nil
}
func (p Provider) Invalidate(ctx context.Context, repo repository.Repository) error {
	response, err := p.Client.Call(ctx, Request{Operation: InvalidateOperation, Target: Target{Repository: repo, Provider: source.Detect(repo.Host, p.Source.Provider)}})
	if err != nil {
		return err
	}
	if response.Error != "" {
		return fmt.Errorf("invalidate CI cache: %s", response.Error)
	}
	return nil
}
func SourceHandler(s source.Source) Handler {
	return func(ctx context.Context, request Request) (json.RawMessage, error) {
		selected := s
		selected.Provider = request.Target.Provider
		switch request.Operation {
		case SnapshotOperation:
			value, err := selected.LoadRepository(ctx, request.Target.Repository, request.PipelineID)
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		case TraceOperation:
			value, err := selected.Trace(ctx, request.Target.Repository, request.Job)
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		default:
			return nil, fmt.Errorf("unsupported CI cache request")
		}
	}
}
