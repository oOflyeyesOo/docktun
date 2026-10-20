package service

import (
  "encoding/json"
  "fmt"
  "os"
  "os/exec"
  "path/filepath"
  "sort"
  "strconv"
  "strings"
  "time"
)

type ExposureSettings struct {
  Mode        string `json:"mode"`
  Hostname    string `json:"hostname,omitempty"`
  Target      string `json:"target,omitempty"`
  PrivateType string `json:"private_type,omitempty"`
  PrivateHost string `json:"private_host,omitempty"`
  PrivatePort int    `json:"private_port,omitempty"`
  Notes       string `json:"notes,omitempty"`
}

type Container struct {
  ID           string            `json:"id"`
  Name         string            `json:"name"`
  Image        string            `json:"image"`
  Status       string            `json:"status"`
  State        string            `json:"state"`
  Ports        string            `json:"ports"`
  Command      string            `json:"command"`
  Created      string            `json:"created"`
  Running      bool              `json:"running"`
  ExposureMode  string            `json:"exposure_mode"`
  Hostname     string            `json:"hostname,omitempty"`
  Target       string            `json:"target,omitempty"`
  PrivateType  string            `json:"private_type,omitempty"`
  PrivateHost  string            `json:"private_host,omitempty"`
  PrivatePort  int               `json:"private_port,omitempty"`
  Notes        string            `json:"notes,omitempty"`
  CPU          string            `json:"cpu,omitempty"`
  Memory       string            `json:"memory,omitempty"`
  Network      string            `json:"network,omitempty"`
  PIDs         int               `json:"pids,omitempty"`
  RestartCount int               `json:"restart_count,omitempty"`
  Uptime       string            `json:"uptime,omitempty"`
  Exposure     ExposureSettings  `json:"exposure,omitempty"`
}

type ContainerDetail struct {
  Container
}

type DockerContainers struct{}

func NewDockerContainers() *DockerContainers { return &DockerContainers{} }

func (s *DockerContainers) List() ([]Container, error) {
  cmd := exec.Command("docker", "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.State}}\t{{.Ports}}\t{{.Command}}")
  out, err := cmd.Output()
  if err != nil {
    return nil, fmt.Errorf("docker ps failed: %w", err)
  }

  text := strings.TrimSpace(string(out))
  if text == "" {
    return []Container{}, nil
  }

  rows := strings.Split(text, "\n")
  containers := make([]Container, 0, len(rows))

  for _, row := range rows {
    row = strings.TrimSpace(row)
    if row == "" {
      continue
    }

    fields := strings.SplitN(row, "\t", 7)
    if len(fields) < 7 {
      continue
    }

    c := Container{
      ID:      strings.TrimSpace(fields[0]),
      Name:    strings.TrimSpace(fields[1]),
      Image:   strings.TrimSpace(fields[2]),
      Status:  strings.TrimSpace(fields[3]),
      State:   strings.TrimSpace(fields[4]),
      Ports:   strings.TrimSpace(fields[5]),
      Command: strings.TrimSpace(fields[6]),
      Running: strings.EqualFold(fields[3], "running"),
    }
    if c.Name == "" {
      c.Name = c.ID
    }
    c.Name = strings.TrimPrefix(c.Name, "/")
    c.Running = c.Running || strings.HasPrefix(strings.ToLower(c.Status), "up ")

    exposure := s.loadExposureForName(c.Name)
    c.ExposureMode = exposure.Mode
    c.Hostname = exposure.Hostname
    c.Target = exposure.Target
    c.PrivateType = exposure.PrivateType
    c.PrivateHost = exposure.PrivateHost
    c.PrivatePort = exposure.PrivatePort
    c.Notes = exposure.Notes
    c.Exposure = exposure

    if c.Status == "" {
      c.Status = c.State
    }

    containers = append(containers, c)
  }

  sort.Slice(containers, func(i, j int) bool {
    if containers[i].Running != containers[j].Running {
      return containers[i].Running
    }
    return strings.ToLower(containers[i].Name) < strings.ToLower(containers[j].Name)
  })

  return containers, nil
}

