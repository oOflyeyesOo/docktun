package service

import (
	"context"
	"crypto/sha256"
	"docktun/internal/config"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"time"
)

type SupportBundles struct {
	Directory  string
	Operations *OperationStore
	Config     *config.Config
}
type SupportPreview struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Report any    `json:"report"`
}

func (s *SupportBundles) Create(ctx context.Context, includeOperations bool) (SupportPreview, error) {
	report := map[string]any{"format": 1, "created_at": time.Now().UTC(), "excluded": []string{"environment values", "credentials", "signing/setup secrets", "database contents", "application files", "raw application logs", "host mount source paths"}}
	build, err := inspectRunningBuild(ctx)
	if err != nil {
		build.Error = "Installed image identity could not be verified"
	}
	report["running_build"] = build
	var info struct {
		ServerVersion, OperatingSystem, Architecture string
		NCPU                                         int
	}
	if dockerJSON(ctx, "GET", "/info", nil, &info) == nil {
		report["docker"] = info
	} else {
		report["docker"] = map[string]string{"state": "unavailable"}
	}
	var rows []struct {
		ID      string `json:"Id"`
		Names   []string
		ImageID string
		State   string
	}
	if dockerJSON(ctx, "GET", "/containers/json?all=true", nil, &rows) == nil {
		safe := []map[string]any{}
		for _, row := range rows {
			if len(safe) >= 100 {
				break
			}
			container, e := inspectUpdateContainer(ctx, row.ID)
			if e != nil {
				continue
			}
			destinations := []string{}
			for _, mount := range container.Mounts {
				destinations = append(destinations, mount.Type+":"+mount.Destination)
			}
			networks := []string{}
			for network := range container.NetworkSettings.Networks {
				networks = append(networks, network)
			}
			safe = append(safe, map[string]any{"id": row.ID, "names": row.Names, "image_id": row.ImageID, "state": row.State, "mount_destinations": destinations, "networks": networks})
		}
		report["containers"] = safe
	}
	if includeOperations {
		operations, e := s.Operations.List("")
		if e != nil {
			return SupportPreview{}, e
		}
		report["operation_events"] = operations
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return SupportPreview{}, err
	}
	if len(raw) > 4<<20 {
		return SupportPreview{}, fmt.Errorf("support preview exceeds size limit; omit operation history")
	}
	if err = os.MkdirAll(s.Directory, 0700); err != nil {
		return SupportPreview{}, err
	}
	entries, _ := os.ReadDir(s.Directory)
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			name := entry.Name()
			if len(name) == 41 && filepath.Ext(name) == ".json" {
				if _, e := uuid.Parse(name[:36]); e == nil {
					if info, e := entry.Info(); e == nil && time.Since(info.ModTime()) > time.Hour {
						_ = os.Remove(filepath.Join(s.Directory, name))
					}
				}
			}
		}
	}
	id := uuid.NewString()
	if err = os.WriteFile(filepath.Join(s.Directory, id+".json"), raw, 0600); err != nil {
		return SupportPreview{}, err
	}
	sum := sha256.Sum256(raw)
	return SupportPreview{ID: id, SHA256: hex.EncodeToString(sum[:]), Report: report}, nil
}
func (s *SupportBundles) Read(id string) ([]byte, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return nil, fmt.Errorf("invalid support bundle")
	}
	root, err := os.OpenRoot(s.Directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(id + ".json")
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 || time.Since(info.ModTime()) > time.Hour {
		return nil, fmt.Errorf("support bundle expired; generate a fresh preview")
	}
	return root.ReadFile(id + ".json")
}
