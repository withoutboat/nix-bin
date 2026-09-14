package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes git commands against repositories.
type Runner struct {
	DryRun  bool
	Verbose bool
}

// NewRunner creates a new GitRunner.
func NewRunner(dryRun, verbose bool) *Runner {
	return &Runner{
		DryRun:  dryRun,
		Verbose: verbose,
	}
}

func (r *Runner) runCmd(dir string, args ...string) (string, error) {
	cmdStr := fmt.Sprintf("git %s", strings.Join(args, " "))
	if dir != "" {
		cmdStr = fmt.Sprintf("git -C %s %s", dir, strings.Join(args, " "))
	}

	if r.Verbose {
		fmt.Printf("==> %s\n", cmdStr)
	}

	if r.DryRun {
		// For read-only commands (branch -r, status), we still execute them in dry-run if the dir exists
		isReadOnly := len(args) > 0 && (args[0] == "branch" || args[0] == "status" || args[0] == "ls-remote" || args[0] == "rev-parse")
		if !isReadOnly {
			fmt.Printf("[dry-run] %s\n", cmdStr)
			return "", nil
		}
	}

	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git command failed (%s): %w\nOutput: %s\nError: %s", cmdStr, err, stdout.String(), stderr.String())
	}

	return stdout.String(), nil
}

// Clone clones a repository into targetDir.
func (r *Runner) Clone(url, targetDir string) error {
	_, err := r.runCmd("", "clone", url, targetDir)
	return err
}

// Pull pulls the current branch.
func (r *Runner) Pull(repoDir string) error {
	_, err := r.runCmd(repoDir, "pull")
	return err
}

// Fetch fetches the remote origin.
func (r *Runner) Fetch(repoDir string) error {
	_, err := r.runCmd(repoDir, "fetch", "origin")
	return err
}

// Checkout checks out a branch or ref.
func (r *Runner) Checkout(repoDir, branch string) error {
	_, err := r.runCmd(repoDir, "checkout", branch)
	return err
}

// PullBranch checks out a branch and pulls it from origin.
func (r *Runner) PullBranch(repoDir, branch string) error {
	if branch != "" {
		if err := r.Checkout(repoDir, branch); err != nil {
			return err
		}
		_, err := r.runCmd(repoDir, "pull", "origin", branch)
		return err
	}
	return r.Pull(repoDir)
}

// ListRemoteBranches returns a list of remote branch names (without the 'origin/' prefix).
func (r *Runner) ListRemoteBranches(repoDir string) ([]string, error) {
	out, err := r.runCmd(repoDir, "branch", "-r")
	if err != nil {
		return nil, err
	}

	var branches []string
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "->") {
			continue // skip origin/HEAD -> origin/main
		}
		// line is like origin/main or origin/dev/v1
		if strings.HasPrefix(line, "origin/") {
			branch := strings.TrimPrefix(line, "origin/")
			branches = append(branches, branch)
		}
	}
	return branches, nil
}

// ResolveBranch finds the best matching remote branch for a given Docker image tag.
// Docker image tags may replace '/' with '-' (e.g. dev-v1 -> dev/v1).
func ResolveBranch(targetTag string, remoteBranches []string) string {
	if targetTag == "" {
		return ""
	}

	// 1. Exact match
	for _, b := range remoteBranches {
		if b == targetTag {
			return b
		}
	}

	// 2. Check if replacing '/' with '-' in remote branch produces targetTag
	for _, b := range remoteBranches {
		substituted := strings.ReplaceAll(b, "/", "-")
		if substituted == targetTag {
			return b
		}
	}

	// 3. Check if replacing '-' with '/' in targetTag matches remote branch
	slashSubstituted := strings.ReplaceAll(targetTag, "-", "/")
	for _, b := range remoteBranches {
		if b == slashSubstituted {
			return b
		}
	}

	// 4. Fallback to targetTag directly
	return targetTag
}

// DirExists returns true if path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
