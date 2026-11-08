package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

type PlanCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type UpdatePlan struct {
	Container      string      `json:"container"`
	Name           string      `json:"name"`
	CurrentImage   string      `json:"current_image"`
	CandidateImage string      `json:"candidate_image"`
	Running        bool        `json:"running"`
	Ready          bool        `json:"ready"`
	Checks         []PlanCheck `json:"checks"`
	Networks       []string    `json:"networks"`
	Mounts         []string    `json:"mounts"`
	ReadinessURL   string      `json:"readiness_url,omitempty"`
	RollbackScope  string      `json:"rollback_scope"`
}

func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 8*time.Second)
}

type recoveryImageInfo struct {
	Config      struct{ Env []string }
	RepoTags    []string
	RepoDigests []string
}

func inspectUpdateImage(ctx context.Context, id string) (recoveryImageInfo, error) {
	var result recoveryImageInfo
	err := dockerJSON(ctx, "GET", "/images/"+url.PathEscape(id)+"/json", nil, &result)
	return result, err
}
func databaseMajor(image string, env []string) (string, string) {
	family := strings.Split(path.Base(strings.Split(image, "@")[0]), ":")[0]
	if family != "postgres" && family != "mysql" && family != "mariadb" {
		for _, e := range env {
			switch {
			case strings.HasPrefix(e, "PG_MAJOR="):
				family = "postgres"
			case strings.HasPrefix(e, "MARIADB_VERSION=") || strings.HasPrefix(e, "MARIADB_MAJOR="):
				family = "mariadb"
			case strings.HasPrefix(e, "MYSQL_MAJOR=") && family != "mariadb":
				family = "mysql"
			}
		}
	}
	// MySQL/MariaDB release series (8.0 vs 8.4, 10.6 vs 10.11) are migration
	// boundaries even when their first version component is unchanged.
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if (family == "mysql" && key == "MYSQL_MAJOR") || (family == "mariadb" && key == "MARIADB_MAJOR") {
			return family, value
		}
	}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if (family == "postgres" && key == "PG_MAJOR") || ((family == "mysql" || family == "mariadb") && (key == "MYSQL_VERSION" || key == "MARIADB_VERSION" || key == "MYSQL_MAJOR" || key == "MARIADB_MAJOR")) {
			value = strings.TrimPrefix(value, "v")
			if _, version, ok := strings.Cut(value, ":"); ok {
				value = version
			}
			parts := strings.Split(value, ".")
			if family != "postgres" && len(parts) > 1 {
				return family, strings.Join(parts[:2], ".")
			}
			return family, parts[0]
		}
	}
	return "", ""
}
func validateDatabaseImageTransition(ctx context.Context, old updateContainer, candidate string) error {
	image, _ := old.Config["Image"].(string)
	familyName := strings.Split(path.Base(strings.Split(image, "@")[0]), ":")[0]
	if familyName != "postgres" && familyName != "mysql" && familyName != "mariadb" {
		return nil
	}
	family, major, err := databaseContainerVersion(ctx, old)
	if err != nil {
		return err
	}
	var after struct{ Config struct{ Env []string } }
	if err := dockerJSON(ctx, "GET", "/images/"+url.PathEscape(candidate)+"/json", nil, &after); err != nil {
		return err
	}
	_, next := databaseMajor(image, after.Config.Env)
	if next == "" || next != major {
		return fmt.Errorf("%s major version %s → %s requires a separate consistent backup and migration; image rollback alone cannot restore database data", family, major, next)
	}
	return nil
}
func (s *DockerContainers) UpdatePlan(ctx context.Context, id string, stage bool) (UpdatePlan, error) {
	old, err := inspectUpdateContainer(ctx, id)
	if err != nil {
		return UpdatePlan{}, err
	}
	name := strings.TrimPrefix(old.Name, "/")
	image, _ := old.Config["Image"].(string)
	plan := UpdatePlan{Container: old.ID, Name: name, CurrentImage: old.Image, Running: old.State.Running, Ready: true, Checks: []PlanCheck{}, Networks: []string{}, Mounts: []string{}, RollbackScope: "Previous image/container only. Database schema and external data writes are not rolled back."}
	add := func(id, label, status, message string) {
		plan.Checks = append(plan.Checks, PlanCheck{id, label, status, message})
		if status == "failed" {
			plan.Ready = false
		}
	}
	if err = validateUpdateContainer(old); err != nil {
		add("definition", "Container definition", "failed", err.Error())
	} else {
		add("definition", "Container definition", "passed", "Original Docker settings will be retained")
	}
	if old.State.Paused || old.State.Restarting {
		add("state", "Container state", "failed", "Resume the container or wait for its restart to finish before updating")
	}
	for network := range old.NetworkSettings.Networks {
		plan.Networks = append(plan.Networks, network)
	}
	for _, mount := range old.Mounts {
		plan.Mounts = append(plan.Mounts, mount.Type+": "+mount.Destination)
	}
	if stage {
		if err = stageUpdateImage(ctx, image); err != nil {
			add("image", "Candidate image", "failed", "Image could not be staged; original container is unchanged")
			return plan, nil
		}
	}
	var candidate struct {
		ID string `json:"Id"`
	}
	if err = dockerJSON(ctx, "GET", "/images/"+url.PathEscape(image)+"/json", nil, &candidate); err != nil {
		status := "warning"
		if stage {
			status = "failed"
		}
		add("image", "Candidate image", status, "No verified staged image metadata; stage and inspect a candidate before updating")
	} else {
		plan.CandidateImage = candidate.ID
		if e := validateDatabaseImageTransition(ctx, old, candidate.ID); e != nil {
			add("migration", "Database migration", "failed", e.Error())
		} else {
			add("migration", "Database migration", "passed", "No detected database major-version change; application schema migration still needs review")
		}
	}
	if old.State.Health.Status != "" {
		add("health", "Docker healthcheck", "passed", "Replacement must report healthy before the original is removed")
	} else {
		add("health", "Application readiness", "warning", "No Docker healthcheck: stable process state does not verify database/login readiness")
	}
	var readiness string
	if s.db != nil {
		_ = s.db.QueryRow(`SELECT url FROM readiness_checks WHERE container_name=?`, name).Scan(&readiness)
	}
	plan.ReadinessURL = readiness
	if readiness != "" && !old.State.Running {
		add("readiness", "HTTP readiness from DockTun", "warning", "Stopped app will remain stopped; readiness is checked during running replacements")
	}
	if readiness != "" && old.State.Running {
		if e := probeReadiness(ctx, readiness); e != nil {
			add("readiness", "HTTP readiness from DockTun", "warning", "Current application readiness is unavailable; the replacement must pass this endpoint before the original is removed")
		} else {
			add("readiness", "HTTP readiness from DockTun", "passed", "Configured readiness endpoint returned success")
		}
	}
	var env []string
	if values, ok := old.Config["Env"].([]any); ok {
		for _, value := range values {
			if v, ok := value.(string); ok {
				env = append(env, v)
			}
		}
	}
	for _, host := range databaseHosts(env) {
		add("dependency:"+host, "Configured dependency", "warning", host+": environment-derived hint; network/alias preservation is checked, database authentication requires an application readiness check")
	}
	return plan, nil
}
func probeReadiness(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("use an HTTP(S) readiness URL without embedded credentials")
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probe, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return fmt.Errorf("readiness endpoint unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
	}
	return nil
}
func (s *DockerContainers) SaveReadiness(id, raw string) error {
	ctx, cancel := operationContext()
	defer cancel()
	container, err := inspectUpdateContainer(ctx, id)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(container.Name, "/")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		_, err = s.db.Exec(`DELETE FROM readiness_checks WHERE container_name=?`, name)
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || len(raw) > 2048 {
		return fmt.Errorf("use an HTTP(S) URL without credentials or query secrets")
	}
	_, err = s.db.Exec(`INSERT INTO readiness_checks(container_name,url) VALUES(?,?) ON CONFLICT(container_name) DO UPDATE SET url=excluded.url`, name, raw)
	return err
}
func (s *DockerContainers) ImportPreview(ctx context.Context, id string) (map[string]any, error) {
	detail, err := s.Details(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"container_id": detail.ID, "name": detail.Name, "project_id": detail.ProjectID, "project_path": detail.ProjectPath, "compose_file": detail.ComposeFile, "mounts": detail.Mounts, "networks": detail.Networks, "changes": []string{}, "read_only": true, "note": "Preview only. Import and file-write actions require an explicit request; unavailable host paths are not created."}, nil
}
func planJSON(p UpdatePlan) string { raw, _ := json.Marshal(p); return string(raw) }

