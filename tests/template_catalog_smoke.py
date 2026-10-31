"""Opt-in live catalog deployments in an existing disposable integration engine.

Run template_review_integration.py with DOCKTUN_KEEP_TEST_ENGINE=1 first, then
set DOCKTUN_SAMPLE_ENGINE and DOCKTUN_SAMPLE_URL from its preview output.
This pulls upstream images and only operates inside that named nested engine.
"""
import json
import os
import random
import secrets
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import quote

engine = os.environ['DOCKTUN_SAMPLE_ENGINE']
base = os.environ['DOCKTUN_SAMPLE_URL']
assert engine.startswith('docktun-template-test-') and base.startswith('http://127.0.0.1:')
seed = int(os.environ.get('DOCKTUN_SAMPLE_SEED', '20261030'))
token = ''
run_id = secrets.token_hex(3)


def inner(*args, input=None):
    return subprocess.check_output(['docker', 'exec', '-i', engine, 'docker', *args], input=input, text=True, stderr=subprocess.STDOUT).strip()


def request(path, body=None, expected=200):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    try:
        response = urllib.request.urlopen(req, timeout=120)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        data = response.read()
        assert response.status == expected, (path, response.status, data[:1500])
        return json.loads(data)


def wait(check, seconds=180):
    deadline = time.monotonic() + seconds
    while True:
        try:
            return check()
        except (AssertionError, subprocess.CalledProcessError):
            if time.monotonic() >= deadline:
                raise
            time.sleep(2)


token = request('/auth/login', {'username': 'owner', 'password': 'fixture-password-123'})['access_token']
source = 'https://raw.githubusercontent.com/Lissy93/portainer-templates/main/templates.json'
catalog = request('/api/templates/catalog?url=' + quote(source, safe=''))
# Stratified random sample: self-contained web, database, productivity,
# monitoring and utility apps. No external accounts, devices or Docker socket.
pools = [('web', ['Nginx', 'Httpd']), ('database', ['Redis (container)', 'PostgreSQL']),
         ('productivity', ['Linkding', 'Freshrss']),
         ('monitoring', ['Uptime Kuma (container)', 'Changedetection.io (container)']),
         ('utility', ['CyberChef', 'IT-Tools'])]
