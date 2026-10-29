"""Validate the shareable installer without pulling packages or starting containers."""
import json
import os
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
compose = root / "compose.prebuilt.yml"
assert compose.is_file(), "Shareable Compose file is missing"

env = dict(os.environ)
env.pop("PORT", None)
config = subprocess.check_output(
    ["docker", "compose", "-f", compose.name, "config", "--format", "json"],
    cwd=root,
    env=env,
    text=True,
)
services = json.loads(config)["services"]
assert "docktun" in services
assert services["docktun"]["image"] == "ghcr.io/ooflyeyesoo/docktun:latest"
assert not any("build" in service for service in services.values())
assert set(services) == {'docktun', 'zeroconf'}, 'Compose must not start optional tunnel providers'
proxy = services['zeroconf']
assert proxy['image'] == services['docktun']['image']
assert proxy['entrypoint'] == ['/app/zeroconf']
assert proxy['network_mode'] == 'host'
assert proxy['environment']['DOCKTUN_PORT'] == '8080'
assert any(m['target'] == '/var/run/docker.sock' and m.get('read_only') for m in proxy['volumes'])
assert any(port.get('target') == 3001 for port in services['docktun']['ports'])
mounts = {m['target']: m for m in services['docktun']['volumes']}
assert mounts['/etc/cloudflared']['type'] == 'volume'
assert mounts['/app/data/wireguard']['type'] == 'volume'
assert any('/var/run/docker.sock' in str(m.get('source')) for m in mounts.values())
assert services['docktun']['environment']['TAILSCALE_STATE_VOLUME'] == 'docktun-tailscale-state'
custom_env = dict(env, DOCKER_NETWORK='synology-custom-network')
custom_env.pop('TAILSCALE_NETWORK', None)
custom = json.loads(subprocess.check_output(['docker', 'compose', '-f', compose.name, 'config', '--format', 'json'], cwd=root, env=custom_env, text=True))
assert custom['networks']['docktun']['name'] == 'synology-custom-network'
assert custom['services']['docktun']['environment']['TAILSCALE_NETWORK'] == 'synology-custom-network'
assert not (root / 'frontend/src/views/SetupView.vue').exists()
print('Portable prebuilt Compose passed: app and local proxy only, persistent provider mounts, independent network settings, no onboarding')