func databaseContainerVersion(ctx context.Context, container updateContainer) (string, string, error) {
	image, _ := container.Config["Image"].(string)
	metadata, err := inspectUpdateImage(ctx, container.Image)
	if err != nil {
		if !recoveryVolumeMissing(err) {
			return "", "", fmt.Errorf("cannot inspect installed database image: %w", err)
		}
		if values, ok := container.Config["Env"].([]any); ok {
			for _, entry := range values {
				if value, ok := entry.(string); ok {
					metadata.Config.Env = append(metadata.Config.Env, value)
				}
			}
		}
	}
	family, major := databaseMajor(image, metadata.Config.Env)
	if family == "" || major == "" {
		return "", "", fmt.Errorf("installed database version cannot be verified; inspect it before maintenance")
	}
	if container.State.Running {
		binary := "postgres"
		if family == "mysql" {
			binary = "mysqld"
		}
		if family == "mariadb" {
			binary = "mariadbd"
		}
		raw, e := updateDocker(ctx, "exec", container.ID, binary, "--version")
		if e != nil {
			return "", "", fmt.Errorf("cannot verify the installed database binary version")
		}
		version := regexp.MustCompile(`\b([0-9]+\.[0-9]+(?:\.[0-9]+)?)`).FindString(string(raw))
		parts := strings.Split(version, ".")
		actual := parts[0]
		if family != "postgres" && len(parts) > 1 {
			actual = strings.Join(parts[:2], ".")
		}
		if actual == "" || actual != major {
			return "", "", fmt.Errorf("database metadata and installed binary version disagree; inspect migration requirements")
		}
	}
	return family, major, nil
}
