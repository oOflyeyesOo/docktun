"""Service ownership, disabled-state preservation, rollback and core-last updates.

All containers/images live in a uniquely named disposable Docker engine.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

engine = 'docktun-service-test-' + uuid.uuid4().hex[:10]
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:service-manager')
base, token = '', ''
passed = False

def docker(*args, input=None):
    return subprocess.check_output(['docker', *map(str, args)], input=input, text=True, stderr=subprocess.STDOUT).strip()

def inner(*args, input=None):
    return docker('exec', '-i', engine, 'docker', *args, input=input)

def inspect(name):
    return json.loads(inner('inspect', name))[0]

def request(path, body=None, expected=200, auth=True):
    headers = {'Content-Type': 'application/json'}
    if token and auth:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    try:
        response = urllib.request.urlopen(req, timeout=45)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        data = response.read()
        assert response.status == expected, (path, response.status, data[:1000])
        return json.loads(data) if data else None

def eventually(check, attempts=120):
    last = None
    for _ in range(attempts):
        try:
            return check()
        except (OSError, AssertionError, subprocess.CalledProcessError) as error:
            last = error
            time.sleep(1)
    raise AssertionError(str(last))

def wait_job(phase):
    def check():
        job = request('/api/services')['job']
        if phase != 'failed' and job['phase'] == 'failed':
            raise RuntimeError(job)
        assert job['phase'] == phase, job
        return job
    return eventually(check, 240)

try:
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', 'docker:29-dind')
    eventually(lambda: inner('info'))
    with tempfile.TemporaryDirectory() as directory:
        archive = Path(directory) / 'images.tar'
        docker('save', '-o', archive, image, 'alpine:3.23')
        with archive.open('rb') as stream:
            subprocess.run(['docker', 'exec', '-i', engine, 'docker', 'load'], stdin=stream, stdout=subprocess.DEVNULL, check=True)
    inner('build', '-t', 'fixture-core:installed', '-', input=f'FROM {image}\nLABEL org.opencontainers.image.revision="' + 'a'*40 + '"\n')
    inner('build', '-t', 'fixture-core:candidate', '-', input=f'FROM {image}\nLABEL org.opencontainers.image.revision="' + 'b'*40 + '"\n')
    core_candidate = inner('image', 'inspect', '--format', '{{.Id}}', 'fixture-core:candidate')
    inner('build', '-t', 'fixture-helper:candidate', '-', input='FROM alpine:3.23\nLABEL test.release="next"\n')
    helper_candidate = inner('image', 'inspect', '--format', '{{.Id}}', 'fixture-helper:candidate')
    inner('build', '-t', 'fixture-helper:broken', '-', input='FROM alpine:3.23\nRUN mv /bin/busybox /bin/busybox.disabled\n')
    bad_candidate = inner('image', 'inspect', '--format', '{{.Id}}', 'fixture-helper:broken')
    inner('network', 'create', 'kept-network')
    inner('run', '--rm', '-v', 'data:/state', 'alpine:3.23', 'sh', '-c', 'touch /state/.initialized && chown -R 1001:1001 /state')
    socket_gid = docker('exec', engine, 'stat', '-c', '%g', '/var/run/docker.sock')
    core = inner('run', '-d', '--name', 'custom-dashboard', '--restart', 'unless-stopped', '--user', '1001:1001', '--group-add', socket_gid, '-p', '3001:3001', '--network', 'kept-network',
                 '-v', 'data:/app/data', '-v', '/var/run/docker.sock:/var/run/docker.sock', '-e', 'DOCKTUN_PREPARE_PROVIDERS=false',
                 '-e', 'DOCKTUN_UPDATE_IMAGE=' + core_candidate, 'fixture-core:installed')
    base = 'http://' + docker('port', engine, '3001/tcp').splitlines()[0]
    eventually(lambda: request('/api/setup/status'))
    secret = inner('exec', 'custom-dashboard', 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': secret, 'admin_username': 'owner', 'admin_password': 'fixture-password-123'})['access_token']
    for path in ['/api/services', '/api/services/check', '/api/services/update']:
        request(path, None if path == '/api/services' else {}, expected=401, auth=False)
    trap = "trap 'exit 0' TERM; while :; do sleep 1 & wait $!; done"
    def helper(name, kind, source, running=True, extra=()):
        args = ['run', '-d'] if running else ['create']
        return inner(*args, '--name', name, '--restart', 'unless-stopped', '--network', 'kept-network', '--network-alias', name + '-alias',
                     '--label', 'docktun.provider=' + kind, '--label', 'io.docktun.update-image=' + source,
                     '-v', name + '-state:/state', *extra, 'alpine:3.23', 'sh', '-c', trap)
    broken = helper('docktun-tailscale', 'tailscale', bad_candidate)
    neighbor = inner('run', '-d', '--name', 'docktun-backup', 'alpine:3.23', 'sleep', '3600')
    assert {row['name'] for row in request('/api/services')['services']} == {'custom-dashboard', 'docktun-tailscale'}
    request('/api/services/update', {'ids': [neighbor]}, expected=409)
    request('/api/services/update', {}, expected=409)
    request('/api/services/update', {'ids': [broken]}, expected=202)
    failure = wait_job('failed')
    assert 'restored' in failure['message'], failure
    assert inspect('docktun-tailscale')['Id'] == broken and inspect('docktun-tailscale')['State']['Running']
    assert inspect('custom-dashboard')['Id'] == core
    inner('rm', '-f', 'docktun-tailscale')
    cloud = helper('docktun-cloudflared', 'cloudflare', helper_candidate)
    wire = helper('docktun-wireguard', 'wireguard', helper_candidate, running=False)
    node_name = 'docktun-ts-' + hashlib.sha256(b'photos').hexdigest()[:20]
    node = inner('run', '-d', '--name', node_name, '--restart', 'unless-stopped', '--network', 'kept-network', '--label', 'io.docktun.tailscale-app=photos',
                 '--label', 'io.docktun.update-image=' + helper_candidate, '-v', 'device-identity:/state', 'alpine:3.23', 'sh', '-c', trap)
    inner('exec', node_name, 'sh', '-c', 'echo preserved-device-key > /state/identity')
    inner('tag', 'fixture-core:installed', 'ghcr.io/ooflyeyesoo/docktun:fixture')
    proxy = inner('run', '-d', '--name', 'docktun-zeroconf', '--restart', 'unless-stopped', '--network', 'host', '--entrypoint', '/app/zeroconf',
                  '-v', '/var/run/docker.sock:/var/run/docker.sock:ro', '-e', 'HTTP_PORT=18080', '-e', 'DOCKTUN_PORT=3001', 'ghcr.io/ooflyeyesoo/docktun:fixture')
    request('/api/services/check', {}, expected=202)
    wait_job('complete')
    rows = request('/api/services')['services']
    assert len(rows) == 5 and all(row['available'] for row in rows), rows
    before = {row['name']: inspect(row['name']) for row in rows}
    eventually(lambda: request('/api/services/update', {'ids': [row['id'] for row in rows]}, expected=202))
    request('/api/services/check', {}, expected=409)
    request('/api/self-update/check', {}, expected=409)
    job = wait_job('complete')
    updated = [row for row in job['services'] if row['result'] == 'updated']
    assert len(updated) == 5 and updated[-1]['kind'] == 'core', job
    rows = request('/api/services')['services']
    assert {row['name'] for row in rows} == set(before), rows
    for row in rows:
        old, new = before[row['name']], inspect(row['name'])
        assert old['Id'] != new['Id'], row
        assert new['State']['Running'] == old['State']['Running'], row
        assert [(m['Source'], m['Destination']) for m in new['Mounts']] == [(m['Source'], m['Destination']) for m in old['Mounts']], row
        assert set(new['NetworkSettings']['Networks']) == set(old['NetworkSettings']['Networks']), row
        assert not row['available'] and row['checked'], row
    stopped = inspect('docktun-wireguard')
    assert stopped['State']['Status'] == 'created' and stopped['State']['StartedAt'].startswith('0001-'), stopped['State']
    assert inner('exec', node_name, 'cat', '/state/identity') == 'preserved-device-key'
    assert 'docktun-cloudflared-alias' in inspect('docktun-cloudflared')['NetworkSettings']['Networks']['kept-network']['Aliases']
    assert inspect('docktun-backup')['Id'] == neighbor
    assert next(row for row in rows if row['kind'] == 'core')['version'] == 'b'*12
    assert inspect('custom-dashboard')['Config']['User'] == '1001:1001'
    assert inner('exec', 'custom-dashboard', 'stat', '-c', '%u:%g', '/app/data/service-updates.json') == '1001:1001'
    docker('restart', engine)
    base = 'http://' + docker('port', engine, '3001/tcp').splitlines()[0]
    eventually(lambda: request('/api/services'))
    stopped = inspect('docktun-wireguard')
    assert not stopped['State']['Running'] and stopped['State']['StartedAt'].startswith('0001-'), stopped['State']
    assert inner('exec', node_name, 'cat', '/state/identity') == 'preserved-device-key'
    print('PASS: authenticated ownership-scoped service updates; failed companion restored; healthy companions/core replaced; stopped provider never started; device state, mounts, aliases, networks and login preserved; core updated last; unrelated similarly named app untouched', flush=True)
    passed = True
    if os.environ.get('DOCKTUN_KEEP_TEST_ENGINE') == '1':
        Path('../../outputs/services-preview.json').write_text(json.dumps({'engine': engine, 'url': base, 'username': 'owner', 'password': 'fixture-password-123'}, indent=2))
except Exception:
    for name in ['custom-dashboard', 'docktun-cloudflared']:
        subprocess.run(['docker', 'exec', engine, 'docker', 'logs', '--tail', '15', name], check=False)
    raise
finally:
    if not passed or os.environ.get('DOCKTUN_KEEP_TEST_ENGINE') != '1':
        subprocess.run(['docker', 'rm', '-f', '-v', engine], stdout=subprocess.DEVNULL, check=False)
