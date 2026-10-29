package service

import "fmt"

// Reject missing management credentials before changing any app networking.
// This is an explicit publish action; inventory itself never waits on the API.
func (s *DockerContainers) validateCloudflarePublish() error {
	if s.configMgr == nil {
		return fmt.Errorf("configure Cloudflare before publishing an app")
	}
	identity, err := s.configMgr.TunnelIdentity()
	if err != nil {
		return err
	}
	if identity.Remote {
		if s.cfAPI == nil {
			return fmt.Errorf("Cloudflare API service is unavailable")
		}
		_, err := s.cfAPI.TunnelConfiguration(identity)
		return err
	}
	if identity.ID == "" {
		return fmt.Errorf("configure the Cloudflare tunnel ID and credentials before publishing an app")
	}
	return nil
}

// An explicit access change must not use an empty/stale inventory snapshot to
// conclude that a remotely managed public route does not exist. Only configured
// management credentials participate; unused Cloudflare stays independent.
func (s *DockerContainers) exposureIngressForMutation(mode string) (map[string]cfIngressRule, error) {
	if mode != "public" && s.configMgr != nil && s.cfAPI != nil {
		identity, identityErr := s.configMgr.TunnelIdentity()
		if identityErr == nil && identity.Remote && s.cfAPI.GetCredentials().HasToken {
			configuration, err := s.cfAPI.TunnelConfiguration(identity)
			if err != nil { return nil, fmt.Errorf("verify existing Cloudflare routes before changing app access: %w", err) }
			return projectedIngress(configuration), nil
		}
	}
	return s.loadCloudflareIngressMap(), nil
}
