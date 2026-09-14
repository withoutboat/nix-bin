package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/withoutboat/nix-bin/projector/internal/config"
	"github.com/withoutboat/nix-bin/projector/internal/projector"
)

var (
	version = "0.1.0"
)

func main() {
	var isRootsSubcommand bool
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "roots" || args[0] == "list-roots") {
		isRootsSubcommand = true
		args = args[1:]
	}

	var (
		flagFile        string
		flagProject     string
		flagDryRun      bool
		flagVerbose     bool
		flagContinueErr bool
		flagRoots       bool
		flagVersion     bool
	)

	fs := flag.NewFlagSet("projector", flag.ExitOnError)
	fs.StringVar(&flagFile, "file", "", "Path to projects YAML file (can also be passed as positional argument)")
	fs.StringVar(&flagFile, "f", "", "Alias for --file")
	fs.StringVar(&flagFile, "config", "", "Alias for --file")
	fs.StringVar(&flagFile, "c", "", "Alias for --file")
	fs.StringVar(&flagProject, "project", "", "Only process the specified project name")
	fs.StringVar(&flagProject, "p", "", "Alias for --project")
	fs.BoolVar(&flagDryRun, "dry-run", false, "Simulate actions without cloning or pulling repositories")
	fs.BoolVar(&flagDryRun, "n", false, "Alias for --dry-run")
	fs.BoolVar(&flagVerbose, "verbose", false, "Enable verbose output")
	fs.BoolVar(&flagVerbose, "v", false, "Alias for --verbose")
	fs.BoolVar(&flagContinueErr, "continue-on-error", true, "Continue with next repo/project if an error occurs")
	fs.BoolVar(&flagRoots, "roots", false, "Print project root directories and exit")
	fs.BoolVar(&flagRoots, "r", false, "Alias for --roots")
	fs.BoolVar(&flagVersion, "version", false, "Print version and exit")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  projector [options] [path_to_projects.yml]   Synchronize project repositories\n")
		fmt.Fprintf(os.Stderr, "  projector roots [options] [path_to_projects.yml] Output project root paths\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if flagVersion {
		fmt.Printf("projector version %s\n", version)
		os.Exit(0)
	}

	configFile := resolveConfigFile(flagFile, fs.Args())

	// Handle roots subcommand or --roots flag
	if isRootsSubcommand || flagRoots {
		handleRoots(configFile)
		return
	}

	if configFile == "" {
		fmt.Fprintln(os.Stderr, "Error: No projects YAML file specified.")
		fmt.Fprintln(os.Stderr, "Provide a path via argument, --file flag, or PROJECTS_FILE environment variable.")
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("==> Loading configuration from %s\n", configFile)
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	opts := projector.Options{
		ConfigFile:     configFile,
		ProjectFilter:  flagProject,
		DryRun:         flagDryRun,
		Verbose:        flagVerbose,
		ContinueErrors: flagContinueErr,
	}

	p := projector.New(opts, cfg)
	if err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
}

func resolveConfigFile(flagFile string, remainingArgs []string) string {
	if flagFile != "" {
		return flagFile
	}

	if len(remainingArgs) > 0 && remainingArgs[0] != "" {
		return remainingArgs[0]
	}

	for _, envVar := range []string{"PROJECTS_FILE", "PROJECTS_CONFIG", "PROJECTOR_CONFIG"} {
		if val := os.Getenv(envVar); val != "" {
			return val
		}
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "nix-home", "secrets", "projects.yml"),
		filepath.Join(home, ".config", "projector", "projects.yml"),
		filepath.Join(home, ".config", "projector", "projects.yaml"),
		"projects.yml",
		"projects.yaml",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func handleRoots(configFile string) {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving home directory: %v\n", err)
		os.Exit(1)
	}

	if configFile != "" {
		cfg, err := config.LoadConfig(configFile)
		if err == nil {
			roots := cfg.ProjectRoots(home)
			_ = config.SaveRootsFile(home, roots)
			for _, r := range roots {
				fmt.Println(r)
			}
			return
		}
	}

	// Fallback to reading existing roots file if config file couldn't be loaded
	roots, err := config.ReadRootsFile(home)
	if err == nil && len(roots) > 0 {
		for _, r := range roots {
			fmt.Println(r)
		}
		return
	}

	if configFile != "" {
		fmt.Fprintf(os.Stderr, "Error: could not load projects configuration from %s\n", configFile)
	} else {
		fmt.Fprintf(os.Stderr, "Error: no project roots found and no configuration file specified.\n")
	}
	os.Exit(1)
}
