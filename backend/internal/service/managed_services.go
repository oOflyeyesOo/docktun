package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const serviceImageLabel = "io.docktun.update-image"

type ManagedService struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	App              string `json:"app,omitempty"`
	Image            string `json:"image"`
	ImageID          string `json:"image_id"`
	Running          bool   `json:"running"`
	Health           string `json:"health,omitempty"`
	Version          string `json:"version,omitempty"`
	Available        bool   `json:"available"`
	Checked          bool   `json:"checked"`
	Result           string `json:"result,omitempty"`
	Message          string `json:"message,omitempty"`
	Candidate        string `json:"candidate,omitempty"`
	CandidateVersion string `json:"candidate_version,omitempty"`
}

type ServiceUpdateJob struct {
	Phase     string           `json:"phase"`
	Action    string           `json:"action,omitempty"`
	Message   string           `json:"message"`
	Helper    string           `json:"helper,omitempty"`
	UpdatedAt time.Time        `json:"updated_at"`
	Services  []ManagedService `json:"services"`
}

type ManagedServicesState struct {
	Services []ManagedService `json:"services"`
	Job      ServiceUpdateJob `json:"job"`
}

type ManagedServices struct{ statePath string }

var managedUpdateMu sync.Mutex

func NewManagedServices(dataDir string) *ManagedServices {
	path, _ := filepath.Abs(filepath.Join(dataDir, "service-updates.json"))
	return &ManagedServices{statePath: path}
}