func (s *DockerContainers) Details(id string) (ContainerDetail, error) {
  detail := ContainerDetail{}

  cmd := exec.Command("docker", "inspect", "--format", "{{json .}}", id)
  out, err := cmd.Output()
  if err != nil {
    return detail, fmt.Errorf("docker inspect failed: %w", err)
  }

  var inspect struct {
    ID      string `json:"Id"`
    Name    string `json:"Name"`
    Image   string `json:"Config,omitempty"`
    Created string `json:"Created"`
    State   struct {
      Status      string `json:"Status"`
      Running     bool   `json:"Running"`
      StartedAt   string `json:"StartedAt"`
      Restarting  bool   `json:"Restarting"`
      RestartCount int    `json:"RestartCount"`
      ExitCode    int    `json:"ExitCode"`
    } `json:"State"`
    Config struct {
      Image   string   `json:"Image"`
      Cmd     []string `json:"Cmd"`
      Labels  map[string]string `json:"Labels"`
    } `json:"Config"`
    NetworkSettings struct {
      Ports map[string][]struct {
        HostIP    string `json:"HostIp"`
        HostPort  string `json:"HostPort"`
      } `json:"Ports"`
    } `json:"NetworkSettings"`
  }

  if err := json.Unmarshal(out, &inspect); err != nil {
    return detail, fmt.Errorf("inspect parse failed: %w", err)
  }

  c := Container{
    ID:      strings.TrimSpace(inspect.ID),
    Name:    strings.TrimPrefix(strings.TrimSpace(inspect.Name), "/"),
    Image:   strings.TrimSpace(inspect.Config.Image),
    Status:  strings.TrimSpace(inspect.State.Status),
    State:   strings.TrimSpace(inspect.State.Status),
    Created: strings.TrimSpace(inspect.Created),
    Running: inspect.State.Running,
    Command: strings.Join(inspect.Config.Cmd, " "),
    RestartCount: inspect.State.RestartCount,
  }

  if c.Name == "" {
    c.Name = id
  }

  if c.Status == "" {
    c.Status = "exited"
  }

  if c.Running {
    c.Uptime = uptimeFromStartedAt(inspect.State.StartedAt)
  }

  exp := s.loadExposureForName(c.Name)
  c.ExposureMode = exp.Mode
  c.Hostname = exp.Hostname
  c.Target = exp.Target
  c.PrivateType = exp.PrivateType
  c.PrivateHost = exp.PrivateHost
  c.PrivatePort = exp.PrivatePort
  c.Notes = exp.Notes
  c.Exposure = exp

  statsCmd := exec.Command("docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.PIDs}}", id)
  if statsOut, statsErr := statsCmd.Output(); statsErr == nil {
    row := strings.TrimSpace(string(statsOut))
    fields := strings.Split(row, "\t")
    if len(fields) >= 4 {
      c.CPU = strings.TrimSpace(fields[0])
      c.Memory = strings.TrimSpace(fields[1])
      c.Network = strings.TrimSpace(fields[2])
      if p, err := strconv.Atoi(strings.TrimSpace(fields[3])); err == nil {
        c.PIDs = p
      }
    }
  }

  if c.Ports == "" {
    ports := []string{}
    for port, bindings := range inspect.NetworkSettings.Ports {
      if len(bindings) == 0 {
        ports = append(ports, port)
        continue
      }
      for _, binding := range bindings {
        if binding.HostPort != "" {
          ports = append(ports, fmt.Sprintf("%s -> %s:%s", port, binding.HostIP, binding.HostPort))
        } else {
          ports = append(ports, fmt.Sprintf("%s -> %s", port, binding.HostIP))
        }
      }
    }
    if len(ports) > 0 {
      c.Ports = strings.Join(ports, ", ")
    }
  }

  detail.Container = c
  return detail, nil
}

func (s *DockerContainers) Start(id string) error { return dockerCommand("start", id) }
func (s *DockerContainers) Stop(id string) error { return dockerCommand("stop", id) }
func (s *DockerContainers) Restart(id string) error { return dockerCommand("restart", id) }

func (s *DockerContainers) Remove(id string, force bool) error {
  args := []string{"rm"}
  if force {
    args = append(args, "-f")
  }
  args = append(args, id)
  return dockerCommand(args...)
}

func (s *DockerContainers) Logs(id string, tail int) (string, error) {
  if tail <= 0 {
    tail = 100
  }

  cmd := exec.Command("docker", "logs", "--tail", strconv.Itoa(tail), id)
  out, err := cmd.CombinedOutput()
  if err != nil {
    return "", fmt.Errorf("docker logs failed: %w", err)
  }
  return strings.TrimRight(string(out), "\r\n"), nil
}

func (s *DockerContainers) UpdateExposure(id string, settings ExposureSettings) error {
  name, err := s.containerName(id)
  if err != nil {
    return err
  }

  mapping, err := s.readExposureMap()
  if err != nil {
    return err
  }

  if settings.Mode == "" {
    settings.Mode = "internal"
  }
  if settings.PrivatePort < 1 && settings.Target != "" {
    settings.PrivatePort = 80
  }

  mapping[name] = settings
  return s.writeExposureMap(mapping)
}

func (s *DockerContainers) loadExposureForName(name string) ExposureSettings {
  mapping, err := s.readExposureMap(); if err != nil { return ExposureSettings{Mode: "internal"} }
  if cfg, ok := mapping[strings.TrimPrefix(name, "/")]; ok {
    return cfg
  }
  return ExposureSettings{Mode: "internal"}
}

func (s *DockerContainers) readExposureMap() (map[string]ExposureSettings, error) {
  path := exposureFilePath()
  data, err := os.ReadFile(path)
  if err != nil {
    if os.IsNotExist(err) {
      return map[string]ExposureSettings{}, nil
    }
    return nil, err
  }

  var mapping map[string]ExposureSettings
  if len(data) == 0 {
    return map[string]ExposureSettings{}, nil
  }
  if err := json.Unmarshal(data, &mapping); err != nil {
    return nil, err
  }
  if mapping == nil {
    return map[string]ExposureSettings{}, nil
  }
  return mapping, nil
}

func (s *DockerContainers) writeExposureMap(mapping map[string]ExposureSettings) error {
  path := exposureFilePath()
  if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
    return err
  }
  data, err := json.MarshalIndent(mapping, "", "  ")
  if err != nil {
    return err
  }
  return os.WriteFile(path, data, 0o644)
}

func (s *DockerContainers) containerName(id string) (string, error) {
  cmd := exec.Command("docker", "inspect", "--format", "{{.Name}}", id)
  out, err := cmd.Output()
  if err != nil {
    return "", fmt.Errorf("docker inspect name failed: %w", err)
  }
  return strings.TrimPrefix(strings.TrimSpace(string(out)), "/"), nil
}

func exposureFilePath() string {
  return filepath.Join("data", "container_access.json")
}

func uptimeFromStartedAt(value string) string {
  if value == "" {
    return ""
  }
  t, err := time.Parse(time.RFC3339Nano, value)
  if err != nil {
    return value
  }
  return time.Since(t).Truncate(time.Second).String()
}

func dockerCommand(args ...string) error {
  cmd := exec.Command("docker", args...)
  out, err := cmd.CombinedOutput()
  if err != nil {
    return fmt.Errorf("docker %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
  }
  return nil
}
