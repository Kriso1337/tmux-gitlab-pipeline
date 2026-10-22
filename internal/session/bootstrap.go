package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"tmux-gitlab-pipeline/internal/process"
)

type Endpoint struct{ Socket, TmuxSocket, Session, ServerPID string }

func Locate(ctx context.Context, pane string) (Endpoint, error) {
	args := []string{"display-message", "-p"}
	if pane != "" {
		if !regexp.MustCompile(`^%[0-9]+$`).MatchString(pane) {
			return Endpoint{}, fmt.Errorf("invalid source pane")
		}
		args = append(args, "-t", pane)
	}
	args = append(args, "#{socket_path}\n#{pid}\n#{session_id}")
	metadata, err := process.Output(ctx, "", "tmux", args...)
	if err != nil {
		return Endpoint{}, err
	}
	parts := strings.Split(metadata, "\n")
	if len(parts) != 3 || !filepath.IsAbs(parts[0]) || !regexp.MustCompile(`^[0-9]+$`).MatchString(parts[1]) || !regexp.MustCompile(`^\$[0-9]+$`).MatchString(parts[2]) {
		return Endpoint{}, fmt.Errorf("tmux session context unavailable")
	}
	directory := fmt.Sprintf("/tmp/tmux-ci-%d", os.Getuid())
	if err := secureDirectory(directory); err != nil {
		return Endpoint{}, err
	}
	hash := sha256.Sum256([]byte(metadata))
	socket := filepath.Join(directory, fmt.Sprintf("%x.sock", hash[:16]))
	return Endpoint{Socket: socket, TmuxSocket: parts[0], Session: parts[2], ServerPID: parts[1]}, nil
}
func secureDirectory(directory string) error {
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("unsafe session socket directory")
	}
	return nil
}
func Ensure(ctx context.Context, endpoint Endpoint, version, executable string) (Client, error) {
	client := Client{Socket: endpoint.Socket, Version: version}
	if err := probe(ctx, client); err == nil {
		return client, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	lock, err := os.OpenFile(endpoint.Socket+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return client, err
	}
	defer func() {
		if err := lock.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close session startup lock:", err)
		}
	}()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return client, err
		}
		select {
		case <-ctx.Done():
			return client, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			fmt.Fprintln(os.Stderr, "unlock session startup:", err)
		}
	}()
	if err := probe(ctx, client); err == nil {
		return client, nil
	}
	old := Client{Socket: endpoint.Socket}
	if err := probe(ctx, old); err == nil {
		if response, err := old.Call(ctx, Request{Operation: ShutdownOperation}); err != nil {
			return client, err
		} else if response.Error != "" {
			return client, fmt.Errorf("stop old cache: %s", response.Error)
		}
		for {
			if _, err := os.Lstat(endpoint.Socket); errors.Is(err, os.ErrNotExist) {
				break
			}
			select {
			case <-ctx.Done():
				return client, ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	connection, dialErr := net.DialTimeout("unix", endpoint.Socket, 300*time.Millisecond)
	if dialErr == nil {
		closeSocket(connection)
		return client, fmt.Errorf("session cache is busy or unresponsive; retry shortly")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return client, fmt.Errorf("probe session listener: %w", dialErr)
	}
	if info, err := os.Lstat(endpoint.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return client, fmt.Errorf("unsafe session socket path")
		}
		if err := os.Remove(endpoint.Socket); err != nil {
			return client, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return client, err
	}
	cmd := exec.Command(executable, "serve", "--socket", endpoint.Socket, "--tmux-socket", endpoint.TmuxSocket, "--session", endpoint.Session, "--server-pid", endpoint.ServerPID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return client, err
	}
	defer func() {
		if err := null.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close daemon streams:", err)
		}
	}()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	if err := cmd.Start(); err != nil {
		return client, fmt.Errorf("start session cache: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for {
		if err := client.Check(ctx); err == nil {
			return client, nil
		}
		select {
		case err := <-exited:
			return client, fmt.Errorf("session cache exited before startup: %v", err)
		case <-ctx.Done():
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return client, errors.Join(ctx.Err(), err)
			}
			return client, fmt.Errorf("start session cache: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func probe(ctx context.Context, client Client) error {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return client.Check(ctx)
}
func Listen(endpoint Endpoint) (net.Listener, error) {
	if !filepath.IsAbs(endpoint.Socket) || filepath.Dir(endpoint.Socket) != fmt.Sprintf("/tmp/tmux-ci-%d", os.Getuid()) || !regexp.MustCompile(`^[0-9a-f]{32}\.sock$`).MatchString(filepath.Base(endpoint.Socket)) || !filepath.IsAbs(endpoint.TmuxSocket) || !regexp.MustCompile(`^\$[0-9]+$`).MatchString(endpoint.Session) {
		return nil, fmt.Errorf("invalid daemon endpoint")
	}
	if err := secureDirectory(filepath.Dir(endpoint.Socket)); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", endpoint.Socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint.Socket, 0600); err != nil {
		closeSocket(listener)
		return nil, err
	}
	return listener, nil
}
func (e Endpoint) Alive(ctx context.Context) bool {
	if _, err := process.Output(ctx, "", "tmux", "-S", e.TmuxSocket, "has-session", "-t", e.Session); err != nil {
		return false
	}
	pid, err := process.Output(ctx, "", "tmux", "-S", e.TmuxSocket, "display-message", "-p", "-t", e.Session, "#{pid}")
	return err == nil && pid == e.ServerPID
}
