# Integration checks

All scripts create uniquely named fixtures and remove them afterward. Build the
current image first and set `DOCKTUN_TEST_IMAGE` to its local tag.

```sh
docker build -t docktun:test .
export DOCKTUN_TEST_IMAGE=docktun:test
python3 tests/container_smoke.py
python3 tests/providers_integration.py
python3 tests/cloudflare_token_integration.py
DOCKTUN_TEST_ENGINE_IMAGE=docker:20.10-dind python3 tests/providers_integration.py
DOCKTUN_TEST_NETWORK=docktun.test python3 tests/providers_integration.py
python3 tests/stack_deletion_integration.py
python3 tests/transfers_update_integration.py
python3 tests/install_script_test.py
```

- `container_smoke.py`: ownership, authentication and restart persistence without
  the Docker socket. Uses local port 18080; run instances sequentially.
- `providers_integration.py`: disposable Docker-in-Docker engine; providers stage
  stopped, start independently, keep credentials private, use the intended network
  and persist state. Idle provider fixtures verify lifecycle and mounts, not live
  Cloudflare connectivity, a real tailnet or WireGuard kernel routing. Engine
  20.10 and 29 checks cover older Docker API compatibility relevant to Synology;
  they do not replace testing on physical DSM hardware.
- `stack_deletion_integration.py`: real Compose stack whose files are inaccessible
  to DockTun; preserves volumes, external networks and neighboring stacks.
- `transfers_update_integration.py`: binary uploads/downloads, web updates and
  rollback. Mounts the Docker socket only in its disposable app; targets only its
  uniquely named fixtures. Never point it at a production DockTun instance.
- `install_script_test.py`: installer runs inside a socket-free container with a
  fake Docker CLI. Never installs Docker or changes host services.

For AMD64/ARM64 release builds:

```sh
docker buildx build --platform linux/amd64,linux/arm64 --provenance=false --load -t docktun:test .
DOCKER_DEFAULT_PLATFORM=linux/amd64 python3 tests/container_smoke.py
DOCKER_DEFAULT_PLATFORM=linux/arm64 python3 tests/container_smoke.py
```

ARM64 execution on an AMD64 Docker Desktop host uses emulation. A passing build
and smoke test is not a physical ARM64 or Synology kernel test.

- `cloudflare_token_integration.py`: full app against a TLS Cloudflare API fixture
  and signed-in Tailscale on disposable local Headscale. Verifies connector-token
  identity overrides stale YAML, API credentials/status, remote route and DNS
  updates, preservation of other/path/catch-all rules and global settings,
  migration from an absent legacy TCP rule, permission failures, and local-YAML
  compatibility. It does not use a real Cloudflare account or claim live edge
  reachability. `DOCKTUN_KEEP_TEST_ENGINE=1` retains a successful fixture for UI QA.

## Audit integrity acceptance

With `DOCKTUN_TEST_IMAGE` set to the built image:

```sh
python3 tests/auth_restore_integration.py
python3 tests/compose_integration.py
python3 tests/application_update_integration.py
python3 tests/transfers_update_integration.py
```

The application-update scenario uses a disposable PostgreSQL 17 database and
HTTP app, a loopback-only registry on the Docker engine, two networks and named
volumes. It verifies application login/database rows after both updates and
failed-image rollback. It does not migrate database major versions. It only
controls its uniquely named fixtures and cleans them up afterward. The core
update scenario independently verifies login, data, aliases, anonymous volumes,
non-default ports and rollback. Run the listed scripts serially per job.


## Roadmap acceptance

- `historical_upgrade_integration.py` upgrades a genuine previously published image
  and checks changed-password login, data and new-session restart continuity.
- `recovery_integration.py` uses only unique fixtures: refuses live-writer volume
  capture, verifies stop/resume, encryption/tamper/wrong-key rejection, native
  PostgreSQL 17/MySQL 8.4/MariaDB 11.4 exports and empty-target restores, a new
  application volume and clean DockTun-instance login/review mode, one-use native
  download authorization and exact support-preview checksum.
- `release_installer_test.py` verifies a standalone digest-pinned Compose asset,
  its metadata checksum and rejection of mutable/invalid release identity.
- Backend tests cover durable leases/restart acknowledgement, queue persistence,
  retry/superseding, stable app identity, safe tar extraction, authentication of
  every encryption frame, remote read-back/retention, and failed Access policies.
- `template_review_integration.py` checks the stack's real `.env`, literal values,
  dashboard-environment exclusion and storage/path safety in a nested engine.

Use `DOCKTUN_TEST_IMAGE` for the exact locally built candidate. Fixtures are not
live-account or physical NAS/ARM64 acceptance. Tests create and remove only their
own resources; retained preview engines from earlier work are not cleanup targets.

- `prebuilt_install_integration.py` installs the single-file candidate in an empty
  nested engine with no checkout, compiler or `.env`, then checks persistent login
  after Compose recreation. PR runs load the unpublished candidate locally; release
  promotion runs the actual digest-pinned downloadable asset.