rng = random.Random(seed)
selected = [(category, rng.choice(names)) for category, names in pools]
print('SEED', seed, 'SAMPLE', selected, flush=True)
results = []
for category, name in selected:
    result = {'category': category, 'name': name, 'passed': False}
    project = None
    try:
        item = next(t for t in catalog if t['name'] == name and not t.get('unavailable'))
        result['source'] = item['collection']
        draft = request('/api/templates/import', {'collection': item['collection'], 'id': item['id']})
        result['pinned_source'] = draft['source_url']
        schema = request('/api/templates/setup', draft)
        values = {f['id']: f['value'] for f in schema['fields']}
        for field in schema['fields']:
            if field['password']:
                values[field['id']] = secrets.token_hex(24)
            elif field['name'] == 'POSTGRES_USER' and not values[field['id']]:
                values[field['id']] = 'sampleowner'
        ports = request('/api/templates/ports', {**draft, 'values': values})
        values.update(ports['values'])
        storage = {m['id']: {'mode': 'managed'} for m in schema['storage'] if m['can_manage'] and not m['read_only']}
        configured = request('/api/templates/setup', {**draft, 'apply': True, 'values': values, 'storage': storage, 'connect_dependencies': True})
        form = {'name': 'sample-' + category + '-' + run_id, 'compose_content': configured['compose_content'],
                'env_content': configured['env_content'], 'compatibility': True}
        review = request('/api/projects/review', form)
        result['checks'] = review.get('checks', [])
        assert review['ready'], review
        project = request('/api/projects', {**form, 'review_storage': True, 'auto_start': True,
                          'template_source': draft['source_url']}, expected=201)

        def deployed():
            output = request(f"/api/projects/{project['id']}/output")
            assert output['done'], 'deployment in progress'
            assert not any('[ERROR]' in line for line in output['lines']), output
            return output
        wait(deployed, 420)
        containers = inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=' + form['name']).splitlines()
        assert len(containers) == 1, containers
        cid = containers[0]

        def responds():
            info = json.loads(inner('inspect', cid))[0]
            assert info['State']['Running'], info['State']
            if category == 'database':
                if name.startswith('Redis'):
                    assert inner('exec', cid, 'redis-cli', 'ping') == 'PONG'
                else:
                    user = next(v.split('=', 1)[1] for v in info['Config']['Env'] if v.startswith('POSTGRES_USER='))
                    assert inner('exec', cid, 'psql', '-U', user, '-Atc', 'SELECT 1') == '1'
            else:
                bindings = info['NetworkSettings']['Ports']
                web = next((p for p in ['80/tcp', '3001/tcp', '5000/tcp', '9090/tcp', '8080/tcp'] if bindings.get(p)), None)
                assert web, bindings
                port = bindings[web][0]['HostPort']
                response = subprocess.check_output(['docker', 'exec', engine, 'wget', '-q', '-O', '-', f'http://127.0.0.1:{port}/'], text=True, stderr=subprocess.STDOUT)
                assert '<html' in response.lower() or '<!doctype' in response.lower(), response[:200]
            return info
        info = wait(responds)
        result['image'] = info['Config']['Image']
        result['image_id'] = info['Image']
        result['mounts'] = [{'type': m['Type'], 'destination': m['Destination'], 'writable': m['RW']} for m in info['Mounts']]
        assert all(m['Type'] == 'volume' for m in info['Mounts']), info['Mounts']
        marker = '.docktun-smoke-' + run_id
        for mount in info['Mounts']:
            inner('run', '--rm', '-v', mount['Name'] + ':/proof', 'alpine:3.23', 'sh', '-c', 'printf persisted > /proof/' + marker)
        if name == 'PostgreSQL':
            user = next(v.split('=', 1)[1] for v in info['Config']['Env'] if v.startswith('POSTGRES_USER='))
            inner('exec', cid, 'psql', '-U', user, '-c', "CREATE TABLE smoke_proof(value text); INSERT INTO smoke_proof VALUES ('persisted');")
        if name.startswith('Redis'):
            inner('exec', cid, 'redis-cli', 'set', 'sample-proof', 'persisted')
            inner('exec', cid, 'redis-cli', 'save')
        if name == 'Linkding':
            credentials = {f['name']: values[f['id']] for f in schema['fields'] if f['name'].startswith('LD_SUPERUSER_')}
            code = 'import json,sys; from django.contrib.auth import authenticate; c=json.load(sys.stdin); assert authenticate(username=c["LD_SUPERUSER_NAME"],password=c["LD_SUPERUSER_PASSWORD"]) is not None; print("credential-check-passed")'
            check = inner('exec', '-i', cid, 'python', 'manage.py', 'shell', '-c', code, input=json.dumps(credentials))
            assert 'credential-check-passed' in check, 'generated Linkding credentials did not authenticate'
            result['generated_login_verified'] = True
        inner('restart', cid)
        wait(responds)
        for mount in info['Mounts']:
            assert inner('run', '--rm', '-v', mount['Name'] + ':/proof:ro', 'alpine:3.23', 'cat', '/proof/' + marker) == 'persisted'
        if name == 'PostgreSQL':
            assert inner('exec', cid, 'psql', '-U', user, '-Atc', 'SELECT value FROM smoke_proof') == 'persisted'
        if name.startswith('Redis'):
            assert inner('exec', cid, 'redis-cli', 'get', 'sample-proof') == 'persisted'
        result['passed'] = True
        print('PASS', name, 'startup, protocol/HTTP response, storage, restart', flush=True)
    except Exception as error:
        result['error'] = str(error)
        print('FAIL', name, str(error)[:1600], flush=True)
    finally:
        if project:
            # Preserve project files/volumes for inspection; stop sample apps.
            request(f"/api/projects/{project['id']}/down", {})
        results.append(result)

output = Path(os.environ.get('DOCKTUN_SAMPLE_OUTPUT', 'template-catalog-smoke.json'))
output.write_text(json.dumps({'seed': seed, 'pools': pools, 'results': results}, indent=2), encoding='utf-8')
assert all(r['passed'] for r in results), f'Sample failures: see {output}'
