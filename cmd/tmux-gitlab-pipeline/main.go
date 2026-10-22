package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"tmux-gitlab-pipeline/internal/browser"
	"tmux-gitlab-pipeline/internal/ci"
	"tmux-gitlab-pipeline/internal/github"
	"tmux-gitlab-pipeline/internal/gitlab"
	"tmux-gitlab-pipeline/internal/process"
	"tmux-gitlab-pipeline/internal/repository"
	"tmux-gitlab-pipeline/internal/session"
	"tmux-gitlab-pipeline/internal/source"
	"tmux-gitlab-pipeline/internal/tmux"
	"tmux-gitlab-pipeline/internal/tui"
)

var version = "0.5.1-dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "tmux-gitlab-pipeline:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "context":
			return contextCommand(ctx, args[1:])
		case "serve":
			return serve(ctx, args[1:])
		case "cache":
			return cacheCommand(ctx, args[1:], output)
		case "status":
			return statusCommand(ctx, args[1:], output)
		case "version", "--version":
			_, err := fmt.Fprintln(output, version)
			return err
		case "doctor":
			return doctor(ctx, output)
		case "bind":
			return tmux.Bind(ctx)
		case "popup":
			flags := flag.NewFlagSet("popup", flag.ContinueOnError)
			pane := flags.String("pane", "", "Source tmux pane")
			client := flags.String("client", "", "Source tmux client")
			if err := flags.Parse(args[1:]); err != nil {
				return err
			}
			return tmux.Popup(ctx, *pane, *client)
		}
	}
	flags := flag.NewFlagSet("tmux-gitlab-pipeline", flag.ContinueOnError)
	flags.SetOutput(output)
	directory := flags.String("repo", ".", "Git working tree directory")
	remote := flags.String("remote", "", "Git remote (upstream, origin, or the only remote)")
	host := flags.String("host", "", "Override CI hostname, for SSH aliases")
	backend := flags.String("provider", "auto", "auto, gitlab, or github (set explicitly for enterprise hosts)")
	interval := flags.Duration("interval", 5*time.Second, "Refresh interval (2s to 5m)")
	theme := flags.String("theme", "auto", "auto, dark, or light")
	icons := flags.String("icons", "unicode", "unicode, nerd, or ascii")
	noMotion := flags.Bool("no-motion", false, "Disable spinner animation")
	trueColor := flags.Bool("true-color", false, "Use RGB output (enabled automatically for RGB-capable tmux clients)")
	sourcePane := flags.String("source-pane", "", "Source tmux pane for remembering worktree selection")
	paneContext := flags.String("pane-context", "", "Source tmux process identity")
	chooseWorktree := flags.Bool("choose-worktree", false, "Choose a worktree before loading CI")
	pipeline := flags.Int64("pipeline", 0, "Pin a pipeline ID")
	demo := flags.Bool("demo", false, "Show simulated data without GitLab access")
	snapshot := flags.Bool("snapshot", false, "Print one snapshot without entering the TUI")
	var usageErr error
	flags.Usage = func() {
		var help strings.Builder
		help.WriteString("tmux-gitlab-pipeline [options]\n\nCommands: doctor, version, bind, popup\nKeys: arrows/hjkl, Enter log, Esc back/close, r refresh, p pipeline, a auto, m MR/PR in browser, w worktree\nLog: / search, n next, f follow, PgUp/PgDown, Home/End\n\n")
		flags.SetOutput(&help)
		flags.PrintDefaults()
		flags.SetOutput(output)
		_, usageErr = fmt.Fprint(output, help.String())
	}
	if err := flags.Parse(args); err != nil {
		if usageErr != nil {
			return usageErr
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *interval < 2*time.Second || *interval > 5*time.Minute {
		return fmt.Errorf("interval must be between 2s and 5m")
	}
	if !slices.Contains([]string{"auto", "dark", "light"}, *theme) {
		return fmt.Errorf("theme must be auto, dark, or light")
	}
	if !slices.Contains([]string{"unicode", "nerd", "ascii"}, *icons) {
		return fmt.Errorf("icons must be unicode, nerd, or ascii")
	}
	if *pipeline < 0 {
		return fmt.Errorf("pipeline ID must not be negative")
	}
	if !slices.Contains([]string{"auto", "gitlab", "github"}, *backend) {
		return fmt.Errorf("provider must be auto, gitlab, or github")
	}
	if *sourcePane != "" && !regexp.MustCompile(`^%[0-9]+$`).MatchString(*sourcePane) {
		return fmt.Errorf("invalid source pane")
	}
	options := tui.Options{Interval: *interval, Theme: *theme, Icons: *icons, Motion: !*noMotion, PipelineID: *pipeline}
	var provider tui.Provider
	if *demo {
		provider = gitlab.DemoSource{Started: time.Now()}
	} else {
		repoOptions := repository.Options{Directory: *directory, Remote: *remote, Host: *host}
		kind := *backend
		if kind == "auto" {
			repo, err := repository.Resolve(ctx, repoOptions)
			if err != nil {
				return err
			}
			kind = string(source.Detect(repo.Host, ""))
		}
		binary := "glab"
		if kind == "github" {
			binary = "gh"
		}
		executable, err := exec.LookPath(binary)
		if err != nil {
			return fmt.Errorf("%s is required; install it and run %s auth login: %w", binary, binary, err)
		}
		s := source.Source{Options: repoOptions, GitLab: gitlab.Client{Executable: "glab"}, GitHub: github.Client{Executable: "gh"}}
		if *backend != "auto" {
			s.Provider = ci.Provider(*backend)
		}
		if kind == "github" {
			s.GitHub.Executable = executable
		} else {
			s.GitLab.Executable = executable
		}
		provider = s
		options.Features = s
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		options.DownloadDirectory = filepath.Join(home, "Downloads", "ci-pipelines")
		var cacheClient *session.Client
		if !*snapshot && os.Getenv("TMUX") != "" {
			endpoint, err := session.Locate(ctx, *sourcePane)
			if err != nil {
				return err
			}
			binary, err := os.Executable()
			if err != nil {
				return err
			}
			client, err := session.Ensure(ctx, endpoint, version, binary)
			if err != nil {
				return err
			}
			cacheClient = &client
			provider = session.Provider{Client: client, Source: s}
		}
		options.Directory = *directory
		options.OpenChange = func(ctx context.Context, repo repository.Repository) error {
			address, err := s.ChangeURL(ctx, repo)
			if err != nil {
				return err
			}
			return (browser.Opener{}).Open(ctx, address)
		}
		if !*snapshot {
			trees, err := repository.Worktrees(ctx, *directory)
			if err != nil {
				return err
			}
			options.Worktrees, options.ChooseWorktree = trees, *chooseWorktree
			options.SwitchRepository = func(ctx context.Context, directory string) (tui.Provider, error) {
				selected := s
				selected.Options.Directory = directory
				if _, err := repository.Resolve(ctx, selected.Options); err != nil {
					return nil, err
				}
				if *sourcePane != "" {
					if err := tmux.RememberWorktree(ctx, *sourcePane, *paneContext, directory); err != nil {
						return nil, err
					}
				}
				if cacheClient != nil {
					return session.Provider{Client: *cacheClient, Source: selected}, nil
				}
				return selected, nil
			}
		}
	}
	if *snapshot {
		data, err := provider.Load(ctx, *pipeline)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, tui.RenderSnapshot(data, options, 100, 28))
		return err
	}
	programOptions := []tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(output)}
	if *trueColor {
		programOptions = append(programOptions, tea.WithColorProfile(colorprofile.TrueColor))
	}
	_, err := tea.NewProgram(tui.New(ctx, provider, options), programOptions...).Run()
	return err
}

