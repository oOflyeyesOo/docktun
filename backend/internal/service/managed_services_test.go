package service

import "testing"

func managedFixture(name string, labels map[string]string) updateContainer {
	c := updateContainer{Name: "/" + name, ID: name + "0123456789abcdef", Config: map[string]any{}, HostConfig: map[string]any{}}
	values := map[string]any{}
	for k, v := range labels {
		values[k] = v
	}
	c.Config["Labels"] = values
	return c
}

func TestManagedOwnershipDoesNotUseNamePrefixes(t *testing.T) {
	core := managedFixture("custom-core", map[string]string{"com.docker.compose.project": "custom", "com.docker.compose.project.working_dir": "/volume1/docktun"})
	if managedServiceKind(core, core) != "core" {
		t.Fatal("custom core identity lost")
	}
	for _, name := range []string{"docktun-backup", "tailscale", "wireguard", "cloudflared", "docktun-cloudflared"} {
		if kind := managedServiceKind(managedFixture(name, nil), core); kind != "" {
			t.Fatalf("unowned %s classified as %s", name, kind)
		}
	}
	provider := managedFixture("docktun-cloudflared", map[string]string{"docktun.provider": "cloudflare"})
	if managedServiceKind(provider, core) != "cloudflare" {
		t.Fatal("managed provider lost")
	}
	app := "photos"
	node := managedFixture(tailscaleNodeName(app), map[string]string{tailscaleOwnerLabel: app})
	if managedServiceKind(node, core) != "tailscale-app" {
		t.Fatal("per-app device lost")
	}
	node.Name = "/unrelated"
	if managedServiceKind(node, core) != "" {
		t.Fatal("mismatched device ownership accepted")
	}
}

func TestManagedComposeOwnershipRequiresSameHostDirectory(t *testing.T) {
	labels := map[string]string{"com.docker.compose.project": "docktun", "com.docker.compose.project.working_dir": "/volume1/primary"}
	core := managedFixture("core", labels)
	other := managedFixture("other-proxy", map[string]string{"com.docker.compose.project": "docktun", "com.docker.compose.project.working_dir": "/volume1/neighbor", "com.docker.compose.service": "zeroconf"})
	if managedServiceKind(other, core) != "" {
		t.Fatal("same project name in another folder was captured")
	}
	other.Config["Labels"].(map[string]any)["com.docker.compose.project.working_dir"] = "/volume1/primary"
	if managedServiceKind(other, core) != "zeroconf" {
		t.Fatal("own Compose proxy not recognized")
	}
}

func TestManagedImageSourceSurvivesImmutableRecreation(t *testing.T) {
	core := managedFixture("core", nil)
	core.Config["Env"] = []any{"DOCKTUN_UPDATE_IMAGE=sha256:offline-core"}
	provider := managedFixture("docktun-tailscale", map[string]string{serviceImageLabel: "tailscale/tailscale:stable"})
	provider.Config["Image"] = "sha256:installed"
	if serviceImage(provider, core, "tailscale") != "tailscale/tailscale:stable" {
		t.Fatal("image tag lost after immutable recreation")
	}
	if serviceImage(provider, core, "zeroconf") != "sha256:offline-core" {
		t.Fatal("local proxy did not follow core release source")
	}
}
