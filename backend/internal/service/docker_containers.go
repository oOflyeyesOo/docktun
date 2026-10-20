package service

import (
  "fmt"
  "os/exec"
  "sort"
  "strconv"
  "strings"
)

type Container struct {
  ID      string `json:"id"`
  Name    string `json:"name"`
  Image   string `json:"image"`
  Status  string `json:"status"`
  State   string `json:"state"`
  Ports   string `json:"ports"`
  Command string `json:"command"`
  Created string `json:"created"`
  Running bool   `json:"running"`
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
      Running: strings.EqualFold(fields[4], "running"),
    }
    if c.Name == "" {
      c.Name = c.ID
    }
    c.Running = c.Running || strings.HasPrefix(strings.ToLower(c.Status), "up ")
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

func (s *DockerContainers) Start(id string) error {
  return dockerCommand("start", id)
}

func (s *DockerContainers) Stop(id string) error {
  return dockerCommand("stop", id)
}

func (s *DockerContainers) Restart(id string) error {
  return dockerCommand("restart", id)
}

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

func dockerCommand(args ...string) error {
  cmd := exec.Command("docker", args...)
  out, err := cmd.CombinedOutput()
  if err != nil {
    return fmt.Errorf("docker %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
  }
  return nil
}
