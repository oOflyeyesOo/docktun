"""Disposable Compose integration: only controls containers created by this test."""
import json
import os
import pathlib
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

project = 'docktun-integration-' + uuid.uuid4().hex[:10]

def docker(*args):
    return subprocess.check_output(['docker', *args], text=True).strip()

with tempfile.TemporaryDirectory() as directory:
    compose_file = pathlib.Path(directory) / 'compose.json'
    compose_file.write_text(json.dumps({
        'services': {
            'app': {
                'image': os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:test'),
                'ports': ['127.0.0.1::3001'],
                'environment': {'DOCKTUN_PREPARE_PROVIDERS': 'false'},
                'volumes': ['data:/app/data', '/var/run/docker.sock:/var/run/docker.sock'],
                'healthcheck': {'test': ['CMD', 'curl', '-fsS', 'http://localhost:3001/healthz'], 'interval': '2s', 'timeout': '2s', 'retries': 30},
            },
            'fixture': {'image': 'alpine:3.23', 'command': ['sh', '-c', 'echo integration-fixture; sleep 600']},
        },
        'volumes': {'data': {}},
    }))
    def compose(*args):
        return docker('compose', '-p', project, '-f', str(compose_file), *args)
    try:
        compose('up', '-d', '--wait', '--wait-timeout', '90')
        base = 'http://' + compose('port', 'app', '3001')
        app = compose('ps', '-q', 'app')
        fixture = compose('ps', '-q', 'fixture')
        def request(path, body=None, token=None, expected=200):
            headers = {'Content-Type': 'application/json'}
            if token:
                headers['Authorization'] = 'Bearer ' + token
            req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
            try:
                response = urllib.request.urlopen(req, timeout=20)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                payload = response.read()
                assert response.code == expected, (path, response.code, payload)
                return json.loads(payload)
        request('/api/diagnostics', expected=401)
        secret = docker('exec', app, 'cat', '/app/data/setup.token')
        access = request('/api/setup/complete', {'setup_token': secret, 'admin_username': 'integration', 'admin_password': 'integration-password-123'})['access_token']
        overview = request('/api/remote-access/overview', token=access)
        assert isinstance(overview['cloudflare']['running'], bool), overview
        report = request('/api/diagnostics', token=access)
        assert len(report['checks']) == 2 and all(check['ok'] for check in report['checks']), report
        items = request('/api/docker/containers', token=access)
        assert any(item['id'] == fixture or fixture.startswith(item['id']) for item in items)
        detail = request('/api/docker/containers/' + fixture, token=access)
        assert detail['running'] is True
        logs = request('/api/docker/containers/' + fixture + '/logs', token=access)
        assert 'integration-fixture' in logs['logs']
        for action, expected in [('stop', 'exited'), ('start', 'running'), ('restart', 'running')]:
            request('/api/docker/containers/' + fixture + '/' + action, {}, access)
            assert docker('inspect', '--format', '{{.State.Status}}', fixture) == expected
        compose('restart', 'app')
        base = 'http://' + compose('port', 'app', '3001')
        for _ in range(60):
            try:
                request('/api/diagnostics', token=access)
                break
            except (OSError, AssertionError):
                time.sleep(1)
        else:
            raise AssertionError('app failed to recover after restart')
        print('Compose integration passed: diagnostics, authenticated inventory, details, logs, stop/start/restart, persistence')
    finally:
        subprocess.run(['docker', 'compose', '-p', project, '-f', str(compose_file), 'down', '-v'], check=True)

