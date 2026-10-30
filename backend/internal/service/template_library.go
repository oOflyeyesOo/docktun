package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type CommunityTemplate struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Documentation string `json:"documentation"`
}

type TemplateLibrary struct {
	client  *http.Client
	mu      sync.Mutex
	items   []CommunityTemplate
	updated time.Time
}

func NewTemplateLibrary() *TemplateLibrary {
	return &TemplateLibrary{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || (req.URL.Host != "raw.githubusercontent.com" && req.URL.Host != "api.github.com") {
			return fmt.Errorf("template redirect is not a public GitHub source")
		}
		return nil
	}}}
}

func publicComposeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("use a public HTTPS GitHub Compose file URL without credentials or query parameters")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Host == "github.com" && len(parts) >= 5 && parts[2] == "blob" {
		parts = append(parts[:2], parts[3:]...)
		u.Host = "raw.githubusercontent.com"
		u.Path = "/" + strings.Join(parts, "/")
	}
	if u.Host != "raw.githubusercontent.com" || len(parts) < 4 || strings.Contains(u.Path, "..") {
		return "", fmt.Errorf("use a raw.githubusercontent.com URL or a GitHub file page; repository and collection indexes are not Compose files")
	}
	if ext := strings.ToLower(path.Ext(u.Path)); ext != ".yml" && ext != ".yaml" {
		return "", fmt.Errorf("select a .yml or .yaml Compose file")
	}
	return u.String(), nil
}

func (s *TemplateLibrary) fetch(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DockTun-template-library")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot download public template: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d; check the public file URL or try again after its rate limit resets", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("template response exceeds 2 MiB")
	}
	return raw, err
}

func (s *TemplateLibrary) Import(ctx context.Context, address string) (string, string, error) {
	canonical, err := publicComposeURL(address)
	if err != nil {
		return "", "", err
	}
	raw, err := s.fetch(ctx, canonical)
	if err != nil {
		return "", "", err
	}
	var document struct {
		Services map[string]any `yaml:"services"`
	}
	if yaml.Unmarshal(raw, &document) != nil || len(document.Services) == 0 {
		return "", "", fmt.Errorf("this file is not a Compose document with services; catalog JSON requires a collection-specific importer")
	}
	return string(raw), canonical, nil
}

func (s *TemplateLibrary) Catalog(ctx context.Context) ([]CommunityTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) > 0 && time.Since(s.updated) < 10*time.Minute {
		return s.items, nil
	}
	raw, err := s.fetch(ctx, "https://api.github.com/repos/docker/awesome-compose/commits?per_page=1")
	if err != nil {
		return nil, err
	}
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Tree struct {
				SHA string `json:"sha"`
			}
		}
	}
	if json.Unmarshal(raw, &commits) != nil || len(commits) != 1 || len(commits[0].SHA) != 40 || len(commits[0].Commit.Tree.SHA) != 40 {
		return nil, fmt.Errorf("GitHub returned an invalid catalog revision")
	}
	revision := commits[0].SHA
	raw, err = s.fetch(ctx, "https://api.github.com/repos/docker/awesome-compose/git/trees/"+commits[0].Commit.Tree.SHA+"?recursive=1")
	if err != nil {
		return nil, err
	}
	var tree struct {
		SHA       string `json:"sha"`
		Truncated bool
		Tree      []struct{ Path, Type string }
	}
	if json.Unmarshal(raw, &tree) != nil || tree.SHA != commits[0].Commit.Tree.SHA || tree.Truncated {
		return nil, fmt.Errorf("GitHub returned an incomplete template catalog")
	}
	items := []CommunityTemplate{}
	for _, entry := range tree.Tree {
		parts := strings.Split(entry.Path, "/")
		if entry.Type != "blob" || len(parts) != 2 || strings.HasPrefix(parts[0], ".") {
			continue
		}
		if parts[1] != "compose.yaml" && parts[1] != "compose.yml" && parts[1] != "docker-compose.yml" && parts[1] != "docker-compose.yaml" {
			continue
		}
		items = append(items, CommunityTemplate{Name: strings.ToLower(strings.ReplaceAll(parts[0], "-", " ")), URL: "https://raw.githubusercontent.com/docker/awesome-compose/" + revision + "/" + entry.Path, Documentation: "https://github.com/docker/awesome-compose/tree/" + revision + "/" + parts[0]})
	}
	s.items, s.updated = items, time.Now()
	return items, nil
}
