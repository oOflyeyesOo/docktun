package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ContainerGitInfo struct {
	RepoURL     string
	LocalCommit string
	Branch      string
	LocalDir    string
	GitUsername string
	GitToken    string
	Source      string
}

// parseGitHubRepo extracts owner and repo from various GitHub URL formats:
// - https://github.com/owner/repo
// - https://github.com/owner/repo.git
// - git@github.com:owner/repo.git
// - github.com/owner/repo
func parseGitHubRepo(rawURL string) (owner, repo string, ok bool) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return "", "", false
	}
	u = strings.TrimSuffix(u, ".git")
	u = strings.TrimRight(u, "/")
	idx := strings.Index(u, "github.com")
	if idx == -1 {
		return "", "", false
	}
	sub := u[idx+len("github.com"):]
	sub = strings.TrimPrefix(sub, "/")
	sub = strings.TrimPrefix(sub, ":")
	parts := strings.Split(sub, "/")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

// isLocalOrCustomImage checks if an image identifier is likely a local build,
// an untagged SHA, or a local Compose service rather than a standard registry image.
func isLocalOrCustomImage(image string) bool {
	img := strings.TrimSpace(image)
	if img == "" {
		return true
	}
	if isHexDigest(img) {
		return true
	}
	if !strings.Contains(img, "/") {
		return true
	}
	parts := strings.Split(img, "/")
	if len(parts) == 2 && !strings.Contains(parts[0], ".") && !strings.Contains(parts[0], ":") {
		return false
	}
	if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
		return false
	}
	return false
}

