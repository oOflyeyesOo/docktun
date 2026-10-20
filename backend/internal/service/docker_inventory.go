package service

import (
  "fmt"
  "os/exec"
  "sort"
  "strings"
)

type Image struct {
  Repository string `json:"repository"`
  Tag        string `json:"tag"`
  ID         string `json:"id"`
  Size       string `json:"size"`
}

type Network struct {
  ID     string `json:"id"`
  Name   string `json:"name"`
  Driver string `json:"driver"`
  Scope  string `json:"scope"`
}

type Volume struct {
  Name   string `json:"name"`
  Driver string `json:"driver"`
  Scope  string `json:"scope"`
}

type DockerInventory struct{}

func NewDockerInventory() *DockerInventory { return &DockerInventory{} }

func (s *DockerInventory) ListImages() ([]Image, error) {
  cmd := exec.Command("docker", "image", "ls", "--format", "{{.Repository}}\t{{.Tag}}\t{{.ID}}\t{{.Size}}")
  out, err := cmd.Output()
  if err != nil {
    return nil, fmt.Errorf("docker image ls failed: %w", err)
  }

  text := strings.TrimSpace(string(out))
  if text == "" {
    return []Image{}, nil
  }

  rows := strings.Split(text, "\n")
  images := make([]Image, 0, len(rows))
  for _, row := range rows {
    fields := strings.SplitN(strings.TrimSpace(row), "\t", 4)
    if len(fields) < 4 {
      continue
    }
    images = append(images, Image{
      Repository: strings.TrimSpace(fields[0]),
      Tag:        strings.TrimSpace(fields[1]),
      ID:         strings.TrimSpace(fields[2]),
      Size:       strings.TrimSpace(fields[3]),
    })
  }

  sort.Slice(images, func(i, j int) bool {
    if images[i].Repository == images[j].Repository {
      return images[i].Tag < images[j].Tag
    }
    return strings.ToLower(images[i].Repository) < strings.ToLower(images[j].Repository)
  })

  return images, nil
}

func (s *DockerInventory) ListNetworks() ([]Network, error) {
  cmd := exec.Command("docker", "network", "ls", "--format", "{{.ID}}\t{{.Name}}\t{{.Driver}}\t{{.Scope}}")
  out, err := cmd.Output()
  if err != nil {
    return nil, fmt.Errorf("docker network ls failed: %w", err)
  }

  text := strings.TrimSpace(string(out))
  if text == "" {
    return []Network{}, nil
  }

  rows := strings.Split(text, "\n")
  networks := make([]Network, 0, len(rows))
  for _, row := range rows {
    fields := strings.SplitN(strings.TrimSpace(row), "\t", 4)
    if len(fields) < 4 {
      continue
    }
    networks = append(networks, Network{
      ID:     strings.TrimSpace(fields[0]),
      Name:   strings.TrimSpace(fields[1]),
      Driver: strings.TrimSpace(fields[2]),
      Scope:  strings.TrimSpace(fields[3]),
    })
  }

  sort.Slice(networks, func(i, j int) bool { return strings.ToLower(networks[i].Name) < strings.ToLower(networks[j].Name) })
  return networks, nil
}

func (s *DockerInventory) ListVolumes() ([]Volume, error) {
  cmd := exec.Command("docker", "volume", "ls", "--format", "{{.Name}}\t{{.Driver}}\t{{.Scope}}")
  out, err := cmd.Output()
  if err != nil {
    return nil, fmt.Errorf("docker volume ls failed: %w", err)
  }

  text := strings.TrimSpace(string(out))
  if text == "" {
    return []Volume{}, nil
  }

  rows := strings.Split(text, "\n")
  volumes := make([]Volume, 0, len(rows))
  for _, row := range rows {
    fields := strings.SplitN(strings.TrimSpace(row), "\t", 3)
    if len(fields) < 3 {
      continue
    }
    volumes = append(volumes, Volume{
      Name:   strings.TrimSpace(fields[0]),
      Driver: strings.TrimSpace(fields[1]),
      Scope:  strings.TrimSpace(fields[2]),
    })
  }

  sort.Slice(volumes, func(i, j int) bool { return strings.ToLower(volumes[i].Name) < strings.ToLower(volumes[j].Name) })
  return volumes, nil
}

func (s *DockerInventory) RemoveImage(ref string) error {
  return dockerInventoryCommand("image", "rm", ref)
}

func (s *DockerInventory) PruneImages() error {
  return dockerInventoryCommand("image", "prune", "-f")
}

func (s *DockerInventory) RemoveNetwork(name string) error {
  return dockerInventoryCommand("network", "rm", name)
}

func (s *DockerInventory) RemoveVolume(name string) error {
  return dockerInventoryCommand("volume", "rm", name)
}

func (s *DockerInventory) PruneVolumes() error {
  return dockerInventoryCommand("volume", "prune", "-f")
}

func dockerInventoryCommand(args ...string) error {
  cmd := exec.Command("docker", args...)
  out, err := cmd.CombinedOutput()
  if err != nil {
    return fmt.Errorf("docker %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
  }
  return nil
}
