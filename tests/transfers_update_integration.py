"""Disposable Docker tests: authenticated binary transfers and self-update/rollback.

Uses disposable local images; never updates an existing user's container.
DOCKTUN_TEST_IMAGE must contain the new updater implementation.
"""
import json
import os
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

prefix = 'docktun-transfer-test-' + uuid.uuid4().hex[:10]
app, registry, fixture = prefix + '-app', prefix + '-registry', prefix + '-files'
network, data, files = prefix + '-net', prefix + '-data', prefix + '-volume'
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:reconciled')
containers, volumes, images = [], [], []
base, token = '', ''

def docker(*args, input=None):
    return subprocess.check_output(['docker', *args], input=input, text=True).strip()

def request(path, body=None, expected=200, raw=False, headers=None, authenticated=True):
    h = dict(headers or {})
    if authenticated and token:
        h['Authorization'] = 'Bearer ' + token
    if body is not None and not isinstance(body, bytes):
        body = json.dumps(body).encode(); h['Content-Type'] = 'application/json'
    req = urllib.request.Request(base + path, data=body, headers=h)
    try:
        response = urllib.request.urlopen(req, timeout=180)
    except urllib.error.HTTPError as e:
        response = e
    with response:
        payload = response.read()
        assert response.status == expected, (path.split('?')[0], response.status, payload[:500])
        return payload if raw else json.loads(payload) if payload else None

def ready():
    global base
    for _ in range(90):
        try:
            port = docker('port', app, '3001/tcp').splitlines()[0]
            base = 'http://' + port
            request('/healthz', raw=True)
            return
        except (OSError, AssertionError, subprocess.CalledProcessError):
            time.sleep(1)
    raise AssertionError('test app did not become ready')