func containerLabels(c updateContainer) map[string]string {
	out := map[string]string{}
	if labels, ok := c.Config["Labels"].(map[string]any); ok {
		for k, v := range labels {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

func coreEnvironment(c updateContainer, key, fallback string) string {
	if env, ok := c.Config["Env"].([]any); ok {
		for _, item := range env {
			if value, ok := strings.CutPrefix(fmt.Sprint(item), key+"="); ok && value != "" {
				return value
			}
		}
	}
	return fallback
}

// Ownership is deliberately independent of display-name prefixes. A user app
// called tailscale, wireguard, or docktun-backup must remain a regular app.
func managedServiceKind(c, core updateContainer) string {
	if len(c.ID) >= 12 && strings.HasSuffix(c.Name, "-rollback-"+c.ID[:12]) {
		return ""
	}
	if c.ID == core.ID {
		return "core"
	}
	labels, own := containerLabels(c), containerLabels(core)
	name := strings.TrimPrefix(c.Name, "/")
	if app := labels[tailscaleOwnerLabel]; app != "" && name == tailscaleNodeName(app) {
		return "tailscale-app"
	}
	for _, kind := range []string{"cloudflare", "tailscale", "wireguard"} {
		expected := "docktun-" + kind
		if kind == "cloudflare" {
			expected = coreEnvironment(core, "CLOUDFLARED_CONTAINER", "docktun-cloudflared")
		}
		if labels["docktun.provider"] == kind && name == expected {
			return kind
		}
	}
	sameProject := own["com.docker.compose.project"] != "" && labels["com.docker.compose.project"] == own["com.docker.compose.project"] && own["com.docker.compose.project.working_dir"] != "" && labels["com.docker.compose.project.working_dir"] == own["com.docker.compose.project.working_dir"]
	if sameProject {
		switch labels["com.docker.compose.service"] {
		case "cloudflared":
			return "cloudflare"
		case "tailscale", "wireguard":
			return labels["com.docker.compose.service"]
		case "zeroconf":
			return "zeroconf"
		}
	}
	image, _ := c.Config["Image"].(string)
	// Older installers created WireGuard without provider labels. Require the
	// exact companion name, image family and shared persistent state; a name or
	// Compose project alone is not evidence that DockTun owns a user's VPN.
	if name == "docktun-wireguard" && (image == "lscr.io/linuxserver/wireguard" || strings.HasPrefix(image, "lscr.io/linuxserver/wireguard:") || strings.HasPrefix(image, "lscr.io/linuxserver/wireguard@")) {
		directory := filepath.Join(filepath.Dir(coreEnvironment(core, "DB_PATH", "/app/data/app.db")), "wireguard")
		expected, err := providerMount(core, directory, "/config", false)
		if err == nil {
			for _, mount := range c.Mounts {
				if mount.Destination != "/config" || !mount.RW {
					continue
				}
				source := mount.Source
				if mount.Type == "volume" {
					source = mount.Name
				}
				if expected == fmt.Sprintf("type=%s,source=%s,target=/config", mount.Type, source) {
					return "wireguard"
				}
			}
		}
	}
	entry, _ := c.Config["Entrypoint"].([]any)
	if name == "docktun-zeroconf" && c.HostConfig["NetworkMode"] == "host" && len(entry) == 1 && (entry[0] == "/app/zeroconf" || entry[0] == "/zeroconf") && (labels["io.docktun.service"] == "zeroconf" || strings.HasPrefix(image, "ghcr.io/ooflyeyesoo/docktun:") || strings.HasPrefix(image, "docktun-zeroconf:")) {
		return "zeroconf"
	}
	return ""
}

func serviceImage(c, core updateContainer, kind string) string {
	if kind == "core" || kind == "zeroconf" {
		return coreEnvironment(core, "DOCKTUN_UPDATE_IMAGE", defaultUpdateImage)
	}
	if image := containerLabels(c)[serviceImageLabel]; image != "" {
		return image
	}
	image, _ := c.Config["Image"].(string)
	return image
}

func managedInventory(ctx context.Context, core updateContainer) ([]ManagedService, error) {
	var summaries []struct {
		ID     string `json:"Id"`
		Names  []string
		Labels map[string]string
	}
	if err := dockerJSON(ctx, "GET", "/containers/json?all=1", nil, &summaries); err != nil {
		return nil, err
	}
	services := []ManagedService{}
	metadata := map[string]string{}
	for _, summary := range summaries {
		labels := summary.Labels
		ownLabels := containerLabels(core)
		candidate := summary.ID == core.ID || labels[tailscaleOwnerLabel] != "" || labels["docktun.provider"] != ""
		if labels["com.docker.compose.project"] != "" && labels["com.docker.compose.project"] == ownLabels["com.docker.compose.project"] {
			candidate = true
		}
		for _, name := range summary.Names {
			if clean := strings.TrimPrefix(name, "/"); clean == "docktun-zeroconf" || clean == "docktun-wireguard" {
				candidate = true
			}
		}
		if !candidate {
			continue
		}
		c, err := inspectUpdateContainer(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		kind := managedServiceKind(c, core)
		if kind == "" {
			continue
		}
		version, known := metadata[c.Image]
		if !known {
			var img struct {
				Config struct{ Labels map[string]string }
			}
			if dockerJSON(ctx, "GET", "/images/"+url.PathEscape(c.Image)+"/json", nil, &img) == nil {
				version = managedImageVersion(img.Config.Labels)
			}
			metadata[c.Image] = version
		}
		services = append(services, ManagedService{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Kind: kind, App: containerLabels(c)[tailscaleOwnerLabel], Image: serviceImage(c, core, kind), ImageID: c.Image, Version: version, Running: c.State.Running, Health: c.State.Health.Status})
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Kind == "core" && services[j].Kind != "core" {
			return true
		}
		if services[j].Kind == "core" {
			return false
		}
		return services[i].Name < services[j].Name
	})
	return services, nil
}

func (s *ManagedServices) read() ServiceUpdateJob {
	job := ServiceUpdateJob{Phase: "idle", Message: "Check installed services for updates.", Services: []ManagedService{}}
	if raw, err := os.ReadFile(s.statePath); err == nil {
		_ = json.Unmarshal(raw, &job)
	}
	return job
}

func (s *ManagedServices) save(job *ServiceUpdateJob) error {
	job.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.statePath), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.statePath), ".services-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.statePath)
}

func serviceJobActive(phase string) bool { return phase == "checking" || phase == "updating" }

func (s *ManagedServices) Status(ctx context.Context) (ManagedServicesState, error) {
	own, err := selfContainer(ctx)
	if err != nil {
		return ManagedServicesState{}, err
	}
	rows, err := managedInventory(ctx, own)
	if err != nil {
		return ManagedServicesState{}, err
	}
	job := s.read()
	if serviceJobActive(job.Phase) && time.Since(job.UpdatedAt) > 10*time.Second {
		helper, e := inspectUpdateContainer(ctx, job.Helper)
		if e == nil && !helper.State.Running || e != nil && strings.Contains(e.Error(), "404") {
			job.Phase = "failed"
			job.Message = "The update worker stopped. Review each service result and check again before retrying."
		}
	}
	for i := range rows {
		for _, previous := range job.Services {
			if previous.Name == rows[i].Name {
				rows[i].Checked = previous.Checked
				rows[i].Candidate = previous.Candidate
				rows[i].CandidateVersion = previous.CandidateVersion
				rows[i].Available = previous.Checked && previous.Candidate != "" && previous.Candidate != rows[i].ImageID
				rows[i].Result, rows[i].Message = previous.Result, previous.Message
			}
		}
	}
	return ManagedServicesState{Services: rows, Job: job}, nil
}

func (s *ManagedServices) Start(ctx context.Context, action string, ids []string) (ServiceUpdateJob, error) {
	managedUpdateMu.Lock()
	defer managedUpdateMu.Unlock()
	if action != "check" && action != "update" {
		return ServiceUpdateJob{}, fmt.Errorf("invalid service action")
	}
	if !selfUpdateMu.TryLock() {
		return ServiceUpdateJob{}, fmt.Errorf("a DockTun update is already being prepared")
	}
	defer selfUpdateMu.Unlock()
	old := s.read()
	if action == "update" && len(ids) == 0 {
		return old, fmt.Errorf("select at least one installed service to update")
	}
	if old.Helper != "" {
		if helper, e := inspectUpdateContainer(ctx, old.Helper); e == nil && helper.State.Running {
			return old, fmt.Errorf("the previous service operation is still finishing")
		}
	}
	if serviceJobActive(old.Phase) {
		helper, helperErr := inspectUpdateContainer(ctx, old.Helper)
		if helperErr == nil && !helper.State.Running || helperErr != nil && strings.Contains(helperErr.Error(), "404") {
			old.Phase = "failed"
			old.Message = "The previous worker stopped; starting a new operation."
			_ = s.save(&old)
		}
	}
	if serviceJobActive(old.Phase) {
		return old, fmt.Errorf("a service operation is already running")
	}
	legacy := NewSelfUpdater(filepath.Dir(s.statePath)).Status()
	if legacy.Helper != "" {
		if helper, e := inspectUpdateContainer(ctx, legacy.Helper); e == nil && helper.State.Running {
			return old, fmt.Errorf("the core updater is already running")
		}
	}
	if updateInProgress(legacy.Phase) {
		return old, fmt.Errorf("the core updater is already running")
	}
	own, err := selfContainer(ctx)
	if err != nil {
		return old, err
	}
	sharedState := false
	for _, mount := range own.Mounts {
		if mount.RW && (mount.Type == "volume" || mount.Type == "bind") && pathWithin(filepath.ToSlash(filepath.Dir(s.statePath)), filepath.ToSlash(mount.Destination)) {
			sharedState = true
		}
	}
	if !sharedState {
		return old, fmt.Errorf("mount DockTun's writable data directory before running service updates; the worker must share update state across restarts")
	}
	rows, err := managedInventory(ctx, own)
	if err != nil {
		return old, err
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []ManagedService{}
	for _, row := range rows {
		if len(ids) == 0 || wanted[row.ID] {
			selected = append(selected, row)
			delete(wanted, row.ID)
		}
	}
	if len(wanted) > 0 || len(selected) == 0 {
		return old, fmt.Errorf("select installed DockTun services; unrelated containers cannot be updated here")
	}
	job := ServiceUpdateJob{Phase: "checking", Action: action, Message: "Checking images before changing any services.", Services: selected, Helper: fmt.Sprintf("docktun-services-updater-%s-%x", own.ID[:12], time.Now().UnixNano())}
	if err = s.save(&job); err != nil {
		return job, err
	}
	// The worker lives independently of the dashboard and updates core last.
	args := []string{"run", "-d", "--rm", "--name", job.Helper, "--label", "docktun.update.target=" + own.ID, "--network", "none", "--volumes-from", own.ID, "--entrypoint", "/app/server"}
	args = append(args, updateWorkerIdentity(own)...)
	args = append(args, own.Image, "services-worker", own.ID, s.statePath)
	_, err = updateDocker(ctx, args...)
	if err != nil {
		// A disconnected HTTP caller must not overwrite a running/completed
		// worker's history or allow a second overlapping operation.
		probe, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if helper, e := inspectUpdateContainer(probe, job.Helper); e == nil && helper.State.Running {
			return s.read(), nil
		}
		latest := s.read()
		if latest.Helper == job.Helper && (latest.Phase == "complete" || latest.Phase == "failed") {
			return latest, nil
		}
		job.Phase = "failed"
		job.Message = err.Error()
		_ = s.save(&job)
	}
	return job, err
}

func RunManagedServiceWorker(target, path string) error {
	s := &ManagedServices{statePath: path}
	job := s.read()
	fail := func(err error) error { job.Phase = "failed"; job.Message = err.Error(); _ = s.save(&job); return err }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	core, err := inspectUpdateContainer(ctx, target)
	if err != nil {
		return fail(err)
	}
	rows, err := managedInventory(ctx, core)
	if err != nil {
		return fail(err)
	}
	live := map[string]ManagedService{}
	for _, row := range rows {
		live[row.ID] = row
	}
	images := map[string]string{}
	versions := map[string]string{}
	for i := range job.Services {
		row := &job.Services[i]
		actual, ok := live[row.ID]
		if !ok || actual.Name != row.Name || actual.Kind != row.Kind {
			return fail(fmt.Errorf("service ownership changed; check again"))
		}
		*row = actual
		if job.Action == "update" {
			container, inspectErr := inspectUpdateContainer(ctx, row.ID)
			if inspectErr != nil {
				return fail(inspectErr)
			}
			if inspectErr = validateUpdateContainer(container); inspectErr != nil {
				row.Result = "failed"
				row.Message = inspectErr.Error()
				return fail(fmt.Errorf("cannot recreate %s safely: %w", row.Name, inspectErr))
			}
			if container.State.Paused {
				return fail(fmt.Errorf("resume %s before updating it", row.Name))
			}
		}
		row.Result = "checking"
		job.Message = "Checking " + row.Name
		_ = s.save(&job)
		candidate, ok := images[row.Image]
		if !ok {
			if err = stageUpdateImage(ctx, row.Image); err != nil {
				row.Result = "failed"
				row.Message = err.Error()
				return fail(fmt.Errorf("check %s: %w", row.Name, err))
			}
			var image struct {
				ID     string `json:"Id"`
				Config struct{ Labels map[string]string }
			}
			if err = dockerJSON(ctx, "GET", "/images/"+url.PathEscape(row.Image)+"/json", nil, &image); err != nil {
				return fail(err)
			}
			candidate = image.ID
			images[row.Image] = candidate
			versions[row.Image] = managedImageVersion(image.Config.Labels)
		}
		row.Candidate = candidate
		row.CandidateVersion = versions[row.Image]
		row.Checked = true
		row.Available = candidate != row.ImageID
		row.Result = "current"
		row.Message = "Already current"
		if row.Available {
			row.Result = "available"
			row.Message = "Update available"
		}
		_ = s.save(&job)
	}
	if job.Action == "check" {
		job.Phase = "complete"
		job.Message = "Image checks complete. Select the services to update."
		return s.save(&job)
	}
	job.Phase = "updating"
	_ = s.save(&job)
	// Stable ordering: independently managed companions first, dashboard last.
	sort.SliceStable(job.Services, func(i, j int) bool { return job.Services[i].Kind != "core" && job.Services[j].Kind == "core" })
	for i := range job.Services {
		row := &job.Services[i]
		if !row.Available {
			continue
		}
		job.Message = "Updating " + row.Name
		row.Result = "updating"
		_ = s.save(&job)
		if row.Kind == "core" {
			err = RunSelfUpdateWorker(row.ID, row.Candidate, filepath.Join(filepath.Dir(path), "self-update.json"))
			if err != nil {
				raw, readErr := os.ReadFile(filepath.Join(filepath.Dir(path), "self-update.json"))
				var state SelfUpdateState
				if readErr == nil && json.Unmarshal(raw, &state) == nil && state.Phase == "rolled_back" {
					err = fmt.Errorf("previous DockTun container restored: %w", err)
				}
			}
		} else {
			err = replaceManagedService(ctx, *row, core)
		}
		if err != nil {
			row.Result = "failed"
			row.Message = err.Error()
			return fail(fmt.Errorf("%s failed; remaining services were left unchanged. %w", row.Name, err))
		}
		row.Result = "updated"
		row.Message = "Updated; previous running state preserved"
		row.Available = false
		_ = s.save(&job)
	}
	job.Phase = "complete"
	job.Message = "Selected services updated. Disabled services remain stopped."
	return s.save(&job)
}

func replaceManagedService(ctx context.Context, row ManagedService, core updateContainer) error {
	old, err := inspectUpdateContainer(ctx, row.ID)
	if err != nil {
		return err
	}
	if managedServiceKind(old, core) != row.Kind || strings.TrimPrefix(old.Name, "/") != row.Name {
		return fmt.Errorf("service identity changed; refusing replacement")
	}
	if err = validateUpdateContainer(old); err != nil {
		return err
	}
	if old.State.Paused {
		return fmt.Errorf("resume the paused service before updating it")
	}
	body := updateCreateBody(old, row.Candidate)
	body["Env"] = old.Config["Env"]
	labels := containerLabels(old)
	labels[serviceImageLabel] = row.Image
	if row.Kind == "wireguard" || row.Kind == "tailscale" || row.Kind == "cloudflare" {
		labels["docktun.provider"] = row.Kind
	}
	body["Labels"] = labels
	if row.Kind == "zeroconf" {
		labels["io.docktun.service"] = "zeroconf"
		body["Entrypoint"] = []string{"/app/zeroconf"}
		body["Cmd"] = []string{}
	}
	backup := row.Name + "-rollback-" + old.ID[:12]
	if old.State.Running {
		if _, err = updateDocker(ctx, "stop", "--time", "30", old.ID); err != nil {
			return err
		}
	}
	if _, err = updateDocker(ctx, "rename", old.ID, backup); err != nil {
		if old.State.Running {
			_, _ = updateDocker(ctx, "start", old.ID)
		}
		return err
	}
	newID := ""
	rollback := func(cause error) error {
		restore, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if newID != "" {
			if _, err = updateDocker(restore, "rm", "-f", newID); err != nil {
				return fmt.Errorf("replacement failed (%v); rollback cleanup failed: %w", cause, err)
			}
		}
		if _, err = updateDocker(restore, "rename", old.ID, row.Name); err != nil {
			return fmt.Errorf("replacement failed (%v); restore %s manually: %w", cause, backup, err)
		}
		if old.State.Running {
			if _, err = updateDocker(restore, "start", old.ID); err != nil {
				return fmt.Errorf("replacement failed (%v); previous service could not restart: %w", cause, err)
			}
		}
		return fmt.Errorf("previous container restored: %w", cause)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err = dockerJSON(ctx, "POST", "/containers/create?name="+url.QueryEscape(row.Name), body, &created); err != nil {
		return rollback(err)
	}
	newID = created.ID
	if old.State.Running {
		if _, err = updateDocker(ctx, "start", newID); err != nil {
			return rollback(err)
		}
		deadline := time.Now().Add(90 * time.Second)
		for {
			time.Sleep(time.Second)
			current, e := inspectUpdateContainer(ctx, newID)
			if e != nil {
				return rollback(e)
			}
			if !current.State.Running || current.State.Restarting {
				return rollback(fmt.Errorf("replacement exited or restarted"))
			}
			health := current.State.Health.Status
			if health == "healthy" || health == "" && time.Until(deadline) < 87*time.Second {
				if row.Kind == "zeroconf" {
					port := coreEnvironment(old, "HTTP_PORT", "80")
					number, e := strconv.Atoi(port)
					if e != nil || number < 1 || number > 65535 {
						return rollback(fmt.Errorf("invalid local proxy HTTP port"))
					}
					domain := strings.Trim(coreEnvironment(old, "DOMAIN", "local"), ".")
					if _, e = updateDocker(ctx, "exec", newID, "curl", "--max-time", "5", "-fsS", "-H", "Host: docktun."+domain, "http://127.0.0.1:"+port+"/healthz"); e != nil {
						return rollback(fmt.Errorf("local proxy could not reach DockTun: %w", e))
					}
				}
				break
			}
			if health == "unhealthy" || time.Now().After(deadline) {
				return rollback(fmt.Errorf("replacement health check failed"))
			}
		}
	}
	if _, err = updateDocker(ctx, "rm", old.ID); err != nil {
		return fmt.Errorf("replacement installed but old backup %s could not be removed: %w", backup, err)
	}
	return nil
}

func managedImageVersion(labels map[string]string) string {
	if value := strings.TrimSpace(labels["org.opencontainers.image.version"]); value != "" {
		return value[:min(80, len(value))]
	}
	if revision := labels["org.opencontainers.image.revision"]; buildRevisionPattern.MatchString(revision) {
		return revision[:min(12, len(revision))]
	}
	return ""
}
