"""Local proxy web controls and specifically bound ports in a disposable engine."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

engine = 'docktun-local-test-' + uuid.uuid4().hex[:10]
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:local-control-fix')
token = ''

def docker(*args):
    return subprocess.check_output(['docker', *map(str, args)], text=True, stderr=subprocess.STDOUT).strip()

def inner(*args):
    return docker('exec', engine, 'docker', *args)

def request(path, body=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    with urllib.request.urlopen(req, timeout=45) as response:
        data = response.read()
        return json.loads(data) if data else None

def eventually(check):
    last_error = None
    for _ in range(60):
        try:
            return check()
        except (OSError, AssertionError, subprocess.CalledProcessError) as error:
            last_error = error
            time.sleep(1)
    raise AssertionError(str(last_error))

def proxy_health(host):
    return inner('exec', 'docktun-zeroconf', 'curl', '-fsS', '-H', 'Host: ' + host, 'http://127.0.0.1/healthz')

try:
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::8080', 'docker:29-dind')
    eventually(lambda: inner('info'))
    with tempfile.TemporaryDirectory() as directory:
        archive = Path(directory) / 'app.tar'
        docker('save', '-o', archive, image)
        with archive.open('rb') as stream:
            subprocess.run(['docker', 'exec', '-i', engine, 'docker', 'load'], stdin=stream, stdout=subprocess.DEVNULL, check=True)
    inner('tag', image, 'ghcr.io/ooflyeyesoo/docktun:latest')
    inner('run', '-d', '--name', 'docktun', '-p', '8080:3001', '-v', 'data:/app/data', '-v', '/var/run/docker.sock:/var/run/docker.sock', '-e', 'DOCKTUN_PREPARE_PROVIDERS=false', image)
    base = 'http://' + docker('port', engine, '8080/tcp').splitlines()[0]
    eventually(lambda: request('/api/setup/status'))
    setup = inner('exec', 'docktun', 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': setup, 'admin_username': 'owner', 'admin_password': 'fixture-password-123'})['access_token']
    request('/api/remote-access/zeroconf/control', {'action': 'start'})
    assert inner('inspect', '--format', '{{.Path}}', 'docktun-zeroconf') == '/app/zeroconf', 'web control started the core server instead of the proxy'
    eventually(lambda: proxy_health('docktun.local'))
    assert request('/api/remote-access/overview')['zeroconf']['running']
    request('/api/remote-access/zeroconf/control', {'action': 'stop'})
    assert inner('inspect', '--format', '{{.State.Running}}', 'docktun-zeroconf') == 'false'
    request('/api/remote-access/zeroconf/control', {'action': 'start'})
    eventually(lambda: proxy_health('docktun.local'))
    request('/api/remote-access/zeroconf/control', {'action': 'restart'})
    eventually(lambda: proxy_health('docktun.local'))
    # This server is deliberately reachable on 127.0.0.2, not 127.0.0.1.
    inner('run', '-d', '--name', 'bound-app', '-p', '127.0.0.2:18081:3001', '-e', 'DOCKTUN_PREPARE_PROVIDERS=false', image)
    eventually(lambda: proxy_health('bound-app.local'))
    assert set(inner('ps', '--format', '{{.Names}}').splitlines()) == {'docktun', 'docktun-zeroconf', 'bound-app'}, 'optional tunnel provider started unexpectedly'
    print('PASS: web start/stop/restart launch actual zeroconf; dashboard and specifically bound app respond through portless proxy; optional tunnel providers stay absent')
except Exception:
    for name in ['docktun', 'docktun-zeroconf']:
        subprocess.run(['docker', 'exec', engine, 'docker', 'logs', '--tail', '12', name], check=False)
    raise
finally:
    subprocess.run(['docker', 'rm', '-f', '-v', engine], stdout=subprocess.DEVNULL, check=False)
