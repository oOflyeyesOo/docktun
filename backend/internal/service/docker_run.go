package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/luzyver/docktun/internal/db"
)

type DockerRunJob struct {
	ID          int      `json:"id"`
	Command     string   `json:"command"`
	Mode        string   `json:"mode"`
	Lines       []string `json:"lines"`
	Done        bool     `json:"done"`
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
	ExtraArgs     []string
}

type DockerRunService struct {
	db          *db.DB
	projectsSvc *Projects
	mu          sync.RWMutex
	jobs        map[int]*DockerRunJob
	nextID      int
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
	return &copyJob, true
}

func (s *DockerRunService) appendLine(id int, line string) {
	s.mu.Lock()
	if j, ok := s.jobs[id]; ok {
		j.Lines = append(j.Lines, line)
	}
	s.mu.Unlock()
}

func (s *DockerRunService) markDone(id int, exitCode int, errMsg string) {
	s.mu.Lock()
	if j, ok := s.jobs[id]; ok {
		j.Done = true
		j.ExitCode = exitCode
		j.Error = errMsg
	}
	s.mu.Unlock()
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

	if connectCloudflared && parsed.Network == "" {
		parsed.Network = "cloudflared"
	}

	s.mu.Lock()
	jobID := s.nextID
	s.nextID++
	job := &DockerRunJob{
		ID:          jobID,
		Command:     trimmed,
		Mode:        mode,
		Lines:       []string{},
		Done:        false,
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
	s.appendLine(jobID, fmt.Sprintf("[STEP] Preparing Compose stack for '%s'...", parsed.ContainerName))

	projDir := filepath.Join("/opt/projects", parsed.ContainerName)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		s.appendLine(jobID, "[ERROR] Failed to create project directory: "+err.Error())
		s.markDone(jobID, 1, err.Error())
		return
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
	s.appendLine(jobID, "[STEP] Executing: docker compose up -d --build")

	cmd := exec.Command("docker", "compose", "--project-directory", projDir, "up", "-d", "--build")
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

	// Strip leading "docker"
	idx := 0
	if idx < len(tokens) && tokens[idx] == "docker" {
		idx++
	}
	cmdArgs = append(cmdArgs, tokens[idx:]...)

	// If cloudflared connect requested and --network wasn't supplied, add --network cloudflared
	hasNetwork := false
	for _, a := range cmdArgs {
		if a == "--network" || strings.HasPrefix(a, "--network=") {
			hasNetwork = true
			break
		}
	}
	if !hasNetwork && connectCloudflared {
		// Insert before image name
		inserted := false
		for i, a := range cmdArgs {
			if a == "run" {
				cmdArgs = append(cmdArgs[:i+1], append([]string{"--network", "cloudflared"}, cmdArgs[i+1:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			cmdArgs = append([]string{"--network", "cloudflared"}, cmdArgs...)
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

func ParseDockerRunCommand(raw string) (*ParsedDockerRun, error) {
	tokens := splitShellArgs(raw)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	idx := 0
	if idx < len(tokens) && tokens[idx] == "docker" {
		idx++
	}
	if idx < len(tokens) && tokens[idx] == "run" {
		idx++
	}

	res := &ParsedDockerRun{
		Restart: "unless-stopped",
		Network: "cloudflared",
	}

	var positional []string
	for i := idx; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "--name=") {
			res.ContainerName = strings.TrimPrefix(t, "--name=")
		} else if t == "--name" && i+1 < len(tokens) {
			i++
			res.ContainerName = tokens[i]
		} else if strings.HasPrefix(t, "-p=") || strings.HasPrefix(t, "--publish=") {
			val := strings.TrimPrefix(strings.TrimPrefix(t, "--publish="), "-p=")
			res.Ports = append(res.Ports, val)
		} else if (t == "-p" || t == "--publish") && i+1 < len(tokens) {
			i++
			res.Ports = append(res.Ports, tokens[i])
		} else if strings.HasPrefix(t, "-v=") || strings.HasPrefix(t, "--volume=") {
			val := strings.TrimPrefix(strings.TrimPrefix(t, "--volume="), "-v=")
			res.Volumes = append(res.Volumes, val)
		} else if (t == "-v" || t == "--volume") && i+1 < len(tokens) {
			i++
			res.Volumes = append(res.Volumes, tokens[i])
		} else if strings.HasPrefix(t, "-e=") || strings.HasPrefix(t, "--env=") {
			val := strings.TrimPrefix(strings.TrimPrefix(t, "--env="), "-e=")
			res.Env = append(res.Env, val)
		} else if (t == "-e" || t == "--env") && i+1 < len(tokens) {
			i++
			res.Env = append(res.Env, tokens[i])
		} else if strings.HasPrefix(t, "--restart=") {
			res.Restart = strings.TrimPrefix(t, "--restart=")
		} else if t == "--restart" && i+1 < len(tokens) {
			i++
			res.Restart = tokens[i]
		} else if strings.HasPrefix(t, "--network=") {
			res.Network = strings.TrimPrefix(t, "--network=")
		} else if t == "--network" && i+1 < len(tokens) {
			i++
			res.Network = tokens[i]
		} else if t == "-d" || t == "--detach" {
			res.Detach = true
		} else if t == "--rm" {
			res.Remove = true
		} else if strings.HasPrefix(t, "-") {
			res.ExtraArgs = append(res.ExtraArgs, t)
		} else {
			positional = append(positional, t)
		}
	}

	if len(positional) > 0 {
		res.Image = positional[0]
		if len(positional) > 1 {
			res.Command = positional[1:]
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
		res.ContainerName = base + "-" + fmt.Sprintf("%d", time.Now().Unix()%1000)
	}

	return res, nil
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
	if p.Network != "" {
		sb.WriteString("    networks:\n")
		sb.WriteString(fmt.Sprintf("      - %s\n", p.Network))
		sb.WriteString("\nnetworks:\n")
		sb.WriteString(fmt.Sprintf("  %s:\n", p.Network))
		sb.WriteString("    external: true\n")
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
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(c)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
