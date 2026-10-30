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


def wait_project_action(project_id):
    for _ in range(60):
        output = request(f'/api/projects/{project_id}/output')
        if output['done']:
            assert not any('[ERROR]' in line for line in output['lines']), output
            return output
        time.sleep(1)
    raise AssertionError('stack action did not complete')


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
    # Synology-style external Compose labels contain daemon-host paths. The
    # same directory is deliberately mounted under a different name in DockTun.
    synology_compose = '''services:
  app:
    image: alpine:3.23
    command: ["sleep", "3600"]
    environment:
      STACK_REVISION: "${STACK_REVISION:-initial}"
    volumes:
      - ./content:/content:ro
'''
    docker('exec', '-i', engine, 'sh', '-c',
           'mkdir -p /volume1/docker/MediaStorm/content && cat > /volume1/docker/MediaStorm/docker-compose.yml', input=synology_compose)
    docker('exec', engine, 'sh', '-c',
           'printf synology-host-marker > /volume1/docker/MediaStorm/content/marker')
    inner('compose', '-p', 'mediastorm', '-f', '/volume1/docker/MediaStorm/docker-compose.yml', 'up', '-d')
    docker('exec', '-i', engine, 'sh', '-c',
           'mkdir -p /volume2/docker/Unmapped && cat > /volume2/docker/Unmapped/compose.yml',
           input='services:\n  app:\n    image: alpine:3.23\n    command: ["sleep", "3600"]\n')
    inner('compose', '-p', 'unmapped-external', '-f', '/volume2/docker/Unmapped/compose.yml', 'up', '-d')
    inner('network', 'create', 'docktun')
    inner('run', '-d', '--name', 'docktun', '--network', 'docktun', '-p', '3001:3001',
          '--label', 'com.docker.compose.project=docktun-core', '--label', 'com.docker.compose.project.working_dir=/opt/core', '-v', 'test-app:/app/data', '-v', 'test-projects:/opt/projects', '-v', 'scoped-cloudflare:/etc/cloudflared',
          '-v', '/volume1/docker:/mounted-stacks',
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
    containers = request('/api/docker/containers')
    if isinstance(containers, dict): containers = containers['containers']
    synology = next(c for c in containers if c.get('project_name') == 'mediastorm')
    synology_id = synology['project_id']
    assert synology['project_path'] == '/volume1/docker/MediaStorm', synology
    request(f'/api/projects/{synology_id}/file-access', expected=401, authenticated=False)
    access = request(f'/api/projects/{synology_id}/file-access')
    assert access['available'] and access['writable'] and access['resolved_path'] == '/mounted-stacks/MediaStorm', access
    loaded = request(f'/api/projects/{synology_id}/compose')
    # subprocess text stdin uses CRLF on Windows; the app preserves the host
    # file's line endings while JSON decoding intentionally leaves them intact.
    assert loaded['filename'] == 'docker-compose.yml' and loaded['content'].replace('\r\n', '\n') == synology_compose, loaded
    assert request(f'/api/projects/{synology_id}/env')['content'] == ''
    files = request(f'/api/projects/{synology_id}/files?path=content')
    assert any(item['name'] == 'marker' for item in files), files
    edited = synology_compose + '# saved through the mapped stack folder\n'
    request(f'/api/projects/{synology_id}/compose', {'filename': loaded['filename'], 'content': edited}, method='PUT')
    request(f'/api/projects/{synology_id}/env', {'content': 'STACK_REVISION=updated\n'}, method='PUT')
    assert docker('exec', engine, 'cat', '/volume1/docker/MediaStorm/docker-compose.yml') == edited.strip()
    assert docker('exec', engine, 'cat', '/volume1/docker/MediaStorm/.env') == 'STACK_REVISION=updated'
    request(f'/api/projects/{synology_id}/up', {})
    wait_project_action(synology_id)
    synology_app = inner('ps', '-q', '--filter', 'label=com.docker.compose.project=mediastorm', '--filter', 'label=com.docker.compose.service=app')
    assert synology_app and synology_app != synology['id'], (synology_app, synology)
    assert inner('exec', synology_app, 'cat', '/content/marker') == 'synology-host-marker'
    assert inner('exec', synology_app, 'printenv', 'STACK_REVISION') == 'updated'
    synology_mount = next(m for m in json.loads(inner('inspect', synology_app))[0]['Mounts'] if m['Destination'] == '/content')
    assert synology_mount['Source'] == '/volume1/docker/MediaStorm/content' and not synology_mount['RW'], synology_mount
    assert next(p for p in request('/api/projects') if p['id'] == synology_id)['path'] == '/volume1/docker/MediaStorm'
    assert not request(f'/api/projects/{synology_id}/delete-options')['can_remove_files']
    inner('exec', 'docktun', 'sh', '-c', 'test ! -e /volume1/docker/MediaStorm')
    assert './content:/content:ro' in docker('exec', engine, 'cat', '/volume1/docker/MediaStorm/docker-compose.yml')

    # An unmounted neighbor stays manageable as a container; file operations
    # explain the missing mount and never manufacture a shadow host directory.
    unmapped = next(c for c in containers if c.get('project_name') == 'unmapped-external')
    unmapped_id = unmapped['project_id']
    access = request(f'/api/projects/{unmapped_id}/file-access')
    assert not access['available'] and not access['writable'] and 'mount that host folder' in access['reason'], access
    for route in ['compose', 'env', 'files']:
        failure = request(f'/api/projects/{unmapped_id}/{route}', expected=500)
        assert 'mount that host folder' in failure['error'], failure
    failure = request(f'/api/projects/{unmapped_id}/compose', {'filename': 'compose.yml', 'content': 'services: {}\n'}, expected=500, method='PUT')
    assert 'mount that host folder' in failure['error'], failure
    failure = request(f'/api/projects/{unmapped_id}/env', {'content': 'SHOULD_NOT_EXIST=yes\n'}, expected=500, method='PUT')
    assert 'mount that host folder' in failure['error'], failure
    inner('exec', 'docktun', 'sh', '-c', 'test ! -e /volume2/docker/Unmapped')
    request('/api/docker/containers/' + unmapped['id'] + '/stop', {})
    assert state(unmapped['id']) == 'exited'
    request('/api/docker/containers/' + unmapped['id'] + '/start', {})
    assert state(unmapped['id']) == 'running'
    assert docker('exec', engine, 'cat', '/volume2/docker/Unmapped/compose.yml') == 'services:\n  app:\n    image: alpine:3.23\n    command: ["sleep", "3600"]'

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
    print('PASS: Synology host-label stack read, saved and redeployed through a differently named mount; relative bind data preserved; unmounted neighbor file access refused without shadow folders while container controls worked; managed stack deployed, updated and persisted with shared bind/secret/config data; host-only Compose stack deleted; stopped/profile services removed; volumes, external network and neighboring stack preserved; failures and self-delete refused')
finally:
    docker('rm', '-f', '-v', engine)
