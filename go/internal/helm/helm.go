package helm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DiscoveredRepo represents a repository reference extracted from Helm charts.
type DiscoveredRepo struct {
	RepoName    string
	BranchOrTag string
	URL         string
	SourceFile  string
}

// ParseImageReference extracts the repository name, branch/tag, and git URL from an image string.
func ParseImageReference(raw string, defaultOrg string, aliases ...map[string]string) *DiscoveredRepo {
	trimmed := strings.TrimSpace(raw)
	if atIdx := strings.Index(trimmed, "@"); atIdx != -1 {
		trimmed = trimmed[:atIdx]
	}

	var pathPart, tagPart string
	if colonIdx := strings.LastIndex(trimmed, ":"); colonIdx != -1 {
		pathPart = trimmed[:colonIdx]
		tagPart = trimmed[colonIdx+1:]
	} else {
		pathPart = trimmed
	}

	segments := strings.Split(strings.Trim(pathPart, "/"), "/")
	if len(segments) == 0 {
		return nil
	}

	var repo string
	if tagPart != "" {
		repo = segments[len(segments)-1]
	} else if len(segments) >= 2 {
		tagPart = segments[len(segments)-1]
		repo = segments[len(segments)-2]
	} else {
		repo = segments[0]
	}

	repo = strings.ToLower(repo)
	var aliasMap map[string]string
	if len(aliases) > 0 {
		aliasMap = aliases[0]
	}

	if aliasMap != nil {
		if mapped, ok := aliasMap[repo]; ok && mapped != "" {
			repo = mapped
		}
	}

	if repo == "" || repo == "infra-charts" {
		return nil
	}

	var gitURL string
	if defaultOrg != "" {
		gitURL = fmt.Sprintf("git@github.com:%s/%s.git", defaultOrg, repo)
	} else {
		gitURL = fmt.Sprintf("git@github.com:%s.git", repo)
	}

	return &DiscoveredRepo{
		RepoName:    repo,
		BranchOrTag: tagPart,
		URL:         gitURL,
	}
}

// FindDiscoveredReposInDir scans a chart directory recursively to unlimited depth
// for YAML files containing deployment image definitions.
func FindDiscoveredReposInDir(dir string, defaultOrg string, aliases ...map[string]string) ([]DiscoveredRepo, error) {
	var results []DiscoveredRepo
	seen := make(map[string]bool)

	var aliasMap map[string]string
	if len(aliases) > 0 {
		aliasMap = aliases[0]
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		name := strings.ToLower(info.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		for _, r := range parseYAML(data, defaultOrg, aliasMap) {
			if !seen[r.RepoName] {
				seen[r.RepoName] = true
				r.SourceFile = path
				results = append(results, r)
			}
		}

		return nil
	})

	return results, err
}

func parseYAML(data []byte, defaultOrg string, aliasMap map[string]string) []DiscoveredRepo {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}

	var repos []DiscoveredRepo
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i].Value
				v := n.Content[i+1]
				if strings.EqualFold(k, "image") && v.Kind == yaml.MappingNode {
					var rawImage string
					for j := 0; j < len(v.Content); j += 2 {
						if strings.EqualFold(v.Content[j].Value, "image") && v.Content[j+1].Kind == yaml.ScalarNode {
							rawImage = v.Content[j+1].Value
							break
						}
					}
					if rawImage != "" {
						if r := ParseImageReference(rawImage, defaultOrg, aliasMap); r != nil {
							repos = append(repos, *r)
						}
					}
				}
				walk(v)
			}
			return
		}
		for _, child := range n.Content {
			walk(child)
		}
	}

	walk(&root)
	return repos
}
