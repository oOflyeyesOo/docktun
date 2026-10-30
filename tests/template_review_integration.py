"""Storage review, host/container path translation and safe template deployment."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

engine = 'docktun-template-test-' + uuid.uuid4().hex[:10]
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:template-review')
base, token = '', ''
passed = False

def docker(*args, input=None):
    return subprocess.check_output(['docker', *map(str, args)], input=input, text=True, stderr=subprocess.STDOUT).strip()

def inner(*args, input=None):
    return docker('exec', '-i', engine, 'docker', *args, input=input)

def eventually(check):
    for _ in range(90):
        try:
            return check()
        except (OSError, AssertionError, subprocess.CalledProcessError):
            time.sleep(1)
    return check()

def request(path, body=None, expected=200, auth=True):
    headers = {'Content-Type': 'application/json'}
    if token and auth:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
    try:
        response = urllib.request.urlopen(req, timeout=90)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        data = response.read()
        assert response.status == expected, (path, response.status, data[:1000])
        return json.loads(data)

def action_done(project_id, failure=False):
    def check():
        output = request(f'/api/projects/{project_id}/output')
        assert output['done'], output
        assert any('[ERROR]' in line for line in output['lines']) == failure, output
        return output
    return eventually(check)

try:
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', 'docker:29-dind')
    eventually(lambda: inner('info'))
    with tempfile.TemporaryDirectory() as directory:
        archive = Path(directory) / 'images.tar'
        docker('save', '-o', archive, image, 'alpine:3.23')
        with archive.open('rb') as stream:
            subprocess.run(['docker', 'exec', '-i', engine, 'docker', 'load'], stdin=stream, stdout=subprocess.DEVNULL, check=True)
    docker('exec', engine, 'sh', '-c', 'mkdir -p /volume1/photos /volume1/readonly "/volume1/media,files" && printf preserved > /volume1/config.txt')
    inner('run', '-d', '--name', 'docktun', '-p', '3001:3001', '-v', 'data:/app/data', '-v', 'projects:/opt/projects',
          '-v', '/var/run/docker.sock:/var/run/docker.sock', '-v', '/volume1/readonly:/readonly:ro', '-e', 'DOCKTUN_PREPARE_PROVIDERS=false',
          '-e', 'SERVER_PRIVATE_KEY=dashboard-secret-must-not-leak', '-e', 'USER_VALUE=dashboard-environment', image)
    base = 'http://' + docker('port', engine, '3001/tcp')
    eventually(lambda: request('/api/setup/status', auth=False))
    secret = inner('exec', 'docktun', 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': secret, 'admin_username': 'owner', 'admin_password': 'fixture-password-123'})['access_token']
    for path, body in [('/api/templates/catalog', None), ('/api/templates/import', {'url': 'https://example.invalid/compose.yml'}), ('/api/projects/review', {})]:
        request(path, body, expected=401, auth=False)
    request('/api/templates/import', {'url': 'http://127.0.0.1/private.yml'}, expected=400)
    compose = '''services:
  app:
    image: alpine:3.23
    container_name: reviewed-app
    command: ["sleep", "3600"]
    environment:
      STACK_VALUE: "${USER_VALUE}"
      RESERVED_VALUE: "${SERVER_PRIVATE_KEY:-not-inherited}"
      LITERAL_VALUE: "${LITERAL_INPUT}"
    volumes:
      - /volume1/photos:/photos
      - /volume1/config.txt:/config/settings.txt:ro
      - app-data:/data
volumes:
  app-data:
'''
    form = {'name': 'reviewed-example', 'path': '', 'compose_content': compose, 'env_content': "USER_VALUE=from-stack\nLITERAL_INPUT='p$USER_VALUE${NOT_A_VARIABLE}'\n"}
    good = request('/api/projects/review', form)
    assert good['ready'] and len(good['mounts']) == 3, good
    assert all(m['status'].startswith('exists:') for m in good['mounts'] if m['type'] == 'bind'), good
    assert docker('exec', engine, 'cat', '/volume1/config.txt') == 'preserved'
    comma = request('/api/projects/review', {**form, 'compose_content': compose.replace('/volume1/photos', '/volume1/media,files')})
    assert comma['ready'], comma
    assert not any(row['name'].startswith('docktun-storage-check-') for row in request('/api/docker/containers'))
    for source in ['/path/to/photos', '/volume1/missing-photos']:
        bad = {**form, 'compose_content': compose.replace('/volume1/photos', source)}
        result = request('/api/projects/review', bad)
        assert not result['ready'] and result['problems'], result
        request('/api/projects', {**bad, 'review_storage': True, 'auto_start': True}, expected=409)
    assert request('/api/projects') == [], 'failed reviews created project records'
    docker('exec', engine, 'sh', '-c', 'test ! -e /path/to/photos && test ! -e /volume1/missing-photos')
    missing_variable = request('/api/projects/review', {**form, 'env_content': ''})
    assert not missing_variable['ready'], missing_variable
    build = request('/api/projects/review', {**form, 'compose_content': 'services:\n  app:\n    build: .\n'})
    assert not build['ready'] and 'repository' in build['problems'][0], build
    readonly = request('/api/projects/review', {**form, 'path': '/readonly/new-stack'})
    assert not readonly['ready'] and 'read-only' in readonly['problems'][0], readonly
    inner('exec', 'docktun', 'sh', '-c', 'mkdir -p /opt/projects/build-source && printf "FROM alpine:3.23\\n" > /opt/projects/build-source/Dockerfile')
    local_build = request('/api/projects/review', {**form, 'path': '/opt/projects/build-source', 'compose_content': 'services:\n  app:\n    build: .\n'})
    assert local_build['ready'], local_build
    external = request('/api/projects/review', {**form, 'compose_content': 'services:\n  app:\n    image: alpine:3.23\n    volumes: ["missing:/data"]\nvolumes:\n  missing:\n    external: true\n'})
    assert not external['ready'], external
    inner('exec', 'docktun', 'mkdir', '-p', '/opt/projects/ready-data')
    mapped = request('/api/projects/review', {**form, 'compose_content': compose.replace('/volume1/photos', '/opt/projects/ready-data')})
    assert mapped['ready'], mapped
    actual_projects = next(m['Source'] for m in json.loads(inner('inspect', 'docktun'))[0]['Mounts'] if m['Destination'] == '/opt/projects')
    assert any(m['source'] == actual_projects + '/ready-data' for m in mapped['mounts']), mapped
    project = request('/api/projects', {**form, 'review_storage': True, 'template_source': 'integration-fixture', 'auto_start': True}, expected=201)
    action_done(project['id'])
    assert inner('exec', 'reviewed-app', 'printenv', 'STACK_VALUE') == 'from-stack'
    assert inner('exec', 'reviewed-app', 'printenv', 'RESERVED_VALUE') == 'not-inherited'
    assert inner('exec', 'reviewed-app', 'printenv', 'LITERAL_VALUE') == 'p$USER_VALUE${NOT_A_VARIABLE}'
    duplicate = request('/api/projects/review', {**form, 'name': 'different-project'})
    assert not duplicate['ready'] and any('already in use' in problem for problem in duplicate['problems']), duplicate
    request('/api/docker/containers/' + inner('inspect', '--format', '{{.Id}}', 'reviewed-app') + '/update', {})
    assert inner('exec', 'reviewed-app', 'printenv', 'RESERVED_VALUE') == 'not-inherited'
    assert inner('exec', 'reviewed-app', 'printenv', 'LITERAL_VALUE') == 'p$USER_VALUE${NOT_A_VARIABLE}'
    actual = json.loads(inner('inspect', 'reviewed-app'))[0]
    file_mount = next(m for m in actual['Mounts'] if m['Destination'] == '/config/settings.txt')
    assert file_mount['Source'] == '/volume1/config.txt' and not file_mount['RW'], actual['Mounts']
    inner('exec', 'docktun', 'sh', '-c', 'test ! -e /opt/projects/reviewed-example/.docktun-template.json')
    # Deleting a source after review must not cause Docker to silently create it.
    inner('rm', '-f', 'reviewed-app')
    docker('exec', engine, 'rmdir', '/volume1/photos')
    request(f"/api/projects/{project['id']}/up", {})
    action_done(project['id'], failure=True)
    docker('exec', engine, 'sh', '-c', 'test ! -e /volume1/photos')
    assert inner('ps', '-aq', '--filter', 'label=docktun.storage-check') == '', 'storage probe leaked a helper'
    passed = True
    print('PASS: authenticated template review; real host directories/files checked read-only; placeholder/missing bind sources and external volumes blocked without creation; core-only paths mapped to host sources; stack .env respected and dashboard credentials excluded; strict bind sources preserved after deployment; probe helpers cleaned', flush=True)
    if os.environ.get('DOCKTUN_KEEP_TEST_ENGINE') == '1':
        Path('../../outputs/template-review-preview.json').write_text(json.dumps({'engine': engine, 'url': base}, indent=2))
finally:
    if not passed or os.environ.get('DOCKTUN_KEEP_TEST_ENGINE') != '1':
        subprocess.run(['docker', 'rm', '-f', '-v', engine], stdout=subprocess.DEVNULL, check=False)
