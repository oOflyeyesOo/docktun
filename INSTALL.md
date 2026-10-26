# Install DockTun

Download `compose.prebuilt.yml`, save it as `compose.yml`, and run:

```sh
docker compose up -d
```

For Synology Container Manager, import the Compose file as a project. The prebuilt image supports AMD64 and ARM64; the private source repository does not need to be shared with testers.

Open `http://SERVER:8080`. Create the administrator account using the ownership code from `docker exec docktun cat /app/data/setup.token`, or supply ADMIN_USERNAME and ADMIN_PASSWORD as environment variables before starting DockTun. Keep the ownership code private.

There is no install/setup shell script or provider onboarding. Docker inventory is available immediately after sign-in. DockTun creates unused provider companions in a stopped state, downloading images on first use if necessary. Enable retries creation if a download or mount was unavailable.

## Independent exposure methods

Choose a container's exposure in Containers → Access. None, Cloudflare, Tailscale, and WireGuard are choices; apps can use different methods simultaneously.

- Cloudflare: save a connector token in Exposure providers → Cloudflare, or supply a local YAML tunnel and credentials file in the shared Cloudflare configuration mount, then Enable. Cloudflare API credentials for DNS and Access are separate from connector credentials.
- Tailscale: connect each app in its Access tab. Each has a separate device identity and forwards only the selected TCP port. The shared legacy companion may remain stopped.
- WireGuard: use an already configured VPN address for an app, or configure the stopped WireGuard companion using its persistent config directory before enabling it. The host must support WireGuard; no host kernel module is installed automatically.

Enabling or disabling one provider does not stop another. Existing provider containers retain their mounts and running state. Use the mounted Docker socket only on a trusted system.

## Persistence and updates

The Compose file mounts app data, Cloudflare configuration, and WireGuard configuration. Provider creation resolves the actual Docker host mounts, including Compose-prefixed volumes and Synology paths. Do not substitute container-only paths such as `/app/data/tailscale` for a host bind source.

Update through Settings → Update DockTun. Back up persistent volumes before upgrading. Keep older provider state when migrating; existing containers are reused.
