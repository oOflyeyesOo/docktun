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
# Optional profile services are excluded by default from Compose's rendered config.
with_cloudflare = subprocess.check_output(
    ["docker", "compose", "-f", compose.name, "--profile", "cloudflare", "config", "--format", "json"],
    cwd=root,
    env=env,
    text=True,
)
profile_services = json.loads(with_cloudflare)["services"]
assert "cloudflared" in profile_services
assert "cloudflare" in profile_services["cloudflared"].get("profiles", [])
assert profile_services["cloudflared"]["image"] == "cloudflare/cloudflared:latest"
assert any(port.get("target") == 3001 for port in services["docktun"]["ports"])
assert any("/var/run/docker.sock" in str(mount.get("source")) for mount in services["docktun"]["volumes"])
print("Prebuilt Compose installer structure passed")
# Tailscale is opt-in and shares exactly the UI's Docker volume, without a TUN device.
with_tailscale = json.loads(subprocess.check_output(
    ['docker', 'compose', '-f', compose.name, '--profile', 'tailscale', 'config', '--format', 'json'],
    cwd=root, env=env, text=True,
))
ts = with_tailscale['services']['tailscale']
assert 'tailscale' not in services and 'cloudflared' not in services
assert 'cloudflared' not in with_tailscale['services']
assert ts['environment']['TS_USERSPACE'] == 'true'
assert not ts.get('devices') and not ts.get('cap_add')
mount = next(m for m in ts['volumes'] if m['target'] == '/var/lib/tailscale')
assert mount['type'] == 'volume'
assert with_tailscale['volumes'][mount['source']]['name'] == with_tailscale['services']['docktun']['environment']['TAILSCALE_STATE_VOLUME']
print('Tailscale profile, portable userspace mode and shared named state volume passed')
