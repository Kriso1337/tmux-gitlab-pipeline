package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/github"
	"tmux-gitlab-pipeline/internal/gitlab"
	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/session"
	"tmux-gitlab-pipeline/internal/source"
	"tmux-gitlab-pipeline/internal/tmux"
)

func serve(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	endpoint := session.Endpoint{}
	flags.StringVar(&endpoint.Socket, "socket", "", "Session socket")
	flags.StringVar(&endpoint.TmuxSocket, "tmux-socket", "", "Tmux server socket")
	flags.StringVar(&endpoint.Session, "session", "", "Tmux session ID")
	flags.StringVar(&endpoint.ServerPID, "server-pid", "", "Tmux server process ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected daemon arguments")
	}
	debug.SetMemoryLimit(192 << 20)
	listener, err := session.Listen(endpoint)
	if err != nil {
		return err
	}
	options := session.DefaultServerOptions(version)
	options.Alive = endpoint.Alive
	options.Notify = func(ctx context.Context, request session.Request, before, after json.RawMessage) error {
		enabled, err := process.Output(ctx, "", "tmux", "-S", endpoint.TmuxSocket, "show-option", "-gqv", "@gitlab-pipeline-notify")
		if err != nil {
			return err
		}
		if enabled == "off" {
			return nil
		}
		var previous, current ci.Snapshot
		if err := json.Unmarshal(before, &previous); err != nil {
			return err
		}
		if err := json.Unmarshal(after, &current); err != nil {
			return err
		}
		if previous.Pipeline == nil || current.Pipeline == nil || previous.Pipeline.ID != current.Pipeline.ID || !previous.Pipeline.Status.Active() || !current.Pipeline.Status.Finished() {
			return nil
		}
		message := fmt.Sprintf("CI · %s · %s · %s", current.Repository.Project, current.Repository.Branch, current.Pipeline.Status)
		message = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, message)
		if len(message) > 400 {
			message = message[:400]
		}
		_, err = process.Output(ctx, "", "tmux", "-S", endpoint.TmuxSocket, "display-message", "-d", "6000", "-t", endpoint.Session, strings.ReplaceAll(message, "#", "##"))
		return err
	}
	backend := source.Source{GitLab: gitlab.Client{Executable: "glab"}, GitHub: github.Client{Executable: "gh"}}
	server, err := session.NewServer(options, session.SourceHandler(backend))
	if err != nil {
		return err
	}
	return server.Serve(ctx, listener)
}
func cacheCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 1 || (args[0] != "stats" && args[0] != "clear" && args[0] != "stop") {
		return fmt.Errorf("usage: cache stats|clear|stop")
	}
	endpoint, err := session.Locate(ctx, "")
	if err != nil {
		return err
	}
	operation := session.StatsOperation
	if args[0] == "clear" {
		operation = session.ClearOperation
	}
	if args[0] == "stop" {
		operation = session.ShutdownOperation
	}
	response, err := (session.Client{Socket: endpoint.Socket, Version: version}).Call(ctx, session.Request{Operation: operation})
	if err != nil {
		return err
	}
	if response.Error != "" {
		return fmt.Errorf("session cache: %s", response.Error)
	}
	if len(response.Data) > 0 {
		_, err = fmt.Fprintln(output, string(response.Data))
	}
	return err
}
func statusCommand(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	pane := flags.String("pane", "", "Tmux pane")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := printStatus(ctx, *pane, output); err != nil {
		_, writeErr := fmt.Fprint(output, "CI ?")
		if writeErr != nil {
			return writeErr
		}
	}
	return nil
}
func cachedSource(ctx context.Context, pane string, s source.Source) (session.Provider, error) {
	endpoint, err := session.Locate(ctx, pane)
	if err != nil {
		return session.Provider{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return session.Provider{}, err
	}
	client, err := session.Ensure(ctx, endpoint, version, executable)
	if err != nil {
		return session.Provider{}, err
	}
	return session.Provider{Client: client, Source: s}, nil
}
func printStatus(ctx context.Context, pane string, output io.Writer) error {
	options, kind, err := tmux.RepositoryContext(ctx, pane)
	if err != nil {
		return err
	}
	provider, err := cachedSource(ctx, pane, source.Source{Options: options, Provider: ci.Provider(kind), GitLab: gitlab.Client{Executable: "glab"}, GitHub: github.Client{Executable: "gh"}})
	if err != nil {
		return err
	}
	snapshot, err := provider.Quick(ctx, 0)
	if err != nil {
		return err
	}
	text := "CI …"
	if snapshot.Pipeline != nil {
		finished, total, _ := snapshot.Progress()
		symbol := "○"
		switch snapshot.Pipeline.Status {
		case ci.Success:
			symbol = "✓"
		case ci.Failed:
			symbol = "×"
		case ci.Running:
			symbol = "↻"
		case ci.Canceled:
			symbol = "⊘"
		}
		text = fmt.Sprintf("CI %s %d/%d", symbol, finished, total)
		if snapshot.CacheError != "" {
			text += " !"
		}
	} else if !snapshot.Refreshing {
		text = "CI —"
	}
	_, err = fmt.Fprint(output, text)
	return err
}
func contextCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("context", flag.ContinueOnError)
	directory := flags.String("repo", "", "Agent worktree directory")
	hook := flags.Bool("hook", false, "Read the working directory from hook JSON on stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected context arguments")
	}
	if os.Getenv("TMUX") == "" || os.Getenv("TMUX_PANE") == "" {
		return nil
	}
	if *hook {
		value, err := tmux.HookDirectory(os.Stdin)
		if err != nil {
			return err
		}
		*directory = value
	}
	if *directory == "" {
		return fmt.Errorf("context requires --repo or --hook")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return tmux.RememberContext(ctx, os.Getenv("TMUX_PANE"), *directory)
}
