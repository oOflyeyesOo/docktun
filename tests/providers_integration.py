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
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:providers')
network = os.environ.get('DOCKTUN_TEST_NETWORK', 'docktun')
token = ''
base = ''

def docker(*args, input=None, timeout=240):
    result = subprocess.run(['docker', *map(str, args)], input=input, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(result.stderr[:2000])
    return result.stdout.strip()

def inner(*args, input=None):
    return docker('exec', '-i', engine, 'docker', *args, input=input)

def request(path, body=None, expected=200, authenticated=True):
    headers = {'Content-Type': 'application/json'}
    if authenticated and token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
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
    inner('network', 'create', network)
    if '.' in network:
        inner('network', 'create', network.replace('.', 'X'))
    inner('run', '-d', '--name', 'docktun', '--network', network, '-p', '3001:3001',
          '-v', 'test-app:/app/data', '-v', 'scoped-cloudflare:/etc/cloudflared',
          '-v', '/var/run/docker.sock:/var/run/docker.sock', '-e', 'DOCKER_NETWORK=' + network, image)
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
    request('/api/provider/cloudflare/credentials', expected=401, authenticated=False)
    request('/api/control/start', {}, expected=500)  # Missing credentials must leave it stopped.
    assert state('docktun-cloudflared') == 'created'
    connector = 'test-connector-private-value'
    req = urllib.request.Request(base + '/api/provider/cloudflare/credentials', data=json.dumps({'token': connector}).encode(), method='PUT', headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req) as response:
        payload = response.read()
        assert connector.encode() not in payload and json.loads(payload)['has_token']
    request('/api/remote-access/tailscale/control', {'action': 'start'})
    request('/api/control/start', {})
    request('/api/remote-access/wireguard/control', {'action': 'start'})
    for name in ['docktun-cloudflared', 'docktun-tailscale', 'docktun-wireguard']:
        assert state(name) == 'running', name
        attached = json.loads(inner('inspect', name))[0]['NetworkSettings']['Networks']
        assert network in attached, attached
        assert attached[network]['NetworkID'] == json.loads(inner('network', 'inspect', network))[0]['Id']
    started_at = inner('inspect', '--format', '{{.State.StartedAt}}', 'docktun-cloudflared')
    request('/api/docker/network/reconcile', {})
    assert inner('inspect', '--format', '{{.State.StartedAt}}', 'docktun-cloudflared') == started_at
    assert inner('exec', 'docktun-cloudflared', 'cat', '/etc/cloudflared/tunnel.token') == connector
    inner('exec', 'docktun-wireguard', 'sh', '-c', 'echo persisted > /config/test-marker')
    assert inner('exec', 'docktun', 'cat', '/app/data/wireguard/test-marker') == 'persisted'
    cloudflare = json.loads(inner('inspect', 'docktun-cloudflared'))[0]
    assert connector not in json.dumps(cloudflare['Config']['Env'])
    assert connector not in json.dumps(cloudflare['Config']['Cmd'])
    mount = next(m for m in cloudflare['Mounts'] if m['Destination'] == '/etc/cloudflared')
    assert mount['Type'] == 'volume' and mount['Name'] == 'scoped-cloudflare' and not mount['RW']
    connector_id = inner('inspect', '--format', '{{.Id}}', 'docktun-cloudflared')
    assert inner('exec', 'docktun', 'stat', '-c', '%a', '/etc/cloudflared/tunnel.token') == '600'
    request('/api/control/stop', {})
    request('/api/control/start', {})
    assert inner('inspect', '--format', '{{.Id}}', 'docktun-cloudflared') == connector_id
    request('/api/control/stop', {})
    assert state('docktun-tailscale') == 'running' and state('docktun-wireguard') == 'running'
    inner('restart', 'docktun')
    for _ in range(60):
        try:
            request('/healthz'); break
        except OSError:
            time.sleep(1)
    assert state('docktun-cloudflared') == 'exited'
    assert state('docktun-tailscale') == 'running' and state('docktun-wireguard') == 'running'
    inner('rm', 'docktun-cloudflared')
    request('/api/control/restart', {})
    assert state('docktun-cloudflared') == 'running'
    assert request('/api/status')['running'] is True
    print('Providers passed: prepared stopped, all three independently running, credentials required, exact named/bind mounts, no token disclosure, restart persistence, missing Cloudflare recreated')
finally:
    subprocess.run(['docker', 'rm', '-f', '-v', engine], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
