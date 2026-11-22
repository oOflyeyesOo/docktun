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
        raise RuntimeError((result.stderr or result.stdout)[:2000])
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
    docker('run', '-d', '--privileged', '--name', engine, '--dns-search', 'fixture.invalid', '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', os.environ.get('DOCKTUN_TEST_ENGINE_IMAGE', 'docker:29-dind'))
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
          '-v', '/var/run/docker.sock:/var/run/docker.sock', '-e', 'DOCKTUN_PREPARE_PROVIDERS=true', '-e', 'DOCKER_NETWORK=' + network, image)
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
    managed = {row['name']: row for row in request('/api/services')['services']}
    for name, kind in [('docktun-cloudflared', 'cloudflare'), ('docktun-tailscale', 'tailscale'), ('docktun-wireguard', 'wireguard')]:
        assert managed[name]['kind'] == kind and not managed[name]['running'], managed
    own = json.loads(inner('inspect', 'docktun'))[0]
    wire = json.loads(inner('inspect', 'docktun-wireguard'))[0]
    data_source = next(m['Source'] for m in own['Mounts'] if m['Destination'] == '/app/data')
    state_source = next(m['Source'] for m in wire['Mounts'] if m['Destination'] == '/config')
    assert state_source == data_source + '/wireguard' and state_source != '/app/data/wireguard', wire['Mounts']
    assert wire['Config']['Labels']['docktun.provider'] == 'wireguard'
    existing_id = inner('run', '-d', '--name', 'already-running', 'alpine:3.23', 'sleep', '3600')
    for mode in ['compose', 'direct']:
        request('/api/docker/run', {'command': 'docker run --name already-running alpine:3.23 sleep 3600', 'mode': mode}, expected=400)
        assert inner('inspect', '--format', '{{.Id}}', 'already-running') == existing_id
        assert state('already-running') == 'running'
    launch = request('/api/docker/run', {'command': 'docker run -d --name new-launch alpine:3.23 sleep 3600', 'mode': 'direct'}, expected=202)
    for _ in range(30):
        output = request(f"/api/docker/run/output/{launch['job_id']}")
        if output['done']:
            assert output['status'] == 'completed', output
            break
        time.sleep(1)
    else:
        raise AssertionError('new launch did not finish')
    assert state('new-launch') == 'running'
    request('/api/provider/cloudflare/credentials', expected=401, authenticated=False)
    request('/api/control/start', {}, expected=500)  # Missing credentials must leave it stopped.
    assert state('docktun-cloudflared') == 'created'
    connector = 'test-connector-private-value'
    req = urllib.request.Request(base + '/api/provider/cloudflare/credentials', data=json.dumps({'token': connector}).encode(), method='PUT', headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req) as response:
        payload = response.read()
        assert connector.encode() not in payload and json.loads(payload)['has_token']
    # Legacy connectors can live on a different origin network from DockTun.
    # Authentication migration must preserve those routes and aliases.
    inner('network', 'create', '--subnet', '172.29.250.0/24', 'legacy-origin')
    inner('network', 'create', '--subnet', '172.29.251.0/24', 'legacy-extra')
    api_version = tuple(map(int, inner('version', '--format', '{{.Server.APIVersion}}').split('.')[:2]))
    priorities = api_version >= (1, 48)
    primary_network = 'name=legacy-origin,gw-priority=7' if priorities else 'legacy-origin'
    secondary_options = ['--gw-priority', '3'] if priorities else []
    good_provider_image = 'cloudflare/cloudflared:fixture-good'
    inner('tag', 'cloudflare/cloudflared:latest', good_provider_image)
    inner('rm', 'docktun-cloudflared')
    legacy_connector = inner('run', '-d', '--name', 'docktun-cloudflared',
                             '--network', primary_network, '--ip', '172.29.250.4', '--network-alias', 'connector-origin',
                             '--dns', '127.0.0.11', '--dns-search', 'fixture.invalid',
                             '--dns-option', 'ndots:0', '--add-host', 'custom-origin:192.0.2.14',
                             '--mount', 'type=volume,source=scoped-cloudflare,target=/etc/cloudflared,readonly',
                             'cloudflare/cloudflared:latest')
    inner('network', 'connect', '--ip', '172.29.251.4', *secondary_options, '--alias', 'connector-extra', 'legacy-extra', 'docktun-cloudflared')
    legacy_settings = json.loads(inner('inspect', 'docktun-cloudflared'))[0]['HostConfig']
    inner('run', '-d', '--name', 'legacy-web', '--network', 'legacy-origin',
          '--network-alias', 'mediastorm', 'alpine:3.23', 'sleep', '3600')
    request('/api/remote-access/tailscale/control', {'action': 'start'})
    # A bad replacement must relinquish both requested addresses and restore
    # the original connector before a subsequent migration can reuse them.
    inner('build', '-t', 'cloudflare/cloudflared:latest', '-', input='FROM alpine:3.23\nENTRYPOINT ["/missing-connector-fixture"]\n')
    request('/api/control/start', {}, expected=500)
    restored = json.loads(inner('inspect', 'docktun-cloudflared'))[0]
    assert restored['Id'] == legacy_connector and restored['State']['Running'], restored
    assert restored['NetworkSettings']['Networks']['legacy-origin']['IPAMConfig']['IPv4Address'] == '172.29.250.4'
    assert restored['NetworkSettings']['Networks']['legacy-extra']['IPAMConfig']['IPv4Address'] == '172.29.251.4'
    if priorities:
        assert restored['NetworkSettings']['Networks']['legacy-origin']['GwPriority'] == 7
        assert restored['NetworkSettings']['Networks']['legacy-extra']['GwPriority'] == 3
    inner('tag', good_provider_image, 'cloudflare/cloudflared:latest')
    request('/api/control/start', {})
    migrated = json.loads(inner('inspect', 'docktun-cloudflared'))[0]
    assert migrated['Id'] != legacy_connector
    for setting in ['Dns', 'DnsSearch', 'DnsOptions', 'ExtraHosts']:
        assert migrated['HostConfig'][setting] == legacy_settings[setting], setting
    assert set(migrated['NetworkSettings']['Networks']) == {'legacy-origin', 'legacy-extra'}, migrated
    assert 'connector-origin' in migrated['NetworkSettings']['Networks']['legacy-origin']['Aliases']
    assert 'connector-extra' in migrated['NetworkSettings']['Networks']['legacy-extra']['Aliases']
    for network_name, address in [('legacy-origin', '172.29.250.4'), ('legacy-extra', '172.29.251.4')]:
        assert migrated['NetworkSettings']['Networks'][network_name]['IPAMConfig']['IPv4Address'] == address
    if priorities:
        assert migrated['NetworkSettings']['Networks']['legacy-origin']['GwPriority'] == 7
        assert migrated['NetworkSettings']['Networks']['legacy-extra']['GwPriority'] == 3
    origin_ip = json.loads(inner('inspect', 'legacy-web'))[0]['NetworkSettings']['Networks']['legacy-origin']['IPAddress']
    lookup_error = None
    for _ in range(30):
        try:
            lookup = inner('exec', 'docktun-cloudflared', 'nslookup', '-type=A', 'mediastorm.')
            assert origin_ip in lookup.split(), lookup
            break
        except (RuntimeError, AssertionError) as error:
            lookup_error = error
            time.sleep(1)
    else:
        raise AssertionError(f'migrated connector could not resolve {origin_ip}: {lookup_error}')

    request('/api/remote-access/wireguard/control', {'action': 'start'})
    for name in ['docktun-cloudflared', 'docktun-tailscale', 'docktun-wireguard']:
        assert state(name) == 'running', name
        attached = json.loads(inner('inspect', name))[0]['NetworkSettings']['Networks']
        expected_network = 'legacy-origin' if name == 'docktun-cloudflared' else network
        assert expected_network in attached, attached
        assert attached[expected_network]['NetworkID'] == json.loads(inner('network', 'inspect', expected_network))[0]['Id']
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
    namespace_target = inner('run', '-d', '--name', 'user-vpn', '--network', 'legacy-origin', 'alpine:3.23', 'sleep', '3600')
    legacy_shared = inner('create', '--name', 'docktun-cloudflared', '--network', 'container:user-vpn',
                         '--mount', 'type=volume,source=scoped-cloudflare,target=/etc/cloudflared,readonly',
                         'cloudflare/cloudflared:latest')
    inner('stop', 'user-vpn')
    request('/api/control/start', {}, expected=500)
    assert inner('inspect', '--format', '{{.Id}}', 'docktun-cloudflared') == legacy_shared
    assert state('docktun-cloudflared') == 'created'
    inner('start', 'user-vpn')
    request('/api/control/start', {})
    shared = json.loads(inner('inspect', 'docktun-cloudflared'))[0]
    assert shared['HostConfig']['NetworkMode'] == 'container:' + namespace_target
    assert shared['Id'] != legacy_shared and state('user-vpn') == 'running'
    assert not shared['HostConfig']['Dns'] and not shared['HostConfig']['ExtraHosts']
    inner('exec', 'docktun-cloudflared', 'nslookup', '-type=A', 'mediastorm.')
    request('/api/control/stop', {})
    assert state('user-vpn') == 'running'
    inner('rm', 'docktun-cloudflared')
    request('/api/control/restart', {})
    assert state('docktun-cloudflared') == 'running'
    assert request('/api/status')['running'] is True
    print('Providers passed: prepared stopped, all three independently running, credentials required, exact named/bind mounts, no token disclosure, restart persistence, legacy static addresses/gateway priorities/resolvers/aliases, failed migration restored, shared namespace target retained, missing Cloudflare recreated')
finally:
    subprocess.run(['docker', 'rm', '-f', '-v', engine], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
