package projector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/withoutboat/nix-bin/projector/internal/config"
	"github.com/withoutboat/nix-bin/projector/internal/git"
	"github.com/withoutboat/nix-bin/projector/internal/helm"
)

// Options holds runtime options for Projector.
type Options struct {
	ConfigFile     string
	HomeDir        string
	ProjectFilter  string
	DryRun         bool
	Verbose        bool
	ContinueErrors bool
}

// Projector manages cloning and pulling project repositories.
type Projector struct {
	opts   Options
	git    *git.Runner
	config *config.Config
}

// New creates a new Projector instance.
func New(opts Options, cfg *config.Config) *Projector {
	if opts.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			opts.HomeDir = home
		}
	}

	return &Projector{
		opts:   opts,
		git:    git.NewRunner(opts.DryRun, opts.Verbose),
		config: cfg,
	}
}

// Run executes the project synchronizations.
func (p *Projector) Run() error {
	var errs []string

	// Save project roots to $HOME/.config/projector/roots
	if !p.opts.DryRun {
		roots := p.config.ProjectRoots(p.opts.HomeDir)
		if err := config.SaveRootsFile(p.opts.HomeDir, roots); err != nil {
			fmt.Printf("Warning: failed to save project roots to ~/.config/projector/roots: %v\n", err)
		} else if p.opts.Verbose {
			fmt.Printf("==> Saved %d project roots to %s/.config/projector/roots\n", len(roots), p.opts.HomeDir)
		}
	}

	for _, project := range p.config.Projects {
		if p.opts.ProjectFilter != "" && !strings.EqualFold(p.opts.ProjectFilter, project.Name) {
			continue
		}

		fmt.Printf("\n==> Processing project: %s\n", project.Name)
		if err := p.processProject(project); err != nil {
			errs = append(errs, fmt.Sprintf("project %s: %v", project.Name, err))
			if !p.opts.ContinueErrors {
				return fmt.Errorf("failed processing project %s: %w", project.Name, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("encountered errors during synchronization:\n  %s", strings.Join(errs, "\n  "))
	}

	fmt.Println("\n==> Synchronization finished successfully!")
	return nil
}

func (p *Projector) processProject(proj config.Project) error {
	baseDir := filepath.Join(p.opts.HomeDir, proj.Name)

	if !git.DirExists(baseDir) && !p.opts.DryRun {
		if err := os.MkdirAll(baseDir, 0755); err != nil {
			return fmt.Errorf("failed to create base directory %s: %w", baseDir, err)
		}
	}

	if proj.ChartsRepo != nil || len(proj.Aliases) > 0 {
		return p.processChartsProject(proj, baseDir)
	}

	return p.processStandardProject(proj, baseDir)
}

func (p *Projector) processStandardProject(proj config.Project, baseDir string) error {
	for _, repo := range proj.Repos {
		targetDir := filepath.Join(baseDir, repo.Name)
		if err := p.syncStandardRepo(repo.URL, targetDir); err != nil {
			fmt.Printf("Error syncing %s: %v\n", repo.Name, err)
			if !p.opts.ContinueErrors {
				return err
			}
		}
	}
	return nil
}

func (p *Projector) syncStandardRepo(repoURL, targetDir string) error {
	if !git.DirExists(targetDir) {
		fmt.Printf("Cloning %s into %s...\n", repoURL, targetDir)
		return p.git.Clone(repoURL, targetDir)
	}

	fmt.Printf("Updating %s in %s...\n", repoURL, targetDir)
	return p.git.Pull(targetDir)
}

func (p *Projector) processChartsProject(proj config.Project, baseDir string) error {
	var chartsRepo config.RepoEntry
	var customRepos []config.RepoEntry

	if proj.ChartsRepo != nil {
		chartsRepo = *proj.ChartsRepo
		customRepos = proj.Repos
	} else if len(proj.Repos) > 0 {
		chartsRepo = proj.Repos[0]
		customRepos = proj.Repos[1:]
	} else {
		return nil
	}

	chartsTargetDir := filepath.Join(baseDir, chartsRepo.Name)
	fmt.Printf("Processing charts repository: %s\n", chartsRepo.Name)

	if err := p.syncStandardRepo(chartsRepo.URL, chartsTargetDir); err != nil {
		fmt.Printf("Error syncing charts repo %s: %v\n", chartsRepo.Name, err)
		if !p.opts.ContinueErrors {
			return err
		}
	}

	org := extractOrgFromURL(chartsRepo.URL, "")

	if git.DirExists(chartsTargetDir) || p.opts.DryRun {
		discovered, err := helm.FindDiscoveredReposInDir(chartsTargetDir, org, proj.Aliases)
		if err != nil {
			fmt.Printf("Warning: error scanning helm charts in %s: %v\n", chartsTargetDir, err)
		} else {
			fmt.Printf("Discovered %d GitHub service repositories in charts\n", len(discovered))
			for _, d := range discovered {
				target := filepath.Join(baseDir, d.RepoName)
				if err := p.syncChartsServiceRepo(d, target); err != nil {
					fmt.Printf("Error syncing service repo %s: %v\n", d.RepoName, err)
					if !p.opts.ContinueErrors {
						return err
					}
				}
			}
		}
	}

	if len(customRepos) > 0 {
		fmt.Printf("Processing %d custom repositories in project...\n", len(customRepos))
		for _, customRepo := range customRepos {
			targetDir := filepath.Join(baseDir, customRepo.Name)
			if err := p.syncStandardRepo(customRepo.URL, targetDir); err != nil {
				fmt.Printf("Error syncing custom repo %s: %v\n", customRepo.Name, err)
				if !p.opts.ContinueErrors {
					return err
				}
			}
		}
	}

	return nil
}

func (p *Projector) syncChartsServiceRepo(d helm.DiscoveredRepo, targetDir string) error {
	if !git.DirExists(targetDir) {
		fmt.Printf("Cloning service repo %s (%s) into %s...\n", d.RepoName, d.URL, targetDir)
		if err := p.git.Clone(d.URL, targetDir); err != nil {
			return err
		}

		if d.BranchOrTag != "" {
			remotes, err := p.git.ListRemoteBranches(targetDir)
			if err != nil {
				// Fallback to checking out tag/branch directly
				fmt.Printf("Checking out branch/tag %s in %s...\n", d.BranchOrTag, targetDir)
				return p.git.Checkout(targetDir, d.BranchOrTag)
			}
			branch := git.ResolveBranch(d.BranchOrTag, remotes)
			fmt.Printf("Checking out resolved branch %s (from tag %s) in %s...\n", branch, d.BranchOrTag, targetDir)
			return p.git.Checkout(targetDir, branch)
		}
		return nil
	}

	fmt.Printf("Updating service repo %s in %s...\n", d.RepoName, targetDir)
	if err := p.git.Fetch(targetDir); err != nil {
		return err
	}

	if d.BranchOrTag != "" {
		remotes, err := p.git.ListRemoteBranches(targetDir)
		if err != nil {
			fmt.Printf("Checking out and pulling branch/tag %s in %s...\n", d.BranchOrTag, targetDir)
			return p.git.PullBranch(targetDir, d.BranchOrTag)
		}
		branch := git.ResolveBranch(d.BranchOrTag, remotes)
		fmt.Printf("Checking out and pulling resolved branch %s (from tag %s) in %s...\n", branch, d.BranchOrTag, targetDir)
		return p.git.PullBranch(targetDir, branch)
	}

	return p.git.Pull(targetDir)
}

func extractOrgFromURL(rawURL, fallback string) string {
	u := strings.TrimSpace(rawURL)
	u = strings.TrimSuffix(u, ".git")

	// e.g. git@github.com:owner/repo
	if colonIdx := strings.LastIndex(u, ":"); colonIdx != -1 {
		path := u[colonIdx+1:]
		parts := strings.Split(path, "/")
		if len(parts) >= 2 && parts[0] != "" {
			return parts[0]
		}
	}

	// e.g. https://github.com/owner/repo
	if strings.Contains(u, "://") {
		idx := strings.Index(u, "://")
		path := u[idx+3:]
		parts := strings.Split(path, "/")
		if len(parts) >= 3 && parts[1] != "" {
			return parts[1]
		}
	}

	return fallback
}
