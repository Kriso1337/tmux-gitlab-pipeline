package repository

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"tmux-gitlab-pipeline/internal/process"
)

type Repository struct {
	Directory string
	Host      string
	Project   string
	Remote    string
	Branch    string
	SHA       string
}

type Options struct {
	Directory string
	Remote    string
	Host      string
}

func Resolve(ctx context.Context, options Options) (Repository, error) {
	directory, err := filepath.Abs(options.Directory)
	if err != nil {
		return Repository{}, fmt.Errorf("resolve repository path: %w", err)
	}
	root, err := process.Output(ctx, directory, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return Repository{}, fmt.Errorf("not a Git working tree: %w", err)
	}
	sha, err := process.Output(ctx, root, "git", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Repository{}, fmt.Errorf("repository has no commit: %w", err)
	}
	branch, err := process.Output(ctx, root, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil && !process.ExitCode(err, 1) {
		return Repository{}, err
	}
	remotesText, err := process.Output(ctx, root, "git", "remote")
	if err != nil {
		return Repository{}, err
	}
	remotes := strings.Fields(remotesText)
	remote := options.Remote
	if remote == "" && branch != "" {
		remote, err = process.Output(ctx, root, "git", "config", "--get", "branch."+branch+".remote")
		if err != nil && !process.ExitCode(err, 1) {
			return Repository{}, err
		}
		if remote == "." {
			remote = ""
		}
	}
	if remote == "" {
		switch {
		case slices.Contains(remotes, "origin"):
			remote = "origin"
		case len(remotes) == 1:
			remote = remotes[0]
		case len(remotes) == 0:
			return Repository{}, fmt.Errorf("repository has no remote")
		default:
			return Repository{}, fmt.Errorf("multiple remotes (%s); select one with --remote", strings.Join(remotes, ", "))
		}
	}
	if !slices.Contains(remotes, remote) {
		return Repository{}, fmt.Errorf("remote %q does not exist", remote)
	}
	remoteURL, err := process.Output(ctx, root, "git", "remote", "get-url", "--", remote)
	if err != nil {
		return Repository{}, err
	}
	host, project, err := ParseRemote(remoteURL)
	if err != nil {
		return Repository{}, err
	}
	if options.Host != "" {
		host = options.Host
	}
	if err := ValidateHost(host); err != nil {
		return Repository{}, err
	}
	return Repository{Directory: root, Host: host, Project: project, Remote: remote, Branch: branch, SHA: sha}, nil
}

func ParseRemote(value string) (string, string, error) {
	var host, project string
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", "", fmt.Errorf("invalid remote URL")
		}
		if parsed.Scheme != "ssh" && parsed.Scheme != "https" && parsed.Scheme != "http" {
			return "", "", fmt.Errorf("remote must use SSH or HTTP(S)")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", fmt.Errorf("remote URL must not contain a query or fragment")
		}
		host = parsed.Host
		if parsed.Scheme == "ssh" {
			host = parsed.Hostname()
		}
		project = strings.TrimPrefix(parsed.Path, "/")
	} else {
		left, right, ok := strings.Cut(value, ":")
		if !ok || strings.Contains(left, "/") {
			return "", "", fmt.Errorf("local remotes are not supported")
		}
		_, after, hasUser := strings.Cut(left, "@")
		if hasUser {
			left = after
		}
		host, project = left, strings.TrimPrefix(right, "/")
	}
	project = strings.TrimSuffix(project, ".git")
	if err := ValidateHost(host); err != nil {
		return "", "", err
	}
	parts := strings.Split(project, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("remote must contain namespace and project")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\x00\r\n\\") {
			return "", "", fmt.Errorf("invalid remote project path")
		}
	}
	return host, project, nil
}

func ValidateHost(host string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*(?::[0-9]+)?$`).MatchString(host) {
		return fmt.Errorf("invalid CI hostname; use --host for an SSH alias")
	}
	return nil
}
