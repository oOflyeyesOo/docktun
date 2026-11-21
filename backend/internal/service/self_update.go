package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultUpdateImage = "ghcr.io/ooflyeyesoo/docktun:latest"

type SelfUpdateState struct {
	Running      *RunningBuildInfo `json:"running,omitempty"`
	Phase        string            `json:"phase"`
	Message      string            `json:"message"`
	Image        string            `json:"image"`
	CurrentImage string            `json:"current_image,omitempty"`
	Revision     string            `json:"revision,omitempty"`
	Available    bool              `json:"available"`
	Checked      bool              `json:"checked"`
	LocalBuild   bool              `json:"local_build,omitempty"`
	Error        string            `json:"error,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Helper       string            `json:"helper,omitempty"`
}

var selfUpdateMu sync.Mutex

type SelfUpdater struct {
	operationStore *OperationStore
	operationID    string
	statePath      string
	buildMu        sync.Mutex
	build          RunningBuildInfo
	buildAt        time.Time
	inspectBuild   func(context.Context) (RunningBuildInfo, error)
	selfContainer  func(context.Context) (updateContainer, error)
	stageImage     func(context.Context, string) error
	verifyUpdate   func(context.Context, string) (string, error)
	inspectImage   func(context.Context, string) (string, error)
}

func NewSelfUpdater(dataDir string) *SelfUpdater {
	path, _ := filepath.Abs(filepath.Join(dataDir, "self-update.json"))
	return &SelfUpdater{statePath: path}
}

// SetCheckFailureForTest allows tests to simulate container or socket failures.
func (s *SelfUpdater) SetCheckFailureForTest(err error) {
	s.selfContainer = func(context.Context) (updateContainer, error) {
		return updateContainer{}, err
	}
}

// An explicitly configured immutable local image supports offline installations.
func stageUpdateImage(ctx context.Context, image string) error {
	if strings.HasPrefix(image, "sha256:") {
		_, err := updateDocker(ctx, "image", "inspect", image)
		return err
	}
	_, err := updateDocker(ctx, "pull", image)
	return err
}
func updateDocker(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Docker %s failed: %s", args[0], strings.TrimSpace(string(out)))
	}
	return out, nil
}

type DockerAPIError struct {
	Status  int
	Message string
}

func (e *DockerAPIError) Error() string {
	return fmt.Sprintf("Docker API: %s (%d)", e.Message, e.Status)
}
func dockerJSON(ctx context.Context, method, path string, body any, result any) error {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var msg struct{ Message string }
		_ = json.NewDecoder(res.Body).Decode(&msg)
		return &DockerAPIError{Status: res.StatusCode, Message: msg.Message}
	}
	if result != nil {
		return json.NewDecoder(res.Body).Decode(result)
	}
	return nil
}

type updateContainer struct {
	ID         string `json:"Id"`
	Name       string
	Image      string
	Config     map[string]any
	HostConfig map[string]any
	Mounts     []struct {
		Type, Source, Name, Destination, Mode, Propagation string
		RW                                                 bool
	}
	NetworkSettings struct{ Networks map[string]map[string]any }
	State           struct {
		Running    bool
		Paused     bool
		Restarting bool
		ExitCode   int
		Health     struct{ Status string }
	}
}

func inspectUpdateContainer(ctx context.Context, id string) (updateContainer, error) {
	var c updateContainer
	err := dockerJSON(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, &c)
	return c, err
}

func selfContainer(ctx context.Context) (updateContainer, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return updateContainer{}, err
	}
	id := os.Getenv("DOCKTUN_CONTAINER")
	if id == "" {
		id = hostname
	}
	c, err := inspectUpdateContainer(ctx, id)
	if err != nil {
		return c, fmt.Errorf("cannot identify this DockTun container; mount the Docker socket and set DOCKTUN_CONTAINER to its name if using a custom hostname: %w", err)
	}
	if c.Config["Hostname"] != hostname || !c.State.Running {
		return c, fmt.Errorf("DOCKTUN_CONTAINER does not identify this running DockTun instance")
	}
	return c, nil
}

func (s *SelfUpdater) save(state SelfUpdateState) error {
	state.Running = nil // Running identity is live metadata, never update history.
	state.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.statePath), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.statePath), ".self-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.statePath)
}

func validateUpdateContainer(c updateContainer) error {
	if len(c.ID) < 12 {
		return fmt.Errorf("invalid container identity")
	}
	if c.HostConfig["AutoRemove"] == true {
		return fmt.Errorf("self-update requires a persistent container without --rm")
	}
	if mode, _ := c.HostConfig["NetworkMode"].(string); strings.HasPrefix(mode, "container:") {
		return fmt.Errorf("self-update does not support sharing another container's network namespace")
	}
	for _, ep := range c.NetworkSettings.Networks {
		if ipam, ok := ep["IPAMConfig"].(map[string]any); ok && (ipam["IPv4Address"] != nil && ipam["IPv4Address"] != "" || ipam["IPv6Address"] != nil && ipam["IPv6Address"] != "") {
			return fmt.Errorf("self-update requires dynamic Docker addresses; remove the static container IP first")
		}
	}
	return nil
}

// Both the database and automatically persisted signing secret must survive a
// replacement. A mount for update progress alone is insufficient.
func validateCorePersistence(c updateContainer) error {
	database := coreEnvironment(c, "DB_PATH", "/app/data/app.db")
	if !filepath.IsAbs(database) {
		workdir, _ := c.Config["WorkingDir"].(string)
		if workdir == "" {
			workdir = "/"
		}
		database = filepath.Join(workdir, database)
	}
	directory := filepath.Dir(database)
	for _, mount := range c.Mounts {
		if mount.RW && (mount.Type == "bind" || mount.Type == "volume") && pathWithin(filepath.ToSlash(directory), filepath.ToSlash(mount.Destination)) {
			return nil
		}
	}
	return fmt.Errorf("database directory %s is not on a writable persistent mount; back it up and mount it before updating", directory)
}

func isLocalBuildContainer(c updateContainer) bool {
	if os.Getenv("DOCKTUN_LOCAL_BUILD") == "true" || os.Getenv("DOCKTUN_LOCAL_BUILD") == "1" {
		return true
	}
	cfgImage, _ := c.Config["Image"].(string)
	cfgImage = strings.TrimSpace(cfgImage)

	// An untagged or raw image SHA (e.g. 956f684565cb, sha256:..., or hex string)
	if cfgImage == "" || buildRevisionPattern.MatchString(cfgImage) || strings.HasPrefix(cfgImage, "sha256:") {
		return true
	}

	// Official releases are always prefixed with ghcr.io/ooflyeyesoo/docktun: or ghcr.io/ooflyeyesoo/docktun@
	if !strings.HasPrefix(cfgImage, "ghcr.io/ooflyeyesoo/docktun:") && !strings.HasPrefix(cfgImage, "ghcr.io/ooflyeyesoo/docktun@") {
		return true
	}

	imageID := strings.TrimSpace(c.Image)
	if imageID != "" && (buildRevisionPattern.MatchString(imageID) || strings.HasPrefix(imageID, "sha256:")) {
		if !strings.HasPrefix(cfgImage, "ghcr.io/ooflyeyesoo/docktun:") && !strings.HasPrefix(cfgImage, "ghcr.io/ooflyeyesoo/docktun@") {
			return true
		}
	}

	return false
}

func richDockerSocketError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "permission denied") {
		return fmt.Errorf("Docker socket permission denied: %s. Verify container user has group permissions to access /var/run/docker.sock", msg)
	}
	if strings.Contains(lower, "no such file or directory") {
		return fmt.Errorf("Docker socket not found: ensure /var/run/docker.sock is mounted into the container (%s)", msg)
	}
	if strings.Contains(lower, "cannot identify this docktun container") || strings.Contains(msg, "DOCKTUN_CONTAINER") {
		return fmt.Errorf("container identification failed: %s", msg)
	}
	return fmt.Errorf("Docker daemon communication error: %s", msg)
}

func richPullError(image string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return fmt.Errorf("registry pull timed out for image %s: check upstream network connectivity and latency (%s)", image, msg)
	}
	if strings.Contains(lower, "lookup") || strings.Contains(lower, "no such host") {
		return fmt.Errorf("registry DNS lookup failed for %s: check Docker daemon DNS settings (%s)", image, msg)
	}
	if strings.Contains(lower, "unauthorized") || strings.Contains(lower, "authentication required") || strings.Contains(lower, "denied") {
		return fmt.Errorf("registry authentication failed for %s: repository access denied (%s)", image, msg)
	}
	return fmt.Errorf("failed to pull image %s: %s", image, msg)
}

func richCosignError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "executable file not found") || strings.Contains(lower, "no such file or directory") || strings.Contains(lower, "binary is absent") {
		return fmt.Errorf("Cosign binary is absent: cannot verify image signature cryptographically (%s)", msg)
	}
	if strings.Contains(lower, "token.actions.githubusercontent.com") || strings.Contains(lower, "dial tcp") || strings.Contains(lower, "timeout") {
		return fmt.Errorf("Cosign verification could not reach certificate issuer (https://token.actions.githubusercontent.com): %s", msg)
	}
	if strings.Contains(lower, "no immutable registry digest") {
		return fmt.Errorf("official update has no immutable registry digest; untagged or custom image cannot be verified (%s)", msg)
	}
	return fmt.Errorf("Cosign signature verification failed: %s", msg)
}

func (s *SelfUpdater) Status() SelfUpdateState {
	state := SelfUpdateState{Phase: "idle", Image: s.Channel().Image, Message: "Check for a published DockTun update."}
	if b, err := os.ReadFile(s.statePath); err == nil {
		_ = json.Unmarshal(b, &state)
	}
	if updateInProgress(state.Phase) && time.Since(state.UpdatedAt) > 15*time.Minute {
		state.Phase = "failed"
		state.Message = "The previous update was interrupted. Check the container logs before retrying."
	}
	build := s.runningBuild()
	state.Running = &build
	if build.LocalBuild {
		state.LocalBuild = true
	}
	return state
}

func updateInProgress(phase string) bool {
	return phase == "pulling" || phase == "restarting" || phase == "checking"
}

func (s *SelfUpdater) Check(ctx context.Context) (SelfUpdateState, error) {
	if !selfUpdateMu.TryLock() {
		return s.Status(), fmt.Errorf("an update is already running")
	}
	defer selfUpdateMu.Unlock()
	maintenance := NewManagedServices(filepath.Dir(s.statePath)).read()
	if maintenance.Helper != "" {
		if helper, e := inspectUpdateContainer(ctx, maintenance.Helper); e == nil && helper.State.Running {
			return s.Status(), fmt.Errorf("DockTun service maintenance is already running")
		}
	}
	previous := s.Status()
	if updateInProgress(previous.Phase) {
		return previous, fmt.Errorf("an update is already running")
	}
	getContainer := s.selfContainer
	if getContainer == nil {
		getContainer = selfContainer
	}
	c, err := getContainer(ctx)
	if err != nil {
		diagErr := richDockerSocketError(err)
		state := previous
		state.Phase = "failed"
		state.Checked = true
		state.Error = diagErr.Error()
		state.Message = diagErr.Error()
		_ = s.save(state)
		return state, diagErr
	}

	// Support environments running from local source or custom build without explicit candidate overrides
	if !s.Channel().Override && isLocalBuildContainer(c) {
		state := SelfUpdateState{
			Phase:        "ready",
			LocalBuild:   true,
			Checked:      true,
			Available:    false,
			Image:        s.Channel().Image,
			CurrentImage: c.Image,
			Message:      "Running a local build or source checkout. Updates are managed via git pull & compose rebuild, or switch channel to official release.",
		}
		build := s.runningBuild()
		state.Running = &build
		if err = s.save(state); err != nil {
			return state, err
		}
		return state, nil
	}

	// Pulling only stages the image; the running container is unchanged until Install.
	state := SelfUpdateState{Phase: "checking", Image: s.Channel().Image, CurrentImage: c.Image, Message: "Downloading the latest published image for comparison."}
	if err = s.save(state); err != nil {
		return state, err
	}
	stageImg := s.stageImage
	if stageImg == nil {
		stageImg = stageUpdateImage
	}
	if err = stageImg(ctx, state.Image); err != nil {
		diagErr := richPullError(state.Image, err)
		state.Phase = "failed"
		state.Checked = true
		state.Error = diagErr.Error()
		state.Message = diagErr.Error()
		_ = s.save(state)
		return state, diagErr
	}
	verify := s.verifyUpdate
	if verify == nil {
		verify = verifyOfficialUpdate
	}
	verified, verifyErr := verify(ctx, state.Image)
	if verifyErr != nil {
		diagErr := richCosignError(verifyErr)
		state.Phase = "failed"
		state.Checked = true
		state.Error = diagErr.Error()
		state.Message = diagErr.Error()
		_ = s.save(state)
		return state, diagErr
	}
	state.Image = verified
	inspectImg := s.inspectImage
	var out string
	if inspectImg != nil {
		out, err = inspectImg(ctx, state.Image)
	} else {
		var rawOut []byte
		rawOut, err = updateDocker(ctx, "image", "inspect", state.Image, "--format", "{{.Id}}")
		out = string(rawOut)
	}
	if err != nil {
		state.Phase = "failed"
		state.Checked = true
		state.Error = fmt.Sprintf("failed to inspect staged image %s: %s", state.Image, err.Error())
		state.Message = state.Error
		_ = s.save(state)
		return state, err
	}
	state.Phase = "ready"
	state.Checked = true
	state.Available = strings.TrimSpace(out) != c.Image
	if isLocalBuildContainer(c) {
		state.LocalBuild = true
	}
	state.Message = "DockTun is up to date."
	if state.Available {
		state.Message = "An update is ready to install. The dashboard will briefly reconnect."
	}
	_ = s.save(state)
	return state, nil
}

func (s *SelfUpdater) Start(ctx context.Context) (SelfUpdateState, error) {
	if !selfUpdateMu.TryLock() {
		return s.Status(), fmt.Errorf("an update is already running")
	}
	defer selfUpdateMu.Unlock()
	maintenance := NewManagedServices(filepath.Dir(s.statePath)).read()
	if maintenance.Helper != "" {
		if helper, e := inspectUpdateContainer(ctx, maintenance.Helper); e == nil && helper.State.Running {
			return s.Status(), fmt.Errorf("DockTun service maintenance is already running")
		}
	}
	state := s.Status()
	if updateInProgress(state.Phase) {
		return state, fmt.Errorf("an update is already running")
	}
	c, err := selfContainer(ctx)
	if err != nil {
		return state, err
	}
	persistent := false
	for _, mount := range c.Mounts {
		rel, e := filepath.Rel(mount.Destination, s.statePath)
		if e == nil && mount.RW && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			persistent = true
		}
	}
	if !persistent {
		return state, fmt.Errorf("mount DockTun's data directory read-write before updating; the updater must preserve your settings and progress")
	}
	if err = validateCorePersistence(c); err != nil {
		return state, err
	}
	if err = validateUpdateContainer(c); err != nil {
		return state, err
	}
	shortCID := c.ID
	if len(shortCID) > 12 {
		shortCID = shortCID[:12]
	}
	helper := "docktun-updater-" + shortCID
	state = SelfUpdateState{Phase: "pulling", Image: s.Channel().Image, CurrentImage: c.Image, Helper: helper, Message: "Preparing the update. This page will reconnect automatically."}
	if err = s.save(state); err != nil {
		return state, err
	}
	args := []string{"run", "-d", "--rm", "--name", helper, "--label", "docktun.update.target=" + c.ID, "--network", "none", "--volumes-from", c.ID, "--entrypoint", "/app/server"}
	args = append(args, updateWorkerIdentity(c)...)
	args = append(args, c.Image, "update-worker", c.ID, state.Image, s.statePath)
	if s.operationID != "" {
		args = append(args, s.operationID)
	}
	if s.operationStore != nil {
		if err = s.operationStore.SetWorker(s.operationID, state.Helper); err != nil {
			return state, err
		}
	}
	_, err = updateDocker(ctx, args...)
	if err != nil {
		state.Phase = "failed"
		state.Message = err.Error()
		_ = s.save(state)
		return state, err
	}
	return state, nil
}

// Clone only the configurable network fields. Docker-assigned endpoint IDs and
// dynamic addresses must not be copied; static IPAM requests and aliases survive.
func updateCreateBody(c updateContainer, image string) map[string]any {
	return replacementCreateBody(c, image, true)
}
func replacementCreateBody(c updateContainer, image string, core bool) map[string]any {
	body := map[string]any{}
	for k, v := range c.Config {
		body[k] = v
	}
	body["Image"] = image
	if core {
		env := []any{}
		if original, ok := c.Config["Env"].([]any); ok {
			for _, v := range original {
				if !strings.HasPrefix(fmt.Sprint(v), "DOCKTUN_CONTAINER=") {
					env = append(env, v)
				}
			}
		}
		env = append(env, "DOCKTUN_CONTAINER="+strings.TrimPrefix(c.Name, "/"))
		body["Env"] = env
	}
	host := map[string]any{}
	for k, v := range c.HostConfig {
		host[k] = v
	}
	// Preserve actual named/anonymous volumes, including inherited mounts.
	delete(host, "VolumesFrom")
	existing := map[string]bool{}
	binds := []any{}
	if original, ok := host["Binds"].([]any); ok {
		for _, v := range original {
			binds = append(binds, v)
			parts := strings.Split(fmt.Sprint(v), ":")
			if len(parts) > 1 {
				existing[parts[1]] = true
			}
		}
	}
	if mounts, ok := host["Mounts"].([]any); ok {
		for _, m := range mounts {
			if v, ok := m.(map[string]any); ok {
				existing[fmt.Sprint(v["Target"])] = true
			}
		}
	}
	for _, m := range c.Mounts {
		if existing[m.Destination] || m.Type == "tmpfs" {
			continue
		}
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		}
		mode := m.Mode
		if mode == "" {
			mode = "rw"
			if !m.RW {
				mode = "ro"
			}
		}
		binds = append(binds, src+":"+m.Destination+":"+mode)
	}
	host["Binds"] = binds
	body["HostConfig"] = host
	endpoints := map[string]any{}
	for name, endpoint := range c.NetworkSettings.Networks {
		config := map[string]any{}
		for _, key := range []string{"IPAMConfig", "Links", "Aliases", "DriverOpts"} {
			if v, ok := endpoint[key]; ok {
				config[key] = v
			}
		}
		if aliases, ok := config["Aliases"].([]any); ok {
			filtered := []any{}
			for _, alias := range aliases {
				a, _ := alias.(string)
				shortCID := c.ID
				if len(shortCID) > 12 {
					shortCID = shortCID[:12]
				}
				if a != c.ID && a != shortCID {
					filtered = append(filtered, alias)
				}
			}
			config["Aliases"] = filtered
		}
		endpoints[name] = config
	}
	if len(endpoints) > 0 {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	}
	return body
}

// RunSelfUpdateWorker executes in an independent container, so replacing DockTun
// cannot terminate the update. The old container is retained until health passes.
func RunSelfUpdateWorker(target, image, statePath string) error {
	s := &SelfUpdater{statePath: statePath}
	state := s.Status()
	fail := func(err error) error {
		state.Phase = "failed"
		state.Message = err.Error()
		_ = s.save(state)
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	old, err := inspectUpdateContainer(ctx, target)
	if err != nil {
		return fail(err)
	}
	if err = validateUpdateContainer(old); err != nil {
		return fail(err)
	}
	if err = validateCorePersistence(old); err != nil {
		return fail(err)
	}
	time.Sleep(2 * time.Second)
	if err = stageUpdateImage(ctx, image); err != nil {
		return fail(err)
	}
	verified, verifyErr := verifyOfficialUpdate(ctx, image)
	if verifyErr != nil {
		return fail(verifyErr)
	}
	image = verified

	out, err := updateDocker(ctx, "image", "inspect", image, "--format", "{{.Id}}")
	if err != nil {
		return fail(err)
	}
	imageID := strings.TrimSpace(string(out))
	if imageID == old.Image {
		state.Phase = "complete"
		state.Message = "DockTun is already up to date."
		state.Available = false
		return s.save(state)
	}
	name := strings.TrimPrefix(old.Name, "/")
	shortOldID := old.ID
	if len(shortOldID) > 12 {
		shortOldID = shortOldID[:12]
	}
	backup := name + "-rollback-" + shortOldID
	body := updateCreateBody(old, imageID)
	state.Phase = "restarting"
	state.Message = "Installing the update and checking health. Reconnecting…"
	if err = s.save(state); err != nil {
		return err
	}
	if _, err = updateDocker(ctx, "stop", "--time", "30", old.ID); err != nil {
		return fail(err)
	}
	if _, err = updateDocker(ctx, "rename", old.ID, backup); err != nil {
		_, _ = updateDocker(ctx, "start", old.ID)
		return fail(err)
	}
	newID := ""
	rollback := func(cause error) error {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer rollbackCancel()
		if newID != "" {
			if _, e := updateDocker(rollbackCtx, "rm", "-f", newID); e != nil {
				return fail(fmt.Errorf("update failed (%v); could not remove replacement: %w; old container is %s", cause, e, backup))
			}
		}
		if _, e := updateDocker(rollbackCtx, "rename", old.ID, name); e != nil {
			return fail(fmt.Errorf("update failed (%v); restore container %s: %w", cause, backup, e))
		}
		if _, e := updateDocker(rollbackCtx, "start", old.ID); e != nil {
			return fail(fmt.Errorf("update failed (%v); could not restart previous container: %w", cause, e))
		}
		state.Phase = "rolled_back"
		state.Message = "The new image failed its health check; the previous container was restored. " + cause.Error()
		_ = s.save(state)
		return cause
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err = dockerJSON(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), body, &created); err != nil {
		return rollback(err)
	}
	newID = created.ID
	replacement, inspectErr := inspectUpdateContainer(ctx, newID)
	if inspectErr != nil {
		return rollback(inspectErr)
	}
	if err = verifyReplacementStorageNetworks(old, replacement); err != nil {
		return rollback(err)
	}
	if _, err = updateDocker(ctx, "start", newID); err != nil {
		return rollback(err)
	}
	port := "3001"
	if env, ok := old.Config["Env"].([]any); ok {
		for _, entry := range env {
			v, _ := entry.(string)
			if strings.HasPrefix(v, "LISTEN_ADDR=") {
				if _, p, e := net.SplitHostPort(strings.TrimPrefix(v, "LISTEN_ADDR=")); e == nil {
					port = p
				}
			}
		}
	}
	healthy := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
		_, probeErr := updateDocker(probeCtx, "exec", newID, "curl", "--max-time", "3", "-fsS", "http://127.0.0.1:"+port+"/healthz")
		probeCancel()
		if probeErr == nil {
			healthy = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !healthy {
		return rollback(fmt.Errorf("replacement did not become healthy within 90 seconds"))
	}
	// Never remove volumes: the replacement and rollback container share app data.
	if _, err = updateDocker(ctx, "rm", old.ID); err != nil {
		return fail(fmt.Errorf("update is healthy, but the old container %s could not be removed: %w", backup, err))
	}
	state.Phase = "complete"
	state.CurrentImage = imageID
	state.Available = false
	state.Checked = true
	state.Message = "DockTun updated successfully."
	return s.save(state)
}