def upload(kind, ident, directory, filename, payload, expected=200):
    boundary = 'docktun-' + uuid.uuid4().hex
    fields = {'target_type': kind, 'target_id': ident, 'dir_path': directory}
    body = bytearray()
    for key, value in fields.items():
        body.extend((f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n').encode())
    escaped = filename.replace('\\', '\\\\').replace('"', '\\"')
    body.extend((f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{escaped}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode())
    body.extend(payload); body.extend((f'\r\n--{boundary}--\r\n').encode())
    return request('/api/storage/files/upload', bytes(body), expected=expected, headers={'Content-Type': 'multipart/form-data; boundary=' + boundary})

def download(kind, ident, path):
    return request('/api/storage/files/download?' + urllib.parse.urlencode({'target_type': kind, 'target_id': ident, 'path': path}), raw=True)

def wait_update(phase):
    global base
    for _ in range(150):
        try:
            port = docker('port', app, '3001/tcp').splitlines()[0]
            base = 'http://' + port
            state = request('/api/self-update')
            if state['phase'] == phase:
                return state
            if state['phase'] == 'failed':
                raise RuntimeError(state['message'])
        except (OSError, AssertionError, subprocess.CalledProcessError):
            pass
        time.sleep(1)
    raise AssertionError('update did not reach ' + phase)

try:
    docker('network', 'create', network)
    for volume in [data, files]:
        docker('volume', 'create', volume); volumes.append(volume)
    tag = prefix + ':candidate'
    images.append(tag)
    docker('build', '-t', tag, '-', input=f'FROM {image}\nLABEL test.release="candidate"\n')
    candidate_id = docker('image', 'inspect', '--format', '{{.Id}}', tag)
    containers.append(fixture)
    docker('run', '-d', '--name', fixture, '--network', network, 'alpine:3.23', 'sleep', '600')
    containers.append(app)
    docker('run', '-d', '--name', app, '--network', network, '--network-alias', 'kept-alias',
           '-p', '127.0.0.1::3001', '-v', data + ':/app/data', '-v', '/extra',
           '-v', '/var/run/docker.sock:/var/run/docker.sock',
           '-e', 'DOCKTUN_UPDATE_IMAGE=' + candidate_id, '-e', 'DOCKER_NETWORK=' + network,
           '-e', 'LITERAL_VALUE=spaces $HOME $(never-execute) "quoted"', image)
    ready()
    before = json.loads(docker('inspect', app))[0]
    anonymous = next(m['Name'] for m in before['Mounts'] if m['Destination'] == '/extra')
    volumes.append(anonymous)
    setup_token = docker('exec', app, 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': setup_token, 'admin_username': 'tester', 'admin_password': 'integration-password-123', 'tunnel': {'type': 'none'}})['access_token']
    request('/api/self-update', expected=401, authenticated=False)
    request('/api/self-update?token=' + token, expected=401, authenticated=False)
    payload = bytes(range(256)) * 4096
    filename = 'binary " $(touch injected) file.bin'
    for kind, ident, directory in [('docktun', 'app_data', 'uploads'), ('volume', files, 'nested'), ('container', fixture, 'tmp')]:
        upload(kind, ident, directory, filename, payload)
        assert download(kind, ident, directory + '/' + filename) == payload
        upload(kind, ident, directory, 'empty.bin', b'')
        assert download(kind, ident, directory + '/empty.bin') == b''
    docker('stop', fixture)
    upload('container', fixture, 'tmp', 'stopped.bin', payload)
    assert download('container', fixture, 'tmp/stopped.bin') == payload
    upload('docktun', 'app_data', '../escape', 'bad', b'bad', expected=500)
    upload('docktun', 'app_data', 'uploads', 'too-large.bin', b'x' * (64 * 1024 * 1024 + 1), expected=413)
    print('Transfers passed: binary/empty files, volume/local/container, stopped container, filename quoting, auth and limits', flush=True)
    checked = request('/api/self-update/check', {})
    assert checked['available'], checked
    request('/api/self-update/install', {}, expected=202)
    complete = wait_update('complete')
    after = json.loads(docker('inspect', app))[0]
    assert before['Id'] != after['Id']
    assert after['HostConfig']['PortBindings'] == before['HostConfig']['PortBindings']
    assert next(m['Name'] for m in after['Mounts'] if m['Destination'] == '/extra') == anonymous
    assert 'kept-alias' in after['NetworkSettings']['Networks'][network]['Aliases']
    assert 'LITERAL_VALUE=spaces $HOME $(never-execute) "quoted"' in after['Config']['Env']
    assert download('docktun', 'app_data', 'uploads/' + filename) == payload
    assert not request('/api/self-update/check', {})['available']
    print('Self-update passed: replacement, port/network/alias/anonymous volume/env preservation, token and data persistence', flush=True)
    # Publish an image whose entrypoint is missing; updater must restore the previous container.
    docker('build', '-t', tag, '-', input=f'FROM {image}\nRUN mv /app/server /app/server.disabled\n')
    bad_id = docker('image', 'inspect', '--format', '{{.Id}}', tag)
    helper = prefix + '-rollback-helper'; containers.append(helper)
    docker('run', '-d', '--name', helper, '--network', 'none', '--volumes-from', app, '--entrypoint', '/app/server', image, 'update-worker', after['Id'], bad_id, '/app/data/self-update.json')
    wait_update('rolled_back')
    restored = json.loads(docker('inspect', app))[0]
    assert restored['Id'] == after['Id']
    assert download('docktun', 'app_data', 'uploads/' + filename) == payload
    print('Rollback passed: failed replacement restores previous healthy container with data and login intact', flush=True)
finally:
    # Only remove uniquely named fixtures and helpers explicitly targeting their IDs.
    ids = []
    for name in containers:
        try:
            ids.append(docker('inspect', '--format', '{{.Id}}', name))
        except subprocess.CalledProcessError:
            pass
    for ident in ids:
        helpers = docker('ps', '-aq', '--filter', 'label=docktun.update.target=' + ident).split()
        for helper in helpers:
            subprocess.run(['docker', 'rm', '-f', helper], stdout=subprocess.DEVNULL)
    for name in containers:
        subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL)
    for volume in volumes:
        subprocess.run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL)
    subprocess.run(['docker', 'network', 'rm', network], stdout=subprocess.DEVNULL)
    for tag in images:
        subprocess.run(['docker', 'image', 'rm', tag], stdout=subprocess.DEVNULL)
