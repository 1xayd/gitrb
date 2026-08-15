package vcs

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"gitrb/internal/protocol"
)

func Status(dir string) protocol.GitStatus {
	result := protocol.GitStatus{Clean: true}
	if out, err := run(dir, "git", "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return result
	}
	result.IsRepository = true
	if out, err := run(dir, "git", "branch", "--show-current"); err == nil {
		result.Branch = strings.TrimSpace(out)
	}
	if out, err := run(dir, "git", "status", "--short"); err == nil {
		result.Porcelain = out
		result.Clean = strings.TrimSpace(out) == ""
	}
	return result
}

func Commit(dir, message string) (protocol.CommitResponse, error) {
	if strings.TrimSpace(message) == "" {
		return protocol.CommitResponse{}, fmt.Errorf("commit message is required")
	}
	if !Status(dir).IsRepository {
		return protocol.CommitResponse{}, fmt.Errorf("%s is not a Git repository", dir)
	}
	if _, err := run(dir, "git", "add", "--all"); err != nil {
		return protocol.CommitResponse{}, err
	}
	if _, err := run(dir, "git", "diff", "--cached", "--quiet"); err == nil {
		return protocol.CommitResponse{OK: true, Committed: false, Output: "nothing to commit"}, nil
	}
	out, err := run(dir, "git", "commit", "-m", message)
	if err != nil {
		return protocol.CommitResponse{}, err
	}
	return protocol.CommitResponse{OK: true, Committed: true, Output: out}, nil
}

func Push(dir, remote, branch string) (string, error) {
	if !Status(dir).IsRepository {
		return "", fmt.Errorf("%s is not a Git repository", dir)
	}
	if remote == "" {
		remote = "origin"
	}
	args := []string{"push", remote}
	if branch != "" {
		args = append(args, branch)
	}
	return run(dir, "git", args...)
}

func GitHubCreate(dir, repo, visibility, description string, push bool) (string, error) {
	if strings.TrimSpace(repo) == "" {
		return "", fmt.Errorf("GitHub repository name is required")
	}
	if visibility != "private" && visibility != "public" && visibility != "internal" {
		return "", fmt.Errorf("visibility must be private, public, or internal")
	}
	args := []string{"repo", "create", repo, "--source", ".", "--" + visibility}
	if description != "" {
		args = append(args, "--description", description)
	}
	if push {
		args = append(args, "--push")
	}
	return run(dir, "gh", args...)
}

func run(dir, command string, args ...string) (string, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("%s: %s", command, message)
	}
	return stdout.String(), nil
}
