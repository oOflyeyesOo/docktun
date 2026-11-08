package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

type ExposurePlan struct {
	Ready       bool        `json:"ready"`
	Checks      []PlanCheck `json:"checks"`
	Changes     []string    `json:"changes"`
	Limitations []string    `json:"limitations"`
}

func (s *DockerContainers) ExposurePreflight(ctx context.Context, id string, settings ExposureSettings) (ExposurePlan, error) {
	plan := ExposurePlan{Ready: true, Checks: []PlanCheck{}, Changes: []string{}, Limitations: []string{"Preview does not change routes, DNS, Access policies, networks or container settings.", "Read permissions do not prove write permissions. Changes are checked again when applied; provider-side partial failures require reviewing the reported route/DNS state."}}
	old, err := inspectUpdateContainer(ctx, id)
	if err != nil {
		return plan, err
	}
	name := strings.TrimPrefix(old.Name, "/")
	add := func(id, label, status, message string) {
		plan.Checks = append(plan.Checks, PlanCheck{id, label, status, message})
		if status == "failed" {
			plan.Ready = false
		}
	}
	if settings.Mode == "" {
		settings.Mode = "internal"
	}
	switch settings.Mode {
	case "internal":
		plan.Changes = append(plan.Changes, "Remove DockTun-managed remote access; existing Docker-published ports remain reachable under their own settings")
	case "public":
		if err := s.exposureHostnameAvailable(name, settings.Hostname); err != nil {
			add("hostname-owner", "Hostname ownership", "failed", err.Error())
		}
		if cleanHostname(settings.Hostname) == "" {
			add("hostname", "Hostname", "failed", "Enter a valid public hostname")
		}
		if err := s.validateCloudflarePublish(); err != nil {
			add("provider", "Tunnel configuration", "failed", err.Error())
		} else {
			add("provider", "Tunnel configuration", "passed", "Configured tunnel can be read; publishing still requires tunnel and DNS write permissions")
		}
		target := s.normalizeTarget(name, settings.Target)
		u, e := url.Parse(target)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			add("origin", "Origin", "failed", "Use an HTTP(S) container target without embedded credentials")
		} else {
			if e = probeReadiness(ctx, target); e != nil {
				add("origin", "Origin reachability", "warning", "Origin did not return 2xx from DockTun; verify the internal port and tunnel network before publishing")
			} else {
				add("origin", "Origin reachability", "passed", "Origin returned 2xx from DockTun")
			}
		}
		if !old.State.Running {
			add("running", "Application", "failed", "Start the application before publishing")
		}
		if settings.ZeroTrustEnabled {
			if s.cfAPI == nil || !s.cfAPI.GetCredentials().HasToken {
				add("access", "Access protection", "failed", "API credentials are required for Access protection")
			} else {
				add("access", "Access protection", "warning", "Apply confirms the requested Access policy before publishing the route")
			}
		} else {
			add("access", "Public access", "warning", "Anyone who can reach this hostname may reach the application; confirm its own authentication is ready")
		}
		plan.Changes = append(plan.Changes, fmt.Sprintf("Publish %s → %s; attach the app to the tunnel network if needed", cleanHostname(settings.Hostname), target))
	case "private":
		if settings.PrivateType != "tailscale" && settings.PrivateType != "wireguard" && settings.PrivateType != "mesh" {
			add("provider", "Private provider", "failed", "Choose a supported private provider")
		}
		if settings.PrivateType != "tailscale" && (strings.TrimSpace(settings.PrivateHost) == "" || settings.PrivatePort < 1 || settings.PrivatePort > 65535) {
			add("address", "VPN endpoint", "failed", "Provide an existing VPN address and valid port")
		}
		add("routing", "VPN routing", "warning", "Existing VPN routing and peer permissions must be configured; this preview cannot prove client-side access")
		plan.Changes = append(plan.Changes, "Save private access settings and remove the previous DockTun-managed public route")
	default:
		add("mode", "Access method", "failed", "Choose internal, public or private access")
	}
	return plan, nil
}

func (s *DockerContainers) exposureHostnameAvailable(name, hostname string) error {
	if s.db == nil {
		return nil
	}
	var owner string
	err := s.db.QueryRow(`SELECT container_name FROM container_exposures WHERE mode='public' AND lower(hostname)=? AND container_name<>? LIMIT 1`, strings.ToLower(cleanHostname(hostname)), name).Scan(&owner)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("hostname is already assigned to another app; remove or change that route first")
}
