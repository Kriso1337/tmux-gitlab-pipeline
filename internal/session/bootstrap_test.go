package session

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tmux-gitlab-pipeline/internal/process"
)

func TestEnsureDoesNotUnlinkBusyLiveSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "busy.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSocket(listener)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			closeSocket(connection)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = Ensure(ctx, Endpoint{Socket: socket}, "test", "/must-not-execute")
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Lstat(socket); err != nil {
		t.Fatal("removed live socket", err)
	}
	closeSocket(listener)
	<-done
}
func TestStartupProbeIsBoundedForUnresponsiveServer(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "slow.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSocket(listener)
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	started := time.Now()
	err = probe(context.Background(), Client{Socket: socket})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("elapsed=%s error=%v", time.Since(started), err)
	}
	closeSocket(<-accepted)
}
func TestPrivateSocketDirectoryRejectsSymlinkAndOpenPermissions(t *testing.T) {
	base := t.TempDir()
	directory := filepath.Join(base, "cache")
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if secureDirectory(directory) == nil {
		t.Fatal("open directory accepted")
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if secureDirectory(link) == nil {
		t.Fatal("symlink accepted")
	}
}

func TestAliveRejectsRemovedSessionAndChangedServerPID(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	if _, err := process.Output(t.Context(), "", "tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "first"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := process.Output(context.Background(), "", "tmux", "-S", socket, "kill-server"); err != nil {
			t.Error(err)
		}
	})
	pid, err := process.Output(t.Context(), "", "tmux", "-S", socket, "display-message", "-p", "#{pid}")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := Endpoint{TmuxSocket: socket, Session: "$0", ServerPID: pid}
	if !endpoint.Alive(t.Context()) {
		t.Fatal("live session rejected")
	}
	changed := endpoint
	changed.ServerPID = "999999999"
	if changed.Alive(t.Context()) {
		t.Fatal("changed server PID accepted")
	}
	if _, err := process.Output(t.Context(), "", "tmux", "-S", socket, "new-session", "-d", "-s", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Output(t.Context(), "", "tmux", "-S", socket, "kill-session", "-t", "$0"); err != nil {
		t.Fatal(err)
	}
	if endpoint.Alive(t.Context()) {
		t.Fatal("removed session accepted while another session survives")
	}
}