func doctor(ctx context.Context, output io.Writer) error {
	var problems []error
	var report strings.Builder
	providerAvailable := false
	for _, name := range []string{"tmux", "git", "ps", "glab", "gh"} {
		arguments := []string{"--version"}
		if name == "tmux" {
			arguments = []string{"-V"}
		}
		if name == "ps" {
			arguments = []string{"-p", strconv.Itoa(os.Getpid()), "-o", "pid="}
		}
		value, err := process.Output(ctx, "", name, arguments...)
		if err != nil {
			if name != "gh" && name != "glab" {
				problems = append(problems, err)
			}
			report.WriteString(name + ": unavailable\n")
			continue
		}
		if name == "gh" || name == "glab" {
			providerAvailable = true
		}
		if name == "ps" {
			value = "ps: available"
		}
		report.WriteString(value + "\n")
	}
	if !providerAvailable {
		problems = append(problems, fmt.Errorf("install glab for GitLab or gh for GitHub Actions"))
	}
	report.WriteString("TERM=" + os.Getenv("TERM") + "\nTERM_PROGRAM=" + os.Getenv("TERM_PROGRAM") + "\ntmux session: " + strconv.FormatBool(os.Getenv("TMUX") != "") + "\n")
	report.WriteString("Authentication: glab auth login --hostname <gitlab-host> or gh auth login --hostname <github-host>\nFonts: configure a Nerd Font in your terminal, then set @gitlab-pipeline-icons nerd\n")
	if _, err := fmt.Fprint(output, report.String()); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}
