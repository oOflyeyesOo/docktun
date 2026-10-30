"""Generic Compose stack identity and creation conflicts in a disposable engine."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

engine = 'docktun-stack-identity-' + uuid.uuid4().hex[:10]
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:stack-identity')
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
        response = urllib.request.urlopen(req, timeout=45)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        data = response.read()
        assert response.status == expected, (path, response.status, data[:500])
        return json.loads(data)

try:
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', 'docker:29-dind')
    eventually(lambda: inner('info'))
    with tempfile.TemporaryDirectory() as directory:
        archive = Path(directory) / 'images.tar'
        docker('save', '-o', archive, image, 'alpine:3.23')
        with archive.open('rb') as stream:
            subprocess.run(['docker', 'exec', '-i', engine, 'docker', 'load'], stdin=stream, stdout=subprocess.DEVNULL, check=True)
    folder = '/volume1/stacks/shared'
    compose = '''services:
  app:
    image: alpine:3.23
    container_name: gallery-web
    command: ["sleep", "3600"]
    expose: ["8080"]
  database:
    image: alpine:3.23
    container_name: custom-database
    command: ["sleep", "3600"]
    expose: ["5432"]
'''
    docker('exec', '-i', engine, 'sh', '-c', 'mkdir -p ' + folder + ' && cat > ' + folder + '/compose.yml', input=compose)
    docker('exec', '-i', engine, 'sh', '-c', 'cat > ' + folder + '/override.yml', input='services:\n  app:\n    environment:\n      FIXTURE: generic\n')
    inner('compose', '-p', 'photos', '-f', folder + '/compose.yml', '-f', folder + '/override.yml', 'up', '-d')
    archive_compose = 'services:\n  app:\n    image: alpine:3.23\n    container_name: photos-archive\n    command: ["sleep", "3600"]\n'
    docker('exec', '-i', engine, 'sh', '-c', 'cat > ' + folder + '/archive.yml', input=archive_compose)
    inner('compose', '-p', 'archive', '-f', folder + '/archive.yml', 'up', '-d')
    standalone = inner('run', '-d', '--name', 'photos-postgres', 'alpine:3.23', 'sleep', '3600')
    inner('run', '-d', '--name', 'docktun', '-p', '3001:3001', '-v', 'data:/app/data', '-v', '/volume1/stacks:/stack-files',
          '-v', '/var/run/docker.sock:/var/run/docker.sock', '-e', 'DOCKTUN_PREPARE_PROVIDERS=false', image)
    base = 'http://' + docker('port', engine, '3001/tcp')
    eventually(lambda: request('/api/setup/status', auth=False))
    secret = inner('exec', 'docktun', 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': secret, 'admin_username': 'owner', 'admin_password': 'fixture-password-123'})['access_token']
    # Existing records deliberately reproduce a misleading folder match and an
    # obsolete name match. Discovery must not overwrite either record.
    archive = request('/api/projects', {'name': 'archive', 'path': folder, 'repo_url': 'https://example.invalid/private'}, expected=201)
    obsolete = request('/api/projects', {'name': 'photos', 'path': '/volume2/old-photos'}, expected=201)
    rows = request('/api/docker/containers')
    by_name = {row['name']: row for row in rows}
    app, database = by_name['gallery-web'], by_name['custom-database']
    assert app['project_name'] == database['project_name'] == 'photos', rows
    assert app['project_id'] == database['project_id'] and app['project_id'] not in (archive['id'], obsolete['id']), rows
    assert app['project_path'] == folder and app['compose_working_dir'] == folder, app
    assert app['compose_config_files'] == folder + '/compose.yml,' + folder + '/override.yml', app
    assert by_name['photos-archive']['project_id'] == archive['id'] and by_name['photos-archive']['project_name'] == 'archive'
    assert not by_name['photos-postgres'].get('project_name') and not by_name['photos-postgres'].get('project_id')
    for row in (app, database, by_name['photos-archive'], by_name['photos-postgres']):
        detail = request('/api/docker/containers/' + row['id'])
        for key in ('project_id', 'project_name', 'project_path', 'compose_project', 'compose_service', 'compose_working_dir', 'compose_config_files'):
            assert row.get(key) == detail.get(key), (key, row, detail)
    assert request('/api/docker/containers/' + database['id'] + '/project', {})['project_id'] == app['project_id']
    before = docker('exec', engine, 'cat', folder + '/compose.yml')
    for name, directory in [('new-name', folder), ('photos', '/volume2/new-photos'), ('archive', folder)]:
        request('/api/projects', {'name': name, 'path': directory, 'compose_content': 'services: {}'}, expected=409)
    assert docker('exec', engine, 'cat', folder + '/compose.yml') == before
    projects = {project['id']: project for project in request('/api/projects')}
    assert projects[archive['id']]['name'] == 'archive' and projects[archive['id']]['repo_url'] == 'https://example.invalid/private'
    assert projects[obsolete['id']]['path'] == '/volume2/old-photos'
    # Re-deploy through the mapped Synology folder. The resulting working_dir
    # label is container-local; both services must retain the original record.
    request('/api/projects/' + str(app['project_id']) + '/up', {})
    def action_done():
        output = request('/api/projects/' + str(app['project_id']) + '/output')
        assert output['done'] and not any('[ERROR]' in line for line in output['lines']), output
        return output
    eventually(action_done)
    rows = {row['name']: row for row in request('/api/docker/containers')}
    assert rows['gallery-web']['project_id'] == rows['custom-database']['project_id'] == app['project_id'], rows
    assert rows['gallery-web']['project_path'] == folder, rows
    assert inner('inspect', '--format', '{{.Id}}', 'photos-postgres') == standalone
    passed = True
    print('PASS: generic Compose labels determine membership; same-folder/different-project and obsolete saved names do not capture containers; standalone prefixes stay standalone; list/details agree; multi-file labels preserved; creation conflicts keep files and project metadata; Synology remapped redeploy keeps identity', flush=True)
    if os.environ.get('DOCKTUN_KEEP_TEST_ENGINE') == '1':
        Path('../../outputs/stack-identity-preview.json').write_text(json.dumps({'engine': engine, 'url': base}, indent=2))
finally:
    if not passed or os.environ.get('DOCKTUN_KEEP_TEST_ENGINE') != '1':
        subprocess.run(['docker', 'rm', '-f', '-v', engine], stdout=subprocess.DEVNULL, check=False)
