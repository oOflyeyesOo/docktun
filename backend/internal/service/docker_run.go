package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"docktun/internal/db"
)

type DockerRunJob struct {
	ID          int      `json:"id"`
	Command     string   `json:"command"`
	Mode        string   `json:"mode"`
	Lines       []string `json:"lines"`
	Output      []string `json:"output"` // alias for lines for frontend compatibility
	Done        bool     `json:"done"`
	Status      string   `json:"status"` // "running", "completed", "failed"
	ExitCode    int      `json:"exit_code"`
	Error       string   `json:"error,omitempty"`
	Container   string   `json:"container,omitempty"`
	ProjectID   int      `json:"project_id,omitempty"`
	ProjectName string   `json:"project_name,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

type ParsedDockerRun struct {
	ContainerName string
	Image         string
	Ports         []string
	Volumes       []string
	Env           []string
	Restart       string
	Network       string
	Command       []string
	Detach        bool
	Remove        bool
	Privileged    bool
	ShmSize       string
	User          string
	Hostname      string
	CapAdd        []string
	Devices       []string
	ExtraArgs     []string
}

type DockerRunService struct {
	db          *db.DB
	projectsSvc *Projects
	mu          sync.RWMutex
	jobs        map[int]*DockerRunJob
	nextID      int
	runDocker   func(context.Context, ...string) ([]byte, error)
}

func defaultDockerNetwork() string {
	if net := strings.TrimSpace(os.Getenv("DOCKER_NETWORK")); net != "" {
		return net
	}
	if err := exec.Command("docker", "network", "inspect", "docktun").Run(); err == nil {
		return "docktun"
	}
	if err := exec.Command("docker", "network", "inspect", "cloudflared").Run(); err == nil {
		return "cloudflared"
	}
	return "docktun"
}

func ensureNetwork(netName string) error {
	if netName == "" || netName == "host" || netName == "bridge" || netName == "none" {
		return nil
	}
	cmd := exec.Command("docker", "network", "ls", "--filter", "name=^"+netName+"$", "--format", "{{.ID}}")
	if out, err := cmd.CombinedOutput(); err == nil {
		ids := strings.Fields(strings.TrimSpace(string(out)))
		if len(ids) > 0 {
			return nil
		}
	}
	return exec.Command("docker", "network", "create", netName).Run()
}

func NewDockerRun(database *db.DB, projectsSvc *Projects) *DockerRunService {
	return &DockerRunService{
		db:          database,
		projectsSvc: projectsSvc,
		jobs:        make(map[int]*DockerRunJob),
		nextID:      1,
	}
}

func (s *DockerRunService) GetJob(id int) (*DockerRunJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	// Return a copy with cloned lines
	copyJob := *j
	copyJob.Lines = make([]string, len(j.Lines))
	copy(copyJob.Lines, j.Lines)
	copyJob.Output = copyJob.Lines
	return &copyJob, true
}

func (s *DockerRunService) appendLine(id int, line string) {
	s.mu.Lock()
	if j, ok := s.jobs[id]; ok {
		j.Lines = append(j.Lines, line)
		j.Output = j.Lines
	}
	s.mu.Unlock()
}

func (s *DockerRunService) markDone(id int, exitCode int, errMsg string) {
	s.mu.Lock()
	if j, ok := s.jobs[id]; ok {
		j.Done = true
		j.ExitCode = exitCode
		j.Error = errMsg
		if exitCode == 0 {
			j.Status = "completed"
		} else {
			j.Status = "failed"
		}
	}
	s.mu.Unlock()
}

// Launching an app must never silently remove an existing container. Updates
// have their own explicit action with rollback and preserved configuration.
func (s *DockerRunService) checkContainerNameAvailable(name string) error {
	if name == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run := s.runDocker
	if run == nil {
		run = providerDocker
	}
	_, err := run(ctx, "container", "inspect", name)
	if err == nil {
		return fmt.Errorf("container %q already exists; choose a different name or use its existing update/redeploy action", name)
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such container") || strings.Contains(message, "no such object") {
		return nil
	}
	return fmt.Errorf("could not check container name %q: %w", name, err)
}

func (s *DockerRunService) StartRun(rawCmd string, mode string, connectCloudflared bool, createProject bool, customProjectName string, onDone func(string)) (*DockerRunJob, error) {
	trimmed := strings.TrimSpace(rawCmd)
	if trimmed == "" {
		return nil, fmt.Errorf("command cannot be empty")
	}

	parsed, err := ParseDockerRunCommand(trimmed)
	if err != nil {
		return nil, fmt.Errorf("failed to parse command: %w", err)
	}

	if customProjectName != "" {
		parsed.ContainerName = customProjectName
	}

	if err := s.checkContainerNameAvailable(parsed.ContainerName); err != nil {
		return nil, err
	}

	if connectCloudflared && parsed.Network == "" {
		parsed.Network = defaultDockerNetwork()
	}

	s.mu.Lock()
	jobID := s.nextID
	s.nextID++
	job := &DockerRunJob{
		ID:          jobID,
		Command:     trimmed,
		Mode:        mode,
		Lines:       []string{},
		Output:      []string{},
		Done:        false,
		Status:      "running",
		Container:   parsed.ContainerName,
		ProjectName: parsed.ContainerName,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	s.jobs[jobID] = job
	s.mu.Unlock()

	go func() {
		if mode == "compose" || createProject {
			s.executeAsCompose(jobID, parsed, trimmed, onDone)
		} else {
			s.executeDirect(jobID, parsed, trimmed, connectCloudflared, onDone)
		}
	}()

	return job, nil
}

func (s *DockerRunService) executeAsCompose(jobID int, parsed *ParsedDockerRun, rawCmd string, onDone func(string)) {
	// Ensure network exists if specified
	if parsed.Network != "" && parsed.Network != "host" && parsed.Network != "bridge" && parsed.Network != "none" {
		if err := ensureNetwork(parsed.Network); err != nil {
			s.appendLine(jobID, fmt.Sprintf("[WARN] Could not ensure network '%s': %v", parsed.Network, err))
		} else {
			s.appendLine(jobID, fmt.Sprintf("[STEP] Docker network '%s' ready.", parsed.Network))
		}
	}

	// Fetch image in advance with streaming feedback
	if parsed.Image != "" {
		s.appendLine(jobID, fmt.Sprintf("[STEP] Fetching container image '%s'...", parsed.Image))
		pcmd := exec.Command("docker", "pull", parsed.Image)
		pstdout, perr := pcmd.StdoutPipe()
		if perr == nil {
			pcmd.Stderr = pcmd.Stdout
			if err := pcmd.Start(); err == nil {
				pbuf := make([]byte, 2048)
				for {
					pn, rerr := pstdout.Read(pbuf)
					if pn > 0 {
						for _, pl := range strings.Split(strings.TrimRight(string(pbuf[:pn]), "\r\n"), "\n") {
							if tr := strings.TrimSpace(pl); tr != "" {
								s.appendLine(jobID, tr)
							}
						}
					}
					if rerr != nil {
						break
					}
				}
				if perrWait := pcmd.Wait(); perrWait != nil {
					s.appendLine(jobID, fmt.Sprintf("[WARN] docker pull: %v (will proceed with compose up)", perrWait))
				} else {
					s.appendLine(jobID, fmt.Sprintf("[STEP] Successfully fetched '%s'", parsed.Image))
				}
			}
		}
	}

	s.appendLine(jobID, fmt.Sprintf("[STEP] Preparing Compose stack for '%s'...", parsed.ContainerName))

	baseDir := "./projects"
	if fi, err := os.Stat("/opt/projects"); err == nil && fi.IsDir() {
		baseDir = "/opt/projects"
	}
	projDir := filepath.Join(baseDir, parsed.ContainerName)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		s.appendLine(jobID, "[ERROR] Failed to create project directory: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
	}

	// Pre-create host directories for bind mounts and pre-create named volumes
	for _, vol := range parsed.Volumes {
		src := extractVolumeSource(vol)
		if isNamedVolume(src) {
			_ = exec.Command("docker", "volume", "create", src).Run()
		} else if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
			fullHost := filepath.Clean(filepath.Join(projDir, src))
			_ = os.MkdirAll(fullHost, 0755)
		} else if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "\\") {
			_ = os.MkdirAll(src, 0755)
		}
	}

	composeContent := parsed.GenerateComposeYAML()
	composePath := filepath.Join(projDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		s.appendLine(jobID, "[ERROR] Failed to write docker-compose.yml: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
	}
	s.appendLine(jobID, "[STEP] Created "+composePath)

	// Register in projects database if not already exists
	res, err := s.db.Exec(`
		INSERT INTO projects (name, path)
		SELECT ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM projects WHERE path = ?);
	`, parsed.ContainerName, projDir, projDir)

	var projectID int
	if err == nil {
		if lid, _ := res.LastInsertId(); lid > 0 {
			projectID = int(lid)
		}
	}
	if projectID == 0 {
		s.db.QueryRow("SELECT id FROM projects WHERE path = ?", projDir).Scan(&projectID)
	}

	s.mu.Lock()
	if j, ok := s.jobs[jobID]; ok {
		j.ProjectID = projectID
	}
	s.mu.Unlock()

	s.appendLine(jobID, fmt.Sprintf("[STEP] Registered project '%s' (ID: %d)", parsed.ContainerName, projectID))
	s.appendLine(jobID, "[STEP] Executing: docker compose up -d")

	cmd := exec.Command("docker", "compose", "-p", parsed.ContainerName, "--project-directory", projDir, "up", "-d")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.appendLine(jobID, "[ERROR] Pipe error: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		s.appendLine(jobID, "[ERROR] Failed to start compose: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			lines := strings.Split(strings.TrimRight(string(buf[:n]), "\r\n"), "\n")
			for _, l := range lines {
				trimmed := strings.TrimSpace(l)
				if trimmed != "" {
					s.appendLine(jobID, trimmed)
				}
			}
		}
		if err != nil {
			break
		}
	}

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		exitCode = 1
		s.appendLine(jobID, "[ERROR] Compose completed with error: "+err.Error())
	} else {
		s.appendLine(jobID, "[SUCCESS] Container started and running under DockTun management!")
	}
	s.markDone(jobID, exitCode, "")

	if onDone != nil {
		if j, ok := s.GetJob(jobID); ok {
			onDone(strings.Join(j.Lines, "\n"))
		}
	}
}

func (s *DockerRunService) executeDirect(jobID int, parsed *ParsedDockerRun, rawCmd string, connectCloudflared bool, onDone func(string)) {
	tokens := splitShellArgs(rawCmd)
	var cmdArgs []string

	// Strip leading "docker" or "podman"
	idx := 0
	if idx < len(tokens) && (tokens[idx] == "docker" || tokens[idx] == "podman") {
		idx++
	}
	cmdArgs = append(cmdArgs, tokens[idx:]...)

	// If the user entered "docker pull ...", "pull ...", or just an image name, construct a "docker run" command
	isRunCmd := false
	if len(cmdArgs) > 0 && cmdArgs[0] == "run" {
		isRunCmd = true
	}

	if !isRunCmd {
		newArgs := []string{"run", "-d"}
		if parsed.Restart != "" {
			newArgs = append(newArgs, "--restart", parsed.Restart)
		}
		if parsed.Privileged {
			newArgs = append(newArgs, "--privileged")
		}
		if parsed.ContainerName != "" {
			newArgs = append(newArgs, "--name", parsed.ContainerName)
		}
		if parsed.Hostname != "" {
			newArgs = append(newArgs, "--hostname", parsed.Hostname)
		}
		if parsed.User != "" {
			newArgs = append(newArgs, "--user", parsed.User)
		}
		if parsed.ShmSize != "" {
			newArgs = append(newArgs, "--shm-size", parsed.ShmSize)
		}
		for _, cap := range parsed.CapAdd {
			newArgs = append(newArgs, "--cap-add", cap)
		}
		for _, dev := range parsed.Devices {
			newArgs = append(newArgs, "--device", dev)
		}
		for _, p := range parsed.Ports {
			newArgs = append(newArgs, "-p", p)
		}
		for _, v := range parsed.Volumes {
			newArgs = append(newArgs, "-v", v)
		}
		for _, e := range parsed.Env {
			newArgs = append(newArgs, "-e", e)
		}
		if parsed.Network != "" {
			newArgs = append(newArgs, "--network", parsed.Network)
		}
		newArgs = append(newArgs, parsed.ExtraArgs...)
		newArgs = append(newArgs, parsed.Image)
		newArgs = append(newArgs, parsed.Command...)
		cmdArgs = newArgs
	}

	// If cloudflared connect requested and --network wasn't supplied, add --network
	hasNetwork := false
	netName := ""
	for i, a := range cmdArgs {
		if strings.HasPrefix(a, "--network=") {
			hasNetwork = true
			netName = strings.TrimPrefix(a, "--network=")
			break
		} else if strings.HasPrefix(a, "--net=") {
			hasNetwork = true
			netName = strings.TrimPrefix(a, "--net=")
			break
		} else if (a == "--network" || a == "--net") && i+1 < len(cmdArgs) {
			hasNetwork = true
			netName = cmdArgs[i+1]
			break
		}
	}

	if !hasNetwork && connectCloudflared {
		netName = defaultDockerNetwork()
		// Insert before image name
		inserted := false
		for i, a := range cmdArgs {
			if a == "run" {
				cmdArgs = append(cmdArgs[:i+1], append([]string{"--network", netName}, cmdArgs[i+1:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			cmdArgs = append([]string{"--network", netName}, cmdArgs...)
		}
		hasNetwork = true
	}

	if hasNetwork && netName != "" && netName != "host" && netName != "bridge" && netName != "none" {
		if err := ensureNetwork(netName); err != nil {
			s.appendLine(jobID, fmt.Sprintf("[WARN] Could not ensure network '%s': %v", netName, err))
		}
	}

	// Pre-create host directories for bind mounts and pre-create named volumes
	for _, vol := range parsed.Volumes {
		src := extractVolumeSource(vol)
		if isNamedVolume(src) {
			_ = exec.Command("docker", "volume", "create", src).Run()
		} else if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "\\") {
			_ = os.MkdirAll(src, 0755)
		} else if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
			_ = os.MkdirAll(src, 0755)
		}
	}

	s.appendLine(jobID, fmt.Sprintf("[STEP] Executing: docker %s", strings.Join(cmdArgs, " ")))

	cmd := exec.Command("docker", cmdArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.appendLine(jobID, "[ERROR] Pipe error: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		s.appendLine(jobID, "[ERROR] Failed to start docker run: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			lines := strings.Split(strings.TrimRight(string(buf[:n]), "\r\n"), "\n")
			for _, l := range lines {
				trimmed := strings.TrimSpace(l)
				if trimmed != "" {
					s.appendLine(jobID, trimmed)
				}
			}
		}
		if err != nil {
			break
		}
	}

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		exitCode = 1
		s.appendLine(jobID, "[ERROR] docker run exited with error: "+err.Error())
	} else {
		s.appendLine(jobID, "[SUCCESS] Container started successfully!")
	}
	s.markDone(jobID, exitCode, "")

	if onDone != nil {
		if j, ok := s.GetJob(jobID); ok {
			onDone(strings.Join(j.Lines, "\n"))
		}
	}
}

func IsDockerRunCommand(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "services:") || strings.HasPrefix(trimmed, "version:") {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "docker run") || strings.HasPrefix(lower, "docker create") ||
		strings.HasPrefix(lower, "podman run") || strings.HasPrefix(lower, "podman create") ||
		strings.HasPrefix(lower, "docker pull") || strings.HasPrefix(lower, "run -") {
		return true
	}
	lines := strings.Split(trimmed, "\n")
	firstLine := strings.TrimSpace(lines[0])
	if (strings.Contains(firstLine, "--name") || strings.Contains(firstLine, "-p ") || strings.Contains(firstLine, "-v ") || strings.Contains(firstLine, "--restart")) && !strings.Contains(firstLine, ":") {
		return true
	}
	return false
}

func ParseDockerRunCommand(raw string) (*ParsedDockerRun, error) {
	tokens := splitShellArgs(raw)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	idx := 0
	if idx < len(tokens) && (tokens[idx] == "docker" || tokens[idx] == "podman") {
		idx++
	}
	if idx < len(tokens) && (tokens[idx] == "run" || tokens[idx] == "pull" || tokens[idx] == "create") {
		idx++
	}

	res := &ParsedDockerRun{
		Restart: "unless-stopped",
		Network: "",
	}

	imageFound := false

	// Flags that take an argument as the next token
	takesArg := map[string]bool{
		"-p": true, "--publish": true,
		"-v": true, "--volume": true,
		"-e": true, "--env": true,
		"--env-file": true,
		"--name":     true,
		"--restart":  true,
		"--network":  true, "--net": true,
		"-u": true, "--user": true,
		"-w": true, "--workdir": true,
		"-h": true, "--hostname": true,
		"--platform": true,
		"--pull":     true,
		"-l":         true, "--label": true,
		"--cpus": true,
		"-m":     true, "--memory": true,
		"--shm-size":   true,
		"--entrypoint": true,
		"--dns":        true,
		"--ip":         true,
		"--log-driver": true,
		"--log-opt":    true,
	}

	for i := idx; i < len(tokens); i++ {
		t := tokens[i]

		// Once the image has been found, all subsequent tokens are the container's command/args
		if imageFound {
			res.Command = append(res.Command, t)
			continue
		}

		if strings.HasPrefix(t, "--name=") {
			res.ContainerName = strings.Trim(strings.TrimPrefix(t, "--name="), "\"'")
		} else if t == "--name" && i+1 < len(tokens) {
			i++
			res.ContainerName = strings.Trim(tokens[i], "\"'")
		} else if strings.HasPrefix(t, "-p=") || strings.HasPrefix(t, "--publish=") {
			val := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(t, "--publish="), "-p="), "\"'")
			res.Ports = append(res.Ports, val)
		} else if (t == "-p" || t == "--publish") && i+1 < len(tokens) {
			i++
			res.Ports = append(res.Ports, strings.Trim(tokens[i], "\"'"))
		} else if strings.HasPrefix(t, "-v=") || strings.HasPrefix(t, "--volume=") {
			val := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(t, "--volume="), "-v="), "\"'")
			res.Volumes = append(res.Volumes, val)
		} else if (t == "-v" || t == "--volume") && i+1 < len(tokens) {
			i++
			res.Volumes = append(res.Volumes, strings.Trim(tokens[i], "\"'"))
		} else if strings.HasPrefix(t, "-e=") || strings.HasPrefix(t, "--env=") {
			val := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(t, "--env="), "-e="), "\"'")
			res.Env = append(res.Env, val)
		} else if (t == "-e" || t == "--env") && i+1 < len(tokens) {
			i++
			res.Env = append(res.Env, strings.Trim(tokens[i], "\"'"))
		} else if strings.HasPrefix(t, "--restart=") {
			res.Restart = strings.Trim(strings.TrimPrefix(t, "--restart="), "\"'")
		} else if t == "--restart" && i+1 < len(tokens) {
			i++
			res.Restart = strings.Trim(tokens[i], "\"'")
		} else if strings.HasPrefix(t, "--network=") || strings.HasPrefix(t, "--net=") {
			val := strings.TrimPrefix(strings.TrimPrefix(t, "--network="), "--net=")
			res.Network = strings.Trim(val, "\"'")
		} else if (t == "--network" || t == "--net") && i+1 < len(tokens) {
			i++
			res.Network = strings.Trim(tokens[i], "\"'")
		} else if t == "-d" || t == "--detach" || t == "-dit" || t == "-itd" {
			res.Detach = true
		} else if t == "--rm" {
			res.Remove = true
		} else if t == "--privileged" {
			res.Privileged = true
		} else if strings.HasPrefix(t, "--privileged=") {
			res.Privileged = strings.Trim(strings.TrimPrefix(t, "--privileged="), "\"'") == "true"
		} else if strings.HasPrefix(t, "--shm-size=") {
			res.ShmSize = strings.Trim(strings.TrimPrefix(t, "--shm-size="), "\"'")
		} else if t == "--shm-size" && i+1 < len(tokens) {
			i++
			res.ShmSize = strings.Trim(tokens[i], "\"'")
		} else if strings.HasPrefix(t, "--hostname=") || strings.HasPrefix(t, "-h=") {
			res.Hostname = strings.Trim(strings.TrimPrefix(strings.TrimPrefix(t, "--hostname="), "-h="), "\"'")
		} else if (t == "--hostname" || t == "-h") && i+1 < len(tokens) {
			i++
			res.Hostname = strings.Trim(tokens[i], "\"'")
		} else if strings.HasPrefix(t, "--user=") || strings.HasPrefix(t, "-u=") {
			res.User = strings.Trim(strings.TrimPrefix(strings.TrimPrefix(t, "--user="), "-u="), "\"'")
		} else if (t == "--user" || t == "-u") && i+1 < len(tokens) {
			i++
			res.User = strings.Trim(tokens[i], "\"'")
		} else if strings.HasPrefix(t, "--cap-add=") {
			res.CapAdd = append(res.CapAdd, strings.Trim(strings.TrimPrefix(t, "--cap-add="), "\"'"))
		} else if t == "--cap-add" && i+1 < len(tokens) {
			i++
			res.CapAdd = append(res.CapAdd, strings.Trim(tokens[i], "\"'"))
		} else if strings.HasPrefix(t, "--device=") {
			res.Devices = append(res.Devices, strings.Trim(strings.TrimPrefix(t, "--device="), "\"'"))
		} else if t == "--device" && i+1 < len(tokens) {
			i++
			res.Devices = append(res.Devices, strings.Trim(tokens[i], "\"'"))
		} else if takesArg[t] && i+1 < len(tokens) {
			i++
			res.ExtraArgs = append(res.ExtraArgs, t, tokens[i])
		} else if strings.HasPrefix(t, "-") {
			res.ExtraArgs = append(res.ExtraArgs, t)
		} else {
			res.Image = t
			imageFound = true
		}
	}

	if res.Image == "" {
		return nil, fmt.Errorf("no container image specified in command")
	}

	if res.ContainerName == "" {
		base := res.Image
		if lastSlash := strings.LastIndex(base, "/"); lastSlash != -1 {
			base = base[lastSlash+1:]
		}
		if colon := strings.Index(base, ":"); colon != -1 {
			base = base[:colon]
		}
		base = strings.TrimSpace(base)
		if base == "" {
			base = "app"
		}
		res.ContainerName = base
	}

	return res, nil
}

func extractVolumeSource(vol string) string {
	parts := strings.Split(vol, ":")
	if len(parts) == 0 {
		return ""
	}
	// On Windows, drive letter like C:/foo:bar has parts ["C", "/foo", "bar"]
	if len(parts) >= 2 && len(parts[0]) == 1 && unicode.IsLetter(rune(parts[0][0])) {
		return parts[0] + ":" + parts[1]
	}
	return parts[0]
}

func isNamedVolume(src string) bool {
	src = strings.TrimSpace(src)
	if src == "" {
		return false
	}
	if strings.HasPrefix(src, "/") || strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || strings.HasPrefix(src, "~") || strings.HasPrefix(src, `\`) {
		return false
	}
	if len(src) >= 2 && src[1] == ':' {
		return false
	}
	return true
}

func (p *ParsedDockerRun) GenerateComposeYAML() string {
	var sb strings.Builder
	sb.WriteString("services:\n")
	sb.WriteString(fmt.Sprintf("  %s:\n", p.ContainerName))
	sb.WriteString(fmt.Sprintf("    image: %s\n", p.Image))
	sb.WriteString(fmt.Sprintf("    container_name: %s\n", p.ContainerName))
	if p.Restart != "" {
		sb.WriteString(fmt.Sprintf("    restart: %s\n", p.Restart))
	}
	if p.Privileged {
		sb.WriteString("    privileged: true\n")
	}
	if p.Hostname != "" {
		sb.WriteString(fmt.Sprintf("    hostname: %s\n", p.Hostname))
	}
	if p.User != "" {
		sb.WriteString(fmt.Sprintf("    user: %s\n", p.User))
	}
	if p.ShmSize != "" {
		sb.WriteString(fmt.Sprintf("    shm_size: %s\n", p.ShmSize))
	}
	if len(p.CapAdd) > 0 {
		sb.WriteString("    cap_add:\n")
		for _, cap := range p.CapAdd {
			sb.WriteString(fmt.Sprintf("      - %s\n", cap))
		}
	}
	if len(p.Devices) > 0 {
		sb.WriteString("    devices:\n")
		for _, dev := range p.Devices {
			sb.WriteString(fmt.Sprintf("      - \"%s\"\n", dev))
		}
	}
	if len(p.Ports) > 0 {
		sb.WriteString("    ports:\n")
		for _, port := range p.Ports {
			sb.WriteString(fmt.Sprintf("      - \"%s\"\n", port))
		}
	}
	if len(p.Volumes) > 0 {
		sb.WriteString("    volumes:\n")
		for _, vol := range p.Volumes {
			sb.WriteString(fmt.Sprintf("      - \"%s\"\n", vol))
		}
	}
	if len(p.Env) > 0 {
		sb.WriteString("    environment:\n")
		for _, env := range p.Env {
			sb.WriteString(fmt.Sprintf("      - %s\n", env))
		}
	}
	if len(p.Command) > 0 {
		sb.WriteString("    command:\n")
		for _, cmd := range p.Command {
			sb.WriteString(fmt.Sprintf("      - %s\n", cmd))
		}
	}
	if p.Network == "host" || p.Network == "none" || p.Network == "bridge" {
		sb.WriteString(fmt.Sprintf("    network_mode: %s\n", p.Network))
	} else if p.Network != "" {
		sb.WriteString("    networks:\n")
		sb.WriteString(fmt.Sprintf("      - %s\n", p.Network))
		sb.WriteString("\nnetworks:\n")
		sb.WriteString(fmt.Sprintf("  %s:\n", p.Network))
		sb.WriteString("    external: true\n")
	}

	// Auto-declare named volumes so Docker Compose creates and manages them
	var namedVolumes []string
	seenVolumes := make(map[string]bool)
	for _, vol := range p.Volumes {
		src := extractVolumeSource(vol)
		if isNamedVolume(src) && !seenVolumes[src] {
			seenVolumes[src] = true
			namedVolumes = append(namedVolumes, src)
		}
	}

	if len(namedVolumes) > 0 {
		sb.WriteString("\nvolumes:\n")
		for _, nv := range namedVolumes {
			sb.WriteString(fmt.Sprintf("  %s:\n", nv))
			sb.WriteString(fmt.Sprintf("    name: %s\n", nv))
		}
	}

	return sb.String()
}

func splitShellArgs(cmd string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)
	escaped := false

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		// Handle line continuation: \ followed by \r\n or \n
		if !inQuotes && c == '\\' {
			if i+1 < len(cmd) && cmd[i+1] == '\n' {
				i++
				continue
			}
			if i+2 < len(cmd) && cmd[i+1] == '\r' && cmd[i+2] == '\n' {
				i += 2
				continue
			}
			if i+1 < len(cmd) && cmd[i+1] == '\r' {
				i++
				continue
			}
		}

		if escaped {
			current.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' {
			escaped = true
			continue
		}

		if inQuotes {
			if c == quoteChar {
				inQuotes = false
			} else {
				current.WriteByte(c)
			}
			continue
		}

		if c == '"' || c == '\'' {
			inQuotes = true
			quoteChar = c
			continue
		}

		if unicode.IsSpace(rune(c)) {
			if current.Len() > 0 {
				val := strings.TrimSpace(current.String())
				if val != "" {
					args = append(args, val)
				}
				current.Reset()
			}
			continue
		}

		current.WriteByte(c)
	}

	if current.Len() > 0 {
		val := strings.TrimSpace(current.String())
		if val != "" {
			args = append(args, val)
		}
	}
	return args
}
