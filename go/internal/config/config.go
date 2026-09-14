package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RepoEntry represents a repository with a name and a clone URL.
type RepoEntry struct {
	Name string
	URL  string
}

// Project represents a named project containing repositories.
type Project struct {
	Name       string
	ChartsRepo *RepoEntry
	Repos      []RepoEntry
	Aliases    map[string]string
}

// Config represents the loaded configuration of all projects.
type Config struct {
	Projects []Project
}

// RepoNameFromURL extracts the repository name from a git URL.
func RepoNameFromURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimRight(u, "/")

	// Handle scp-like syntax e.g. git@github.com:owner/repo
	if idx := strings.LastIndex(u, "/"); idx != -1 {
		return u[idx+1:]
	}
	if idx := strings.LastIndex(u, ":"); idx != -1 {
		return u[idx+1:]
	}
	return u
}

// ExpandPath expands ~ to the user's home directory.
func ExpandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("unable to determine home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// ParseConfig parses YAML data into a Config struct.
// It supports both mapping formats (project: [repos...]) and sequence formats (- project: [repos...]).
func ParseConfig(data []byte) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	if len(root.Content) == 0 {
		return &Config{}, nil
	}

	docNode := root.Content[0]
	cfg := &Config{}

	switch docNode.Kind {
	case yaml.MappingNode:
		// Map format:
		// project-name:
		//   - repo1
		for i := 0; i < len(docNode.Content); i += 2 {
			keyNode := docNode.Content[i]
			valNode := docNode.Content[i+1]
			projectName := keyNode.Value

			project, err := parseProjectNode(projectName, valNode)
			if err != nil {
				return nil, fmt.Errorf("failed parsing project %q: %w", projectName, err)
			}
			cfg.Projects = append(cfg.Projects, project)
		}

	case yaml.SequenceNode:
		// Sequence format:
		// - project-name:
		//     - repo1
		for _, item := range docNode.Content {
			if item.Kind == yaml.MappingNode {
				for i := 0; i < len(item.Content); i += 2 {
					keyNode := item.Content[i]
					valNode := item.Content[i+1]
					projectName := keyNode.Value

					project, err := parseProjectNode(projectName, valNode)
					if err != nil {
						return nil, fmt.Errorf("failed parsing project %q: %w", projectName, err)
					}
					cfg.Projects = append(cfg.Projects, project)
				}
			}
		}

	default:
		return nil, fmt.Errorf("unexpected yaml root kind: %d", docNode.Kind)
	}

	return cfg, nil
}

func parseProjectNode(projectName string, valNode *yaml.Node) (Project, error) {
	proj := Project{
		Name:    projectName,
		Aliases: make(map[string]string),
	}

	switch valNode.Kind {
	case yaml.SequenceNode:
		repos, aliases, err := parseReposAndAliasesNode(valNode)
		if err != nil {
			return proj, err
		}
		proj.Repos = repos
		for k, v := range aliases {
			proj.Aliases[k] = v
		}

	case yaml.MappingNode:
		if isObjectProjectMapping(valNode) {
			var chartsRepos []RepoEntry
			var generalRepos []RepoEntry

			for i := 0; i < len(valNode.Content); i += 2 {
				key := strings.ToLower(strings.TrimSpace(valNode.Content[i].Value))
				val := valNode.Content[i+1]

				switch key {
				case "aliases":
					aliases, err := parseAliasesNode(val)
					if err != nil {
						return proj, fmt.Errorf("failed parsing aliases: %w", err)
					}
					for k, v := range aliases {
						proj.Aliases[k] = v
					}

				case "charts", "chart":
					repos, _, err := parseReposAndAliasesNode(val)
					if err != nil {
						return proj, fmt.Errorf("failed parsing charts: %w", err)
					}
					chartsRepos = append(chartsRepos, repos...)

				case "repos", "repositories":
					repos, aliases, err := parseReposAndAliasesNode(val)
					if err != nil {
						return proj, fmt.Errorf("failed parsing repos: %w", err)
					}
					for k, v := range aliases {
						proj.Aliases[k] = v
					}
					generalRepos = append(generalRepos, repos...)
				}
			}
			if len(chartsRepos) > 0 {
				proj.ChartsRepo = &chartsRepos[0]
			}
			proj.Repos = generalRepos
		} else {
			// Old-style mapping of repoName: repoURL
			repos, err := parseReposNode(valNode)
			if err != nil {
				return proj, err
			}
			proj.Repos = repos
		}

	case yaml.ScalarNode:
		repo, err := parseSingleRepoNode(valNode)
		if err != nil {
			return proj, err
		}
		proj.Repos = append(proj.Repos, repo)

	default:
		return proj, fmt.Errorf("unexpected project node kind: %d", valNode.Kind)
	}

	if proj.ChartsRepo == nil && len(proj.Aliases) > 0 && len(proj.Repos) > 0 {
		charts := proj.Repos[0]
		proj.ChartsRepo = &charts
		proj.Repos = proj.Repos[1:]
	}

	return proj, nil
}

func isObjectProjectMapping(node *yaml.Node) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(node.Content); i += 2 {
		k := strings.ToLower(strings.TrimSpace(node.Content[i].Value))
		if k == "aliases" || k == "charts" || k == "chart" || k == "repos" || k == "repositories" {
			return true
		}
	}
	return false
}

