# Integration checks

All scripts create uniquely named fixtures and remove them afterward. Build the
current image first and set `DOCKTUN_TEST_IMAGE` to its local tag.

```sh
docker build -t docktun:test .
export DOCKTUN_TEST_IMAGE=docktun:test
python3 tests/container_smoke.py
python3 tests/providers_integration.py
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
