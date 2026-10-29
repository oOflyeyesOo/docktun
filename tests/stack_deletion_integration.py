"""Stack creation, persistence and deletion in a disposable Docker engine.

Uses real Compose projects plus idle provider fixtures; never changes user stacks.
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
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', os.environ.get('DOCKTUN_TEST_ENGINE_IMAGE', 'docker:29-dind'))
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
          '--label', 'com.docker.compose.project=docktun-core', '-v', 'test-app:/app/data', '-v', 'test-projects:/opt/projects', '-v', 'scoped-cloudflare:/etc/cloudflared',
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
    # Projects created in the UI must use the mounted, persistent directory.
    managed_compose = 'services:\n  app:\n    image: alpine:3.23\n    command: ["sleep", "3600"]\n    volumes:\n      - ./content:/content:ro\n    secrets: [key]\n    configs:\n      - source: settings\n        target: /etc/app.conf\n  optional:\n    image: alpine:3.23\n    command: ["sleep", "3600"]\n    profiles: [optional]\nsecrets:\n  key:\n    file: ./content/key\nconfigs:\n  settings:\n    file: ./content/settings\n'
    created = request('/api/projects', {'name': 'managed-example', 'compose_content': managed_compose, 'auto_start': False}, expected=201)
    assert created['path'] == '/opt/projects/managed-example', created
    request(f"/api/projects/{created['id']}/delete-options", expected=401, authenticated=False)
    available = request(f"/api/projects/{created['id']}/delete-options")
    assert available['can_remove_files'] and available['path'] == created['path'], available

    inner('exec', 'docktun', 'mkdir', '-p', created['path'] + '/content')
    inner('exec', 'docktun', 'sh', '-c', 'printf shared-project-marker > /opt/projects/managed-example/content/marker')
    inner('exec', 'docktun', 'sh', '-c', 'printf shared-secret-marker > /opt/projects/managed-example/content/key; printf shared-config-marker > /opt/projects/managed-example/content/settings')
    request(f"/api/projects/{created['id']}/up", {})
    for _ in range(60):
        output = request(f"/api/projects/{created['id']}/output")
        if output['done']:
            assert not any('[ERROR]' in line for line in output['lines']), output
            break
        time.sleep(1)
    else:
        raise AssertionError('managed stack did not finish deploying')
    managed_id = inner('ps', '-q', '--filter', 'label=com.docker.compose.project=managed-example', '--filter', 'label=com.docker.compose.service=app')
    assert managed_id
    assert inner('exec', managed_id, 'cat', '/content/marker') == 'shared-project-marker'
    assert inner('exec', managed_id, 'cat', '/run/secrets/key') == 'shared-secret-marker'
    assert inner('exec', managed_id, 'cat', '/etc/app.conf') == 'shared-config-marker'
    mounts = json.loads(inner('inspect', managed_id))[0]['Mounts']
    for target in ['/content', '/run/secrets/key', '/etc/app.conf']:
        assert not next(m for m in mounts if m['Destination'] == target)['RW']
    saved = inner('exec', 'docktun', 'cat', created['path'] + '/docker-compose.yml')
    assert './content:/content:ro' in saved and '/var/lib/docker/volumes/' not in saved

    assert not inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=managed-example', '--filter', 'label=com.docker.compose.service=optional')
    request('/api/docker/containers/' + managed_id + '/update', {})
    updated_id = inner('ps', '-q', '--filter', 'label=com.docker.compose.project=managed-example', '--filter', 'label=com.docker.compose.service=app')
    assert updated_id and updated_id != managed_id
    assert inner('exec', updated_id, 'cat', '/content/marker') == 'shared-project-marker'
    assert inner('exec', updated_id, 'cat', '/run/secrets/key') == 'shared-secret-marker'
    assert inner('exec', updated_id, 'cat', '/etc/app.conf') == 'shared-config-marker'
    assert not inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=managed-example', '--filter', 'label=com.docker.compose.service=optional')

    inner('restart', 'docktun')
    for _ in range(60):
        try:
            request('/healthz'); break
        except OSError:
            time.sleep(1)
    inner('exec', 'docktun', 'test', '-f', created['path'] + '/docker-compose.yml')
    request(f"/api/projects/{created['id']}", method='DELETE')
    inner('exec', 'docktun', 'test', '-f', created['path'] + '/docker-compose.yml')
    request('/api/projects', {'name': 'unwritable-example', 'path': '/proc/docktun-unwritable', 'compose_content': 'services:\n  app:\n    image: alpine:3.23\n', 'auto_start': True}, expected=500)
    assert not inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=unwritable-example')
    launch = request('/api/docker/run', {'command': "docker run --name run-compose-example -v ./content:/content alpine:3.23 sh -c 'echo from-run > /content/marker; sleep 3600'", 'mode': 'compose'}, expected=202)
    for _ in range(90):
        output = request(f"/api/docker/run/output/{launch['job_id']}")
        if output['done']:
            assert output['status'] == 'completed', output
            break
        time.sleep(1)
    else:
        raise AssertionError('Docker Run conversion did not finish')
    assert inner('exec', 'docktun', 'cat', '/opt/projects/run-compose-example/content/marker') == 'from-run'
    request(f"/api/projects/{output['project_id']}", method='DELETE')
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
    unavailable = request(f'/api/projects/{project_id}/delete-options')
    assert not unavailable['can_remove_files'] and unavailable['reason'], unavailable

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
    print('PASS: managed stack deployed, updated and persisted with shared bind/secret/config data; file errors reported; host-only Compose stack deleted; stopped/profile services removed; volumes, external network and neighboring stack preserved; failures and self-delete refused')
finally:
    docker('rm', '-f', '-v', engine)