func parseAliasesNode(node *yaml.Node) (map[string]string, error) {
	aliases := make(map[string]string)
	if node == nil || (node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "" || node.Value == "~")) {
		return aliases, nil
	}

	if node.Kind == yaml.ScalarNode {
		// Encrypted multiline/JSON string: unmarshal as YAML or parse key: value lines
		var subMap map[string]string
		if err := yaml.Unmarshal([]byte(node.Value), &subMap); err == nil && len(subMap) > 0 {
			return subMap, nil
		}
		lines := strings.Split(node.Value, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			var k, v string
			if idx := strings.Index(line, ":"); idx != -1 {
				k = strings.TrimSpace(line[:idx])
				v = strings.TrimSpace(line[idx+1:])
			} else if idx := strings.Index(line, "="); idx != -1 {
				k = strings.TrimSpace(line[:idx])
				v = strings.TrimSpace(line[idx+1:])
			}
			if k != "" && v != "" {
				aliases[k] = v
			}
		}
		return aliases, nil
	}

	if node.Kind == yaml.SequenceNode {
		// List of strings: ["key: value", "key=value", ...]
		for _, elem := range node.Content {
			if elem.Kind == yaml.ScalarNode {
				val := strings.TrimSpace(elem.Value)
				var k, v string
				if idx := strings.Index(val, ":"); idx != -1 {
					k = strings.TrimSpace(val[:idx])
					v = strings.TrimSpace(val[idx+1:])
				} else if idx := strings.Index(val, "="); idx != -1 {
					k = strings.TrimSpace(val[:idx])
					v = strings.TrimSpace(val[idx+1:])
				}
				if k != "" && v != "" {
					aliases[k] = v
				}
			}
		}
		return aliases, nil
	}

	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			k := strings.TrimSpace(node.Content[i].Value)
			v := strings.TrimSpace(node.Content[i+1].Value)
			if k != "" && v != "" {
				aliases[k] = v
			}
		}
		return aliases, nil
	}

	return aliases, nil
}

func isAliasesMappingNode(node *yaml.Node) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(node.Content); i += 2 {
		if strings.EqualFold(node.Content[i].Value, "aliases") {
			return true
		}
	}
	return false
}

func extractAliasesFromMappingNode(node *yaml.Node) (map[string]string, error) {
	for i := 0; i < len(node.Content); i += 2 {
		if strings.EqualFold(node.Content[i].Value, "aliases") {
			return parseAliasesNode(node.Content[i+1])
		}
	}
	return nil, nil
}

func parseReposAndAliasesNode(node *yaml.Node) ([]RepoEntry, map[string]string, error) {
	var repos []RepoEntry
	aliases := make(map[string]string)

	switch node.Kind {
	case yaml.SequenceNode:
		for _, elem := range node.Content {
			if elem.Kind == yaml.MappingNode && isAliasesMappingNode(elem) {
				extracted, err := extractAliasesFromMappingNode(elem)
				if err != nil {
					return nil, nil, err
				}
				for k, v := range extracted {
					aliases[k] = v
				}
				continue
			}
			repo, err := parseSingleRepoNode(elem)
			if err != nil {
				return nil, nil, err
			}
			repos = append(repos, repo)
		}
	case yaml.ScalarNode:
		repo, err := parseSingleRepoNode(node)
		if err != nil {
			return nil, nil, err
		}
		repos = append(repos, repo)
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			name := node.Content[i].Value
			val := node.Content[i+1]
			if val.Kind == yaml.ScalarNode {
				repos = append(repos, RepoEntry{
					Name: name,
					URL:  val.Value,
				})
			}
		}
	}

	return repos, aliases, nil
}

func parseReposNode(node *yaml.Node) ([]RepoEntry, error) {
	repos, _, err := parseReposAndAliasesNode(node)
	return repos, err
}

func parseSingleRepoNode(node *yaml.Node) (RepoEntry, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		url := strings.TrimSpace(node.Value)
		return RepoEntry{
			Name: RepoNameFromURL(url),
			URL:  url,
		}, nil
	case yaml.MappingNode:
		var name, url string
		for i := 0; i < len(node.Content); i += 2 {
			k := node.Content[i].Value
			v := node.Content[i+1].Value
			if strings.EqualFold(k, "name") {
				name = v
			} else if strings.EqualFold(k, "url") {
				url = v
			} else if len(node.Content) == 2 {
				// Single key-value pair where key is name and value is url
				name = k
				url = v
			}
		}
		if url == "" {
			return RepoEntry{}, fmt.Errorf("missing repo URL in mapping")
		}
		if name == "" {
			name = RepoNameFromURL(url)
		}
		return RepoEntry{
			Name: name,
			URL:  url,
		}, nil
	default:
		return RepoEntry{}, fmt.Errorf("unexpected node kind for repo: %d", node.Kind)
	}
}

// LoadConfig reads and parses the YAML configuration file from the given path.
func LoadConfig(filePath string) (*Config, error) {
	expanded, err := ExpandPath(filePath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", filePath, err)
	}

	return ParseConfig(data)
}

// ProjectRoots returns the absolute directory paths for each project.
func (c *Config) ProjectRoots(homeDir string) []string {
	var roots []string
	for _, p := range c.Projects {
		roots = append(roots, filepath.Join(homeDir, p.Name))
	}
	return roots
}

// SaveRootsFile writes project roots to $HOME/.config/projector/roots.
func SaveRootsFile(homeDir string, roots []string) error {
	configDir := filepath.Join(homeDir, ".config", "projector")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create projector config directory %s: %w", configDir, err)
	}
	rootsPath := filepath.Join(configDir, "roots")
	content := strings.Join(roots, "\n")
	if len(roots) > 0 {
		content += "\n"
	}
	return os.WriteFile(rootsPath, []byte(content), 0644)
}

// ReadRootsFile reads previously saved project roots from $HOME/.config/projector/roots.
func ReadRootsFile(homeDir string) ([]string, error) {
	rootsPath := filepath.Join(homeDir, ".config", "projector", "roots")
	data, err := os.ReadFile(rootsPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}
