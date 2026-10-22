package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/repository"
)

func testTarget() Target {
	return Target{Provider: ci.GitLab, Repository: repository.Repository{Directory: "/repo", Host: "gitlab.example.com", Project: "acme/payments", Branch: "feature/cache", SHA: "abc123"}}
}
func testServer(t *testing.T, options ServerOptions, handler Handler) Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "cache.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(options, handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("session server did not stop")
		}
	})
	return Client{Socket: socket, Version: options.Version}
}
func call(t *testing.T, client Client, request Request) Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	response, err := client.Call(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func TestReopeningReturnsCachedDataAndLiveRefreshContinues(t *testing.T) {
	options := DefaultServerOptions("test")
	options.Refresh = 30 * time.Millisecond
	options.Sweep = 5 * time.Millisecond
	options.Warm = 200 * time.Millisecond
	var count atomic.Int64
	client := testServer(t, options, func(context.Context, Request) (json.RawMessage, error) {
		n := count.Add(1)
		return json.RawMessage(fmt.Sprintf(`{"count":%d}`, n)), nil
	})
	request := Request{Operation: SnapshotOperation, Target: testTarget()}
	first := call(t, client, request)
	second := call(t, client, request)
	if first.Cached || !second.Cached || string(first.Data) != string(second.Data) || count.Load() != 1 {
		t.Fatalf("first=%+v second=%+v count=%d", first, second, count.Load())
	}
	deadline := time.Now().Add(time.Second)
	for count.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	third := call(t, client, request)
	if string(third.Data) == string(first.Data) {
		t.Fatal("background refresh did not update reopened popup")
	}
}
func TestConcurrentClientsShareOneFetchAndBranchKeysAreIsolated(t *testing.T) {
	var count atomic.Int64
	client := testServer(t, DefaultServerOptions("test"), func(ctx context.Context, r Request) (json.RawMessage, error) {
		count.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
		return json.Marshal(r.Target.Repository.Branch)
	})
	request := Request{Operation: SnapshotOperation, Target: testTarget()}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { call(t, client, request) })
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("fetches=%d", count.Load())
	}
	request.Target.Repository.Branch = "feature/other"
	response := call(t, client, request)
	if string(response.Data) != `"feature/other"` || count.Load() != 2 {
		t.Fatalf("response=%+v count=%d", response, count.Load())
	}
}
func TestInvalidationCancelsOldFetchAndAllowsImmediateNewFetch(t *testing.T) {
	entered := make(chan struct{})
	var count atomic.Int64
	client := testServer(t, DefaultServerOptions("test"), func(ctx context.Context, r Request) (json.RawMessage, error) {
		if count.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return json.RawMessage(`"new"`), nil
	})
	request := Request{Operation: SnapshotOperation, Target: testTarget(), Quick: true}
	call(t, client, request)
	<-entered
	call(t, client, Request{Operation: InvalidateOperation, Target: request.Target})
	request.Quick = false
	response := call(t, client, request)
	if string(response.Data) != `"new"` {
		t.Fatalf("response=%+v", response)
	}
}
func TestFailedRefreshPreservesSnapshotAndReportsError(t *testing.T) {
	var count atomic.Int64
	client := testServer(t, DefaultServerOptions("test"), func(context.Context, Request) (json.RawMessage, error) {
		if count.Add(1) > 1 {
			return nil, fmt.Errorf("provider unavailable")
		}
		return json.RawMessage(`{"state":"running"}`), nil
	})
	request := Request{Operation: SnapshotOperation, Target: testTarget()}
	first := call(t, client, request)
	request.Force = true
	failed := call(t, client, request)
	if string(failed.Data) != string(first.Data) || failed.Error != "provider unavailable" {
		t.Fatalf("response=%+v", failed)
	}
}
func TestFinishedLogCannotReuseRunningPreview(t *testing.T) {
	client := testServer(t, DefaultServerOptions("test"), func(_ context.Context, r Request) (json.RawMessage, error) { return json.Marshal(r.Job.Status) })
	request := Request{Operation: TraceOperation, Target: testTarget(), Job: ci.Job{ID: 7, Status: ci.Running}}
	call(t, client, request)
	request.Job.Status = ci.Success
	response := call(t, client, request)
	if string(response.Data) != `"success"` || response.Cached {
		t.Fatalf("response=%+v", response)
	}
}
func TestTargetsAndWorkersRemainBoundedAndShutdownJoinsWorkers(t *testing.T) {
	baseline := runtime.NumGoroutine()
	options := DefaultServerOptions("test")
	options.Targets = 3
	options.Warm = 30 * time.Millisecond
	options.Sweep = 5 * time.Millisecond
	client := testServer(t, options, func(context.Context, Request) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	for i := range 100 {
		target := testTarget()
		target.Repository.Branch = fmt.Sprintf("feature/%d", i)
		call(t, client, Request{Operation: SnapshotOperation, Target: target})
	}
	response := call(t, client, Request{Operation: StatsOperation})
	var stats ServerStats
	if err := json.Unmarshal(response.Data, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Targets > 3 || stats.Flights > options.Workers+options.Queue {
		t.Fatalf("stats=%+v", stats)
	}
	if runtime.NumGoroutine() > baseline+30 {
		t.Fatalf("unbounded goroutines: %d", runtime.NumGoroutine()-baseline)
	}
	time.Sleep(50 * time.Millisecond)
	response = call(t, client, Request{Operation: StatsOperation})
	if err := json.Unmarshal(response.Data, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Targets != 0 {
		t.Fatalf("expired watches=%d", stats.Targets)
	}
}
func TestRequestValidationRejectsUnknownOperationsAndInvalidTargets(t *testing.T) {
	for _, request := range []Request{{Operation: "oops"}, {Operation: SnapshotOperation}, {Operation: TraceOperation, Target: testTarget()}} {
		if request.validate() == nil {
			t.Fatalf("accepted %+v", request)
		}
	}
}