func isHexDigest(s string) bool {
	clean := strings.TrimPrefix(s, "sha256:")
	if len(clean) < 7 {
		return false
	}
	for _, c := range clean {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func fetchLatestGitHubCommit(ctx context.Context, owner, repo, branch string) (string, string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?per_page=1", owner, repo)
	if branch != "" && branch != "HEAD" {
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", owner, repo, branch)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "DockTun-Dashboard/1.0")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound && branch != "" && branch != "HEAD" {
		// Fallback to default branch
		return fetchLatestGitHubCommit(ctx, owner, repo, "")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	// Try commit array first (from ?per_page=1)
	var arr []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(bodyBytes, &arr); err == nil && len(arr) > 0 {
		shortSHA := arr[0].SHA
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		firstLine := strings.Split(arr[0].Commit.Message, "\n")[0]
		if len(firstLine) > 50 {
			firstLine = firstLine[:47] + "..."
		}
		return shortSHA, firstLine, nil
	}

	// Try single commit object (from /commits/:ref)
	var single struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(bodyBytes, &single); err == nil && single.SHA != "" {
		shortSHA := single.SHA
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		firstLine := strings.Split(single.Commit.Message, "\n")[0]
		if len(firstLine) > 50 {
			firstLine = firstLine[:47] + "..."
		}
		return shortSHA, firstLine, nil
	}

	return "", "", fmt.Errorf("unable to parse github commit response")
}

func (s *DockerContainers) getContainerGitInfo(detail ContainerDetail) ContainerGitInfo {
	info := ContainerGitInfo{}

	// 1. Check if tracked project has repo_url configured in DB
	if detail.ProjectID > 0 && s.db != nil {
		var repoURL, gitUser, gitTok, projPath string
		err := s.db.QueryRow(`SELECT repo_url, git_username, git_token, path FROM projects WHERE id = ?`, detail.ProjectID).Scan(&repoURL, &gitUser, &gitTok, &projPath)
		if err == nil {
			info.RepoURL = strings.TrimSpace(repoURL)
			info.GitUsername = strings.TrimSpace(gitUser)
			info.GitToken = strings.TrimSpace(gitTok)
			if info.RepoURL != "" {
				info.Source = "project_config"
			}
			if projPath != "" && detail.ProjectPath == "" {
				detail.ProjectPath = projPath
			}
		}
	}

	// 2. Search local directories for .git
	var candidates []string
	if detail.ComposeWorkingDir != "" {
		candidates = append(candidates, detail.ComposeWorkingDir)
	}
	if detail.ProjectPath != "" && detail.ProjectPath != detail.ComposeWorkingDir {
		candidates = append(candidates, detail.ProjectPath)
	}
	for _, m := range detail.Mounts {
		if m.Source != "" {
			candidates = append(candidates, m.Source)
		}
	}
	if detail.ComposeProject != "" {
		candidates = append(candidates,
			filepath.Join("/opt/projects", detail.ComposeProject),
			filepath.Join("./projects", detail.ComposeProject),
		)
	}

	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		gitDir := filepath.Join(dir, ".git")
		if stat, err := os.Stat(gitDir); err == nil && (stat.IsDir() || !stat.IsDir()) {
			info.LocalDir = dir
			if info.LocalCommit == "" {
				cmd := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD")
				if out, err := cmd.Output(); err == nil {
					info.LocalCommit = strings.TrimSpace(string(out))
				}
			}
			if info.RepoURL == "" {
				cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
				if out, err := cmd.Output(); err == nil {
					info.RepoURL = strings.TrimSpace(string(out))
					info.Source = "git_dir"
				}
			}
			if info.Branch == "" {
				cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
				if out, err := cmd.Output(); err == nil {
					b := strings.TrimSpace(string(out))
					if b != "HEAD" {
						info.Branch = b
					}
				}
			}
			break
		}
	}

	// 3. Check container labels for git metadata
	if detail.Labels != nil {
		if info.RepoURL == "" {
			if src := detail.Labels["org.opencontainers.image.source"]; src != "" {
				info.RepoURL = strings.TrimSpace(src)
				info.Source = "labels"
			} else if src := detail.Labels["vcs-url"]; src != "" {
				info.RepoURL = strings.TrimSpace(src)
				info.Source = "labels"
			}
		}
		if info.LocalCommit == "" {
			if rev := detail.Labels["org.opencontainers.image.revision"]; rev != "" {
				info.LocalCommit = strings.TrimSpace(rev)
				if len(info.LocalCommit) > 7 {
					info.LocalCommit = info.LocalCommit[:7]
				}
			} else if rev := detail.Labels["git_commit"]; rev != "" {
				info.LocalCommit = strings.TrimSpace(rev)
				if len(info.LocalCommit) > 7 {
					info.LocalCommit = info.LocalCommit[:7]
				}
			}
		}
	}

	return info
}

func (s *DockerContainers) checkGitUpstream(ctx context.Context, info ContainerGitInfo, detail ContainerDetail) (UpdateCheckResult, bool) {
	cname := strings.TrimPrefix(detail.Name, "/")
	res := UpdateCheckResult{
		ContainerID:   detail.ID,
		ContainerName: cname,
		Image:         detail.Image,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	remoteURL := info.RepoURL
	if remoteURL == "" && info.LocalDir != "" {
		cmd := exec.CommandContext(ctx, "git", "-C", info.LocalDir, "remote", "get-url", "origin")
		if out, err := cmd.Output(); err == nil {
			remoteURL = strings.TrimSpace(string(out))
		}
	}
	if remoteURL == "" {
		return res, false
	}

	var remoteSHA, commitMsg string
	var checkErr error

	// Strategy A: If it's GitHub, use GitHub Commits API
	if owner, repo, ok := parseGitHubRepo(remoteURL); ok {
		remoteSHA, commitMsg, checkErr = fetchLatestGitHubCommit(ctx, owner, repo, info.Branch)
	}

	// Strategy B: Universal Git remote check via git ls-remote
	if checkErr != nil || remoteSHA == "" {
		targetURL := remoteURL
		if info.GitToken != "" {
			if u, err := url.Parse(remoteURL); err == nil {
				user := info.GitUsername
				if user == "" {
					user = "oauth2"
				}
				u.User = url.UserPassword(user, info.GitToken)
				targetURL = u.String()
			}
		}

		var cmd *exec.Cmd
		if info.LocalDir != "" {
			cmd = exec.CommandContext(ctx, "git", "-C", info.LocalDir, "ls-remote", "origin", "HEAD")
		} else {
			cmd = exec.CommandContext(ctx, "git", "ls-remote", targetURL, "HEAD")
		}

		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) >= 1 && len(fields[0]) >= 7 {
					remoteSHA = fields[0][:7]
					commitMsg = "Upstream commit"
					checkErr = nil
					break
				}
			}
		}
	}

	if remoteSHA == "" {
		return res, false
	}

	res.LatestDigest = remoteSHA
	localCommit := info.LocalCommit
	if localCommit == "" && s.cfg != nil {
		localCommit = getLocalGitCommit(s.cfg.SelfPath, detail)
	}

	if localCommit != "" {
		res.CurrentDigest = localCommit
		if strings.HasPrefix(remoteSHA, localCommit) || strings.HasPrefix(localCommit, remoteSHA) {
			res.UpdateAvailable = false
			res.Error = ""
		} else {
			if commitMsg != "" {
				res.Error = fmt.Sprintf("Upstream git commit %s available for a source build (%s)", remoteSHA, commitMsg)
			} else {
				res.Error = fmt.Sprintf("Upstream git commit %s available for a source build", remoteSHA)
			}
		}
	} else {
		res.CurrentDigest = "local-build"
		res.UpdateAvailable = false
		if commitMsg != "" {
			res.Error = fmt.Sprintf("Upstream git commit %s available (local commit untracked)", remoteSHA)
		} else {
			res.Error = "Local build commit untracked"
		}
	}

	return res, true
}
