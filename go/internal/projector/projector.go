package projector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/withoutboat/nix-bin/projector/internal/config"
	"github.com/withoutboat/nix-bin/projector/internal/git"
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

	for _, repo := range proj.Repos {
		targetDir := filepath.Join(baseDir, repo.Name)
		if err := p.syncRepo(repo.URL, targetDir); err != nil {
			fmt.Printf("Error syncing %s: %v\n", repo.Name, err)
			if !p.opts.ContinueErrors {
				return err
			}
		}
	}
	return nil
}

func (p *Projector) syncRepo(repoURL, targetDir string) error {
	if !git.DirExists(targetDir) {
		fmt.Printf("Cloning %s into %s...\n", repoURL, targetDir)
		return p.git.Clone(repoURL, targetDir)
	}

	fmt.Printf("Updating %s in %s...\n", repoURL, targetDir)
	return p.git.Pull(targetDir)
}
