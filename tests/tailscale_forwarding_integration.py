"""Authenticated TCP forwarding against a disposable local Headscale tailnet.
No user's Tailscale account or auth keys are used. Creates two separate app devices; no shared DockTun device is needed.
"""
import hashlib
import json
import os
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

prefix = 'docktun-forward-test-' + uuid.uuid4().hex[:8]
network = prefix + '-net'
app, control, client, web = [prefix + '-' + x for x in ['app', 'control', 'client', 'web']]
tailscale = ''
node_networks = []
containers, volumes = [], []
token, base = '', ''

def docker(*args, input=None, timeout=90):
    p = subprocess.run(['docker', *args], input=input, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if p.returncode:
        raise RuntimeError('Docker command failed: ' + p.stderr[:1500])
    return p.stdout.strip()

def request(path, body=None, expected=200):
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
    try:
        response = urllib.request.urlopen(req, timeout=150)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        data = response.read()
        assert response.status == expected, (response.status, data[:1000])
        return json.loads(data) if data and response.headers.get_content_type() == 'application/json' else None

try:
    docker('network', 'create', network)
    for suffix in ['control-data', 'control-config', 'app-data', 'ts-data']:
        volume = prefix + '-' + suffix; docker('volume', 'create', volume); volumes.append(volume)
    docker('run', '--rm', '-v', volumes[1] + ':/out', 'alpine:3.23', 'sh', '-c', 'apk add --no-cache openssl >/dev/null && openssl req -x509 -nodes -newkey rsa:2048 -keyout /out/key.pem -out /out/cert.pem -days 1 -subj /CN=headscale -addext subjectAltName=DNS:headscale', timeout=180)
    config = {
        'server_url': 'https://headscale:443', 'listen_addr': '0.0.0.0:443',
        'tls_cert_path': '/etc/headscale/cert.pem', 'tls_key_path': '/etc/headscale/key.pem',
        'noise': {'private_key_path': '/var/lib/headscale/noise.key'},
        'prefixes': {'v4': '100.64.0.0/10', 'v6': 'fd7a:115c:a1e0::/48'},
        'database': {'type': 'sqlite', 'sqlite': {'path': '/var/lib/headscale/db.sqlite'}},
        'dns': {'magic_dns': True, 'base_domain': 'docktun-test.internal', 'override_local_dns': False},
        'derp': {'urls': ['https://controlplane.tailscale.com/derpmap/default'], 'auto_update_enabled': False},
        'disable_check_updates': True, 'unix_socket': '/var/lib/headscale/headscale.sock',
        'log': {'level': 'warn'},
    }
    containers.append(control)
    docker('create', '--name', control, '--network', network, '--network-alias', 'headscale',
           '-v', volumes[0] + ':/var/lib/headscale', '-v', volumes[1] + ':/etc/headscale', 'headscale/headscale:0.29.6', 'serve', timeout=180)
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / 'config.yaml'; path.write_text(json.dumps(config))
        docker('cp', str(path), control + ':/etc/headscale/config.yaml')
    docker('start', control)
    for _ in range(30):
        try:
            user = json.loads(docker('exec', control, 'headscale', 'users', 'create', 'tester', '-o', 'json')); break
        except (RuntimeError, json.JSONDecodeError):
            time.sleep(1)
    else:
        raise RuntimeError(docker('logs', control))
    key_info = json.loads(docker('exec', control, 'headscale', 'preauthkeys', 'create', '--user', str(user['id']), '--reusable', '--expiration', '1h', '-o', 'json'))
    key = key_info['key']
    containers.append(app)
    docker('run', '-d', '--name', app, '--network', network, '-p', '127.0.0.1::3001',
           '-v', volumes[2] + ':/app/data', '-v', '/var/run/docker.sock:/var/run/docker.sock',
           '-e', 'DOCKER_NETWORK=' + network, '-e', 'TAILSCALE_NETWORK=' + network,
           '-e', 'TAILSCALE_STATE_VOLUME=' + volumes[3], os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:reconciled'))
    base = 'http://' + docker('port', app, '3001/tcp')
    for _ in range(60):
        try:
            request('/healthz'); break
        except (OSError, AssertionError, json.JSONDecodeError):
            time.sleep(1)
    setup = docker('exec', app, 'cat', '/app/data/setup.token')
    token = request('/api/setup/complete', {'setup_token': setup, 'admin_username': 'tester', 'admin_password': 'integration-password-123', 'tunnel': {'type': 'tailscale'}})['access_token']
    containers.append(client)
    docker('run', '-d', '--name', client, '--network', network, '-v', volumes[1] + ':/testcert:ro', '-e', 'SSL_CERT_FILE=/testcert/cert.pem', '--entrypoint', 'tailscaled', 'tailscale/tailscale:latest', '--tun=userspace-networking', '--state=mem:', '--socks5-server=0.0.0.0:1055')
    time.sleep(2)
    docker('exec', client, 'tailscale', 'up', '--login-server=https://headscale:443', '--authkey=' + key, '--hostname=test-client', '--accept-dns=false')
    app_nodes = []
    for index in range(2):
        web_app = web + str(index)
        containers.append(web_app)
        docker('run', '-d', '--name', web_app, '--network', network, 'busybox:1.37', 'sh', '-c', 'mkdir /www; echo app-' + str(index) + ' > /www/index.html; httpd -f -p 80 -h /www')
        node = 'docktun-ts-' + hashlib.sha256(web_app.encode()).hexdigest()[:20]
        containers.append(node); node_networks.append(node); volumes.append(node + '-state')
        status = request('/api/docker/containers/' + web_app + '/tailscale', {})
        assert node == status['node']
        inspect = json.loads(docker('inspect', node))[0]
        assert list(inspect['NetworkSettings']['Networks']) == [node], inspect['NetworkSettings']
        assert not inspect['HostConfig']['Privileged'] and not inspect['HostConfig']['PortBindings']
        assert inspect['HostConfig']['CapDrop'] == ['ALL']
        assert len(inspect['Mounts']) == 1 and inspect['Mounts'][0]['Type'] == 'volume'
        # Test-only access to our disposable control plane and CA.
        docker('network', 'connect', network, node)
        with tempfile.TemporaryDirectory() as directory:
            cert = str(Path(directory) / 'headscale.crt')
            docker('cp', control + ':/etc/headscale/cert.pem', cert)
            docker('cp', cert, node + ':/usr/local/share/ca-certificates/headscale.crt')
        docker('exec', node, 'update-ca-certificates')
        docker('restart', node)
        time.sleep(2)
        docker('exec', node, 'tailscale', 'up', '--login-server=https://headscale:443', '--authkey=' + key, '--hostname=app-' + str(index), '--accept-dns=false', '--accept-routes=false', '--reset')
        settings = {'mode': 'private', 'private_type': 'tailscale', 'private_port': 8080, 'target': web_app + ':80'}
        request('/api/docker/containers/' + web_app + '/exposure', settings)
        identity = json.loads(docker('exec', node, 'tailscale', 'status', '--json'))
        app_nodes.append((web_app, node, identity['TailscaleIPs'][0], identity['Self']['ID']))
        prefs = json.loads(docker('exec', node, 'tailscale', 'debug', 'prefs'))
        assert not prefs.get('AdvertiseRoutes') and not prefs.get('RunSSH'), prefs
    assert app_nodes[0][2] != app_nodes[1][2] and app_nodes[0][3] != app_nodes[1][3]

    def fetch_node(index, port=8080, expected=True):
        node_ip = app_nodes[index][2]
        for attempt in range(15 if expected else 1):
            try:
                reply = docker('run', '--rm', '--network', network, '--entrypoint', 'curl', os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:per-app'), '--max-time', '5', '-fsS', '--socks5-hostname', client + ':1055', 'http://' + node_ip + ':' + str(port), timeout=10)
                if not expected: raise AssertionError('unselected port unexpectedly reachable')
                assert 'app-' + str(index) in reply, reply
                return
            except (RuntimeError, subprocess.TimeoutExpired):
                if not expected: return
                time.sleep(1)
        raise AssertionError('app device never became reachable')

    fetch_node(0); fetch_node(1)
    fetch_node(0, 80, expected=False)
    first, node, address, identity = app_nodes[0]
    docker('restart', node); time.sleep(3)
    fetch_node(0)
    assert json.loads(docker('exec', node, 'tailscale', 'status', '--json'))['Self']['ID'] == identity
    # A Compose-style recreation drops dynamic network attachments. Reconcile only this selected app.
    docker('rm', '-f', first)
    docker('run', '-d', '--name', first, '--network', network, 'busybox:1.37', 'sh', '-c', 'mkdir /www; echo app-0 > /www/index.html; httpd -f -p 80 -h /www')
    fetch_node(0)
    assert json.loads(docker('exec', node, 'tailscale', 'status', '--json'))['Self']['ID'] == identity
    request('/api/docker/containers/' + first + '/exposure', {'mode': 'internal'})
    assert not json.loads(docker('inspect', node))[0]['State']['Running']
    fetch_node(0, expected=False); fetch_node(1)
    for _ in range(10):
        status = request('/api/docker/containers/' + first + '/tailscale', {})
        if status['state'] == 'Running': break
        time.sleep(1)
    assert status['state'] == 'Running', status
    request('/api/docker/containers/' + first + '/exposure', {'mode': 'private', 'private_type': 'tailscale', 'private_port': 8080, 'target': first + ':80'})
    fetch_node(0)
    assert json.loads(docker('exec', node, 'tailscale', 'status', '--json'))['Self']['ID'] == identity
    assert request('/api/status')['status'] == 'disabled'
    assert 'docktun-tailscale' not in docker('ps', '-a', '--format', '{{.Names}}').splitlines()
    print('Per-app Tailscale passed: distinct devices and IPs, same ports, no subnet routes, selected-port isolation, persistent identity across app recreation, independent disable/re-enable, no shared DockTun device', flush=True)

finally:
    for name in reversed(containers):
        subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for volume in volumes:
        subprocess.run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL)
    for item in node_networks + [network]:
        subprocess.run(['docker', 'network', 'rm', item], stdout=subprocess.DEVNULL)
