package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/dustin/go-humanize"
	"os/exec"
	"strconv"
	"strings"
)

type NotificationSample struct {
	Containers  map[string]bool
	Names       map[string]string
	CPU, Memory float64
	UsageKnown  bool
}

func SampleNotifications(ctx context.Context) (NotificationSample, error) {
	sample := NotificationSample{Containers: map[string]bool{}, Names: map[string]string{}}
	raw, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{json .}}").Output()
	if err != nil {
		return sample, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var row struct{ ID, Names, State string }
		if err = json.Unmarshal([]byte(line), &row); err != nil {
			return sample, err
		}
		sample.Containers[row.ID] = row.State == "running"
		sample.Names[row.ID] = row.Names
	}
	raw, err = exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}").Output()
	if err != nil {
		return sample, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var row struct{ CPUPerc, MemUsage string }
		if json.Unmarshal([]byte(line), &row) != nil {
			return sample, nil
		}
		cpu, e := strconv.ParseFloat(strings.TrimSuffix(row.CPUPerc, "%"), 64)
		if e != nil {
			return sample, nil
		}
		used := strings.TrimSpace(strings.SplitN(row.MemUsage, "/", 2)[0])
		mem, e := humanize.ParseBytes(used)
		if e != nil {
			return sample, nil
		}
		sample.CPU += cpu
		sample.Memory += float64(mem)
	}
	raw, err = exec.CommandContext(ctx, "docker", "info", "--format", "{{json .}}").Output()
	if err != nil {
		return sample, nil
	}
	var host struct {
		NCPU     int
		MemTotal int64
	}
	if err = json.Unmarshal(raw, &host); err != nil || host.NCPU <= 0 || host.MemTotal <= 0 {
		return sample, fmt.Errorf("unknown Docker capacity")
	}
	sample.CPU /= float64(host.NCPU)
	sample.Memory = sample.Memory * 100 / float64(host.MemTotal)
	sample.UsageKnown = true
	return sample, nil
}
