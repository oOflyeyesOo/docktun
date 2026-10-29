package service

import (
	"net/url"
	"strings"
	"time"
)

type tunnelIngressSnapshot struct {
	rules              map[string]cfIngressRule
	updated, attempted time.Time
	refreshing         bool
	mutation           uint64
}

func tunnelCacheKey(identity TunnelIdentity) string { return identity.AccountID + "/" + identity.ID }

func ingressContainer(service string) string {
	u, err := url.Parse(service)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "tcp") || u.User != nil {
		return ""
	}
	return u.Hostname()
}

func projectedIngress(configuration map[string]any) map[string]cfIngressRule {
	rules := map[string]cfIngressRule{}
	ingress, _ := configuration["ingress"].([]any)
	for _, item := range ingress {
		rule, _ := item.(map[string]any)
		host, _ := rule["hostname"].(string)
		service, _ := rule["service"].(string)
		path, _ := rule["path"].(string)
		app := ingressContainer(service)
		// A path-specific route must not replace the hostname-wide app setting.
		if host == "" || app == "" || path != "" {
			continue
		}
		if _, found := rules[app]; !found {
			rules[app] = cfIngressRule{Hostname: host, Service: service}
		}
	}
	return rules
}

func (s *CloudflareAPI) tunnelSnapshotLocked(identity TunnelIdentity) *tunnelIngressSnapshot {
	if s.tunnelCache == nil {
		s.tunnelCache = map[string]*tunnelIngressSnapshot{}
	}
	key := tunnelCacheKey(identity)
	if s.tunnelCache[key] == nil {
		s.tunnelCache[key] = &tunnelIngressSnapshot{}
	}
	return s.tunnelCache[key]
}

func (s *CloudflareAPI) tunnelMutation(identity TunnelIdentity) uint64 {
	s.tunnelCacheMu.Lock()
	defer s.tunnelCacheMu.Unlock()
	return s.tunnelSnapshotLocked(identity).mutation
}

func (s *CloudflareAPI) rememberTunnelIngress(identity TunnelIdentity, configuration map[string]any, expected *uint64) {
	rules := projectedIngress(configuration)
	s.tunnelCacheMu.Lock()
	defer s.tunnelCacheMu.Unlock()
	snapshot := s.tunnelSnapshotLocked(identity)
	// An earlier GET must never replace a newer successful route write.
	if expected != nil && snapshot.mutation != *expected {
		return
	}
	if expected == nil {
		snapshot.mutation++
	}
	snapshot.rules, snapshot.updated = rules, time.Now()
}

// Inventory stays independent of Cloudflare latency and API availability.
// Explicit configuration reads/writes also refresh this same snapshot.
func (s *CloudflareAPI) cachedTunnelIngress(identity TunnelIdentity) map[string]cfIngressRule {
	s.tunnelCacheMu.Lock()
	snapshot := s.tunnelSnapshotLocked(identity)
	rules := make(map[string]cfIngressRule, len(snapshot.rules))
	for app, rule := range snapshot.rules {
		rules[app] = rule
	}
	refresh := !snapshot.refreshing && time.Since(snapshot.updated) > 15*time.Second && time.Since(snapshot.attempted) > 15*time.Second
	if refresh {
		snapshot.refreshing, snapshot.attempted = true, time.Now()
	}
	s.tunnelCacheMu.Unlock()
	if refresh {
		go func() {
			_, _ = s.TunnelConfiguration(identity) // tunnelRequest bounds the request to ten seconds.
			s.tunnelCacheMu.Lock()
			s.tunnelSnapshotLocked(identity).refreshing = false
			s.tunnelCacheMu.Unlock()
		}()
	}
	return rules
}

func projectExposure(saved ExposureSettings, ingress cfIngressRule, found bool) ExposureSettings {
	if saved.Mode == "" {
		saved.Mode = "internal"
	}
	if found {
		saved.Mode, saved.Hostname, saved.Target = "public", ingress.Hostname, ingress.Service
	}
	return saved
}

func setContainerExposure(container *Container, exposure ExposureSettings) {
	container.Exposure = exposure
	container.ExposureMode, container.Hostname, container.Target = exposure.Mode, exposure.Hostname, exposure.Target
	container.PrivateType, container.PrivateHost, container.PrivatePort = exposure.PrivateType, exposure.PrivateHost, exposure.PrivatePort
	container.Notes = exposure.Notes
	container.ZeroTrustEnabled, container.ZeroTrustEmails = exposure.ZeroTrustEnabled, exposure.ZeroTrustEmails
	container.ZeroTrustAuthType, container.ZeroTrustSession = exposure.ZeroTrustAuthType, exposure.ZeroTrustSession
}

func exactNetworkMembership(networks, desired string) bool {
	for _, name := range strings.Split(networks, ",") {
		if strings.TrimSpace(name) == desired {
			return true
		}
	}
	return false
}
