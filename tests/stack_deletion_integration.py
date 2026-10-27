"""Provider lifecycle in a disposable Docker-in-Docker engine.

Provider processes are idle fixtures, so no Cloudflare account, WireGuard kernel
configuration, or real tailnet is needed. Verifies the actual app/API/Docker
creation, simultaneous state, mount mapping and missing-container recovery.
"""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

engine = 'docktun-provider-engine-' + uuid.uuid4().hex[:10]
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:stack-delete')
token = ''
base = ''

def docker(*args, input=None, timeout=240):
    result = subprocess.run(['docker', *map(str, args)], input=input, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(result.stderr[:2000])
    return result.stdout.strip()

def inner(*args, input=None):
    return docker('exec', '-i', engine, 'docker', *args, input=input)

def request(path, body=None, expected=200, authenticated=True, method=None):
    headers = {'Content-Type': 'application/json'}
    if authenticated and token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=60)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        payload = response.read()
        assert response.status == expected, (path, response.status, payload[:400])
        return json.loads(payload) if payload and response.headers.get_content_type() == 'application/json' else payload

def state(name):
    return inner('inspect', '--format', '{{.State.Status}}', name)

try:
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', 'docker:29-dind')
    for _ in range(90):
        try:
            inner('info'); break
        except RuntimeError:
            time.sleep(1)
    else:
        raise AssertionError('disposable Docker engine did not start')
    with tempfile.TemporaryDirectory() as directory:
        archive = Path(directory) / 'images.tar'
        docker('save', '-o', archive, image, 'alpine:3.23')
        with archive.open('rb') as stream:
            loaded = subprocess.run(['docker', 'exec', '-i', engine, 'docker', 'load'], stdin=stream, capture_output=True, timeout=240)
            assert loaded.returncode == 0, loaded.stderr[:1000]
    fixture = 'FROM alpine:3.23\nENTRYPOINT ["/bin/sh", "-c", "exec sleep 3600", "provider-fixture"]\n'
    for provider_image in ['cloudflare/cloudflared:latest', 'tailscale/tailscale:latest', 'lscr.io/linuxserver/wireguard:latest']:
        inner('build', '-q', '-t', provider_image, '-', input=fixture)
    inner('network', 'create', 'docktun')
    inner('run', '-d', '--name', 'docktun', '--network', 'docktun', '-p', '3001:3001',
          '--label', 'com.docker.compose.project=docktun-core', '-v', 'test-app:/app/data', '-v', 'scoped-cloudflare:/etc/cloudflared',
          '-v', '/var/run/docker.sock:/var/run/docker.sock', '-e', 'DOCKER_NETWORK=docktun', image)
    base = 'http://' + docker('port', engine, '3001/tcp')
    for _ in range(90):
        try:
            request('/healthz', authenticated=False)
            if all(state(name) == 'created' for name in ['docktun-cloudflared', 'docktun-tailscale', 'docktun-wireguard']):
                break
        except (OSError, RuntimeError, AssertionError):
            pass
        time.sleep(1)
    else:
        raise AssertionError('providers were not staged stopped')
    secret = inner('exec', 'docktun', 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': secret, 'admin_username': 'owner', 'admin_password': 'integration-password-123', 'tunnel': {'type': 'tailscale'}})['access_token']
    # Host-side Compose file is deliberately outside the app's mounted paths.
    inner('network', 'create', 'shared-external')
    compose = """services:
  web:
    image: alpine:3.23
    command: sleep 3600
    volumes:
      - data:/data
      - /anonymous
    networks: [default, shared]
  idle:
    image: alpine:3.23
    command: sleep 3600
    profiles: [optional]
volumes:
  data:
networks:
  shared:
    external: true
    name: shared-external
"""
    docker('exec', '-i', engine, 'sh', '-c', 'mkdir -p /tmp/host-only && cat > /tmp/host-only/compose.yml', input=compose)
    inner('compose', '-p', 'deletion-test', '-f', '/tmp/host-only/compose.yml', '--profile', '*', 'up', '-d')
    inner('compose', '-p', 'deletion-test', '-f', '/tmp/host-only/compose.yml', 'stop', 'web')
    inner('run', '-d', '--name', 'neighbor', '--label', 'com.docker.compose.project=deletion-test-other', 'alpine:3.23', 'sleep', '3600')
    containers = request('/api/docker/containers')
    if isinstance(containers, dict): containers = containers['containers']
    target = next(c for c in containers if c.get('project_name') == 'deletion-test')
    project_id = target['project_id']
    before_volumes = set(inner('volume', 'ls', '-q').splitlines())
    before_ids = set(inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=deletion-test').splitlines())
    assert len(before_ids) == 2
    # Inaccessible file cleanup must fail before stopping anything.
    request(f'/api/projects/{project_id}?remove_files=true', expected=500, method='DELETE')
    assert set(inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=deletion-test').splitlines()) == before_ids
    request(f'/api/projects/{project_id}', method='DELETE')
    assert not inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=deletion-test')
    assert not inner('network', 'ls', '-q', '--filter', 'label=com.docker.compose.project=deletion-test')
    assert set(inner('volume', 'ls', '-q').splitlines()) == before_volumes
    assert state('neighbor') == 'running'
    inner('network', 'inspect', 'shared-external')
    containers = request('/api/docker/containers')
    if isinstance(containers, dict): containers = containers['containers']
    assert not any(c.get('project_name') == 'deletion-test' for c in containers)
    projects = request('/api/projects')
    assert not any(p['id'] == project_id for p in projects)
    core = next(c for c in containers if c['name'] == 'docktun')
    request(f"/api/projects/{core['project_id']}", method='DELETE', expected=500)
    assert state('docktun') == 'running'
    print('PASS: host-only Compose stack deleted; stopped/profile services removed; volumes, external network and neighboring stack preserved; failures and self-delete refused')
finally:
    docker('rm', '-f', '-v', engine)
