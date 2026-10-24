"""Run against prebuilt Compose using an isolated project and disposable state.
Set DOCKTUN_TEST_IMAGE to test a candidate (defaults to the published image).
The fixed Tailscale container name must be unused; never touches an existing one.
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

root = Path(__file__).resolve().parents[1]
project = 'docktun-tailscale-test-' + uuid.uuid4().hex[:10]
state_volume = project + '-state'
tailscale = 'docktun-tailscale'

def docker(*args):
    return subprocess.check_output(['docker', *map(str, args)], text=True).strip()

if tailscale in docker('ps', '-a', '--format', '{{.Names}}').splitlines():
    raise RuntimeError('docktun-tailscale already exists; run this integration test on an isolated Docker engine')

config = json.loads(docker('compose', '-f', root / 'compose.prebuilt.yml', 'config', '--format', 'json'))
config.pop('name', None)
app = config['services']['docktun']
app['container_name'] = project + '-app'
app['image'] = os.environ.get('DOCKTUN_TEST_IMAGE', app['image'])
app['ports'] = ['127.0.0.1::3001']
app['environment']['TAILSCALE_STATE_VOLUME'] = state_volume
app['environment']['TAILSCALE_NETWORK'] = project + '-network'
for key, volume in config['volumes'].items():
    volume['name'] = project + '-' + key
for key, network in config['networks'].items():
    network['name'] = project + '-network'

with tempfile.TemporaryDirectory() as directory:
    compose_file = Path(directory) / 'compose.json'
    compose_file.write_text(json.dumps(config))
    def compose(*args):
        return docker('compose', '-p', project, '-f', compose_file, *args)
    created_tailscale = False
    try:
        compose('up', '-d', '--wait', '--wait-timeout', '90')
        base = 'http://' + compose('port', 'docktun', '3001')
        def request(path, body=None, token=None):
            headers = {'Content-Type': 'application/json'}
            if token:
                headers['Authorization'] = 'Bearer ' + token
            req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
            with urllib.request.urlopen(req, timeout=150) as response:
                return json.load(response)
        secret = docker('exec', app['container_name'], 'cat', '/app/data/setup.token')
        token = request('/api/setup/complete', {'setup_token': secret, 'admin_username': 'tester', 'admin_password': 'tester-password-12345', 'tunnel': {'type': 'tailscale'}})['access_token']
        details = request('/api/setup/details', token=token)
        assert details['tunnel_type'] == 'tailscale', details
        assert request('/api/status', token=token)['status'] == 'disabled'
        request('/api/remote-access/tailscale/control', {'action': 'start'}, token)
        created_tailscale = True
        mounts = json.loads(docker('inspect', tailscale))[0]['Mounts']
        mount = next(m for m in mounts if m['Destination'] == '/var/lib/tailscale')
        assert mount['Type'] == 'volume' and mount['Name'] == state_volume, mount
        for _ in range(30):
            status = request('/api/remote-access/tailscale/status', token=token)
            if status['backend_state'] == 'NeedsLogin':
                break
            time.sleep(1)
        else:
            raise AssertionError(status)
        docker('exec', tailscale, 'sh', '-c', 'echo persisted > /var/lib/tailscale/docktun-test-marker')
        compose('restart', 'docktun')
        base = 'http://' + compose('port', 'docktun', '3001')
        for _ in range(30):
            try:
                assert request('/api/setup/details', token=token)['tunnel_type'] == 'tailscale'
                break
            except (OSError, AssertionError):
                time.sleep(1)
        else:
            raise AssertionError('provider did not survive restart')
        original = docker('inspect', '--format', '{{.Id}}', tailscale)
        request('/api/remote-access/tailscale/control', {'action': 'start'}, token)
        assert docker('inspect', '--format', '{{.Id}}', tailscale) == original
        docker('rm', '-f', tailscale)
        request('/api/remote-access/tailscale/control', {'action': 'start'}, token)
        assert docker('exec', tailscale, 'cat', '/var/lib/tailscale/docktun-test-marker') == 'persisted'
        print('Prebuilt Tailscale integration passed: provider selection, no Cloudflare requirement, named mount, userspace daemon, reuse, restart, recreation persistence')
    finally:
        # The name was absent before the test. Clean a partially started container too.
        if created_tailscale or tailscale in docker('ps', '-a', '--format', '{{.Names}}').splitlines():
            subprocess.run(['docker', 'rm', '-f', tailscale], check=True)
        compose('down', '-v')
        subprocess.run(['docker', 'volume', 'rm', state_volume], check=False)

