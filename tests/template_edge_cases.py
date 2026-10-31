"""Draft edge cases and optional real database connections in a disposable fixture."""
import json
import os
import secrets
import subprocess
import time
import urllib.request
from pathlib import Path
from urllib.parse import quote

fixture = json.loads(Path('../../outputs/template-review-preview.json').read_text())
assert fixture['engine'].startswith('docktun-template-test-')
base = fixture['url']
token = ''


def request(path, body, expected=200):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    with urllib.request.urlopen(urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers), timeout=60) as response:
        assert response.status == expected, (path, response.status)
        return json.load(response)


token = request('/auth/login', {'username': 'owner', 'password': 'fixture-password-123'})['access_token']
cases = [
    ('unrelated credentials', '''services:
  database:
    image: postgres:16-alpine
    environment: {POSTGRES_PASSWORD: changeme}
  archive:
    image: alpine:3.23
    environment: {BACKUP_PASSWORD: changeme}
'''),
    ('advanced ports followed by normal ports', '''services:
  app:
    image: alpine:3.23
    ports:
      - {target: 8000, published: "18000-18010", protocol: tcp}
      - {target: 9000, published: "19000", protocol: udp}
'''),
    ('variable bind address', '''services:
  app:
    image: alpine:3.23
    ports:
      - "${BIND_IP:-127.0.0.1}:18080:80"
      - {target: 81, published: "18082", protocol: udp, host_ip: "${BIND_IP:-127.0.0.1}"}
'''),
    ('disabled health check', '''services:
  database:
    image: postgres:16-alpine
    healthcheck: {test: [NONE]}
  app:
    image: alpine:3.23
    environment: {DB_HOST: database}
'''),
]
failures = []
for name, compose in cases:
    result = request('/api/templates/setup', {'compose_content': compose})
    try:
        if name == 'unrelated credentials':
            assert not result['credential_links'], result['credential_links']
        elif name.startswith('advanced ports'):
            fields = [f for f in result['fields'] if f['kind'] == 'port']
            assert len(fields) == 1 and fields[0]['value'] == '19000' and fields[0]['port_uses'][0]['protocol'] == 'udp', fields
        elif name == 'variable bind address':
            ports = request('/api/templates/ports', {'compose_content': compose.replace('127.0.0.1', '192.0.2.99'), 'values': {'var:BIND_IP': '127.0.0.1'}})
            assert len(ports['values']) == 2, ports
        else:
            assert not result['dependencies'][0]['healthy'], result['dependencies']
            applied = request('/api/templates/setup', {'compose_content': compose, 'apply': True, 'connect_dependencies': True})
            assert 'service_started' in applied['compose_content'] and 'service_healthy' not in applied['compose_content'], applied
        print('PASS', name, flush=True)
    except Exception as error:
        failures.append(name)
        print('FAIL', name, str(error), flush=True)
catalog = request('/api/templates/catalog?url=' + quote('https://raw.githubusercontent.com/Lissy93/portainer-templates/main/templates.json', safe=''), None)
gitea = next(item for item in catalog if item['name'] == 'Gitea' and not item.get('unavailable'))
draft = request('/api/templates/import', {'collection': gitea['collection'], 'id': gitea['id']})
schema = request('/api/templates/setup', draft)
clock_mount = next(m for m in schema['storage'] if 'timezone' in m['target'])
if clock_mount['target'] == '/etc/timezone' and clock_mount['source'] == '/etc/timezone' and clock_mount['read_only']:
    print('PASS community inline read-only mount and host-path whitespace', flush=True)
else:
    failures.append('community inline read-only mount')
    print('FAIL community inline read-only mount', clock_mount, flush=True)
if failures:
    raise AssertionError('Draft regressions: ' + ', '.join(failures))

if os.environ.get('DOCKTUN_EDGE_DEPLOY') == '1':
    def inner(*args):
        return subprocess.check_output(['docker', 'exec', fixture['engine'], 'docker', *args], text=True, stderr=subprocess.STDOUT).strip()

    services = {}
    for name in ['first', 'second']:
        services[name] = {'image': 'postgres:16-alpine', 'environment': {'POSTGRES_USER': name, 'POSTGRES_DB': name, 'POSTGRES_PASSWORD': 'sample'},
                          'healthcheck': {'test': ['NONE']} if name == 'first' else {'test': ['CMD-SHELL', 'pg_isready -U second -d second'], 'interval': '1s', 'timeout': '2s', 'retries': 60}}
        services[name + '-client'] = {'image': 'postgres:16-alpine',
            'environment': {'DATABASE_URL': f'postgres://{name}:sample@{name}/{name}', 'DB_PASSWORD': 'sample', 'ADMIN_PASSWORD': 'sample'},
            'command': ['sh', '-c', 'for i in $$(seq 1 60); do if pg_isready -d "$$DATABASE_URL" >/dev/null 2>&1; then exec psql "$$DATABASE_URL" -Atc "SELECT current_database()"; fi; sleep 1; done; exit 1']}
    compose = json.dumps({'services': services})
    schema = request('/api/templates/setup', {'compose_content': compose})
    links = {}
    for link in schema['credential_links']:
        if link['kind'] == 'password':
            uses = ','.join(link['uses'])
            assert not ('first ·' in uses and 'second ·' in uses), link
            links[link['id']] = secrets.token_hex(24)
    assert len(links) == 2, schema['credential_links']
    configured = request('/api/templates/setup', {'compose_content': compose, 'apply': True, 'connect_dependencies': True, 'credential_links': links})
    name = 'edge-accounts-' + secrets.token_hex(3)
    form = {'name': name, 'compose_content': configured['compose_content'], 'env_content': configured['env_content'], 'compatibility': True}
    reviewed = request('/api/projects/review', form)
    assert reviewed['ready'], reviewed
    project = request('/api/projects', {**form, 'review_storage': True, 'auto_start': True}, expected=201)
    try:
        for _ in range(210):
            output = request(f"/api/projects/{project['id']}/output", None)
            if output['done']:
                assert not any('[ERROR]' in line for line in output['lines']), output
                break
            time.sleep(2)
        else:
            raise AssertionError('deployment timed out')
        for service in ['first-client', 'second-client']:
            cid = inner('ps', '-aq', '--filter', 'label=com.docker.compose.project=' + name, '--filter', 'label=com.docker.compose.service=' + service)
            for _ in range(90):
                info = json.loads(inner('inspect', cid))[0]
                if not info['State']['Running']:
                    assert info['State']['ExitCode'] == 0, inner('logs', cid)
                    assert inner('logs', cid).splitlines()[-1] == service.split('-')[0]
                    assert 'ADMIN_PASSWORD=sample' in info['Config']['Env'], 'app admin credential was overwritten'
                    break
                time.sleep(1)
            else:
                raise AssertionError(service + ' connection did not finish')
        print('PASS two independent databases with different generated passwords and real client SQL; disabled healthcheck starts normally, active healthcheck gates startup; app admin credentials preserved', flush=True)
    finally:
        request(f"/api/projects/{project['id']}/down", {})
