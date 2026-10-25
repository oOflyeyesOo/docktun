"""Authenticated TCP forwarding against a disposable local Headscale tailnet.
No user's Tailscale account or auth keys are used. Requires docktun-tailscale unused.
"""
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
tailscale = 'docktun-tailscale'
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

if tailscale in docker('ps', '-a', '--format', '{{.Names}}').splitlines():
    raise RuntimeError('docktun-tailscale already exists; use an isolated engine')

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
    containers.append(tailscale)
    docker('run', '-d', '--name', tailscale, '--network', network, '-v', volumes[3] + ':/var/lib/tailscale', '-v', volumes[1] + ':/testcert:ro', '-e', 'SSL_CERT_FILE=/testcert/cert.pem', '--entrypoint', 'tailscaled', 'tailscale/tailscale:latest', '--tun=userspace-networking', '--state=/var/lib/tailscale/tailscaled.state')
    time.sleep(2)
    docker('exec', tailscale, 'tailscale', 'up', '--login-server=https://headscale:443', '--authkey=' + key, '--hostname=docktun', '--accept-dns=false', '--reset')
    containers.append(client)
    docker('run', '-d', '--name', client, '--network', network, '-v', volumes[1] + ':/testcert:ro', '-e', 'SSL_CERT_FILE=/testcert/cert.pem', '--entrypoint', 'tailscaled', 'tailscale/tailscale:latest', '--tun=userspace-networking', '--state=mem:', '--socks5-server=0.0.0.0:1055')
    time.sleep(2)
    docker('exec', client, 'tailscale', 'up', '--login-server=https://headscale:443', '--authkey=' + key, '--hostname=test-client', '--accept-dns=false')
    containers.append(web)
    docker('run', '-d', '--name', web, '--network', network, 'busybox:1.37', 'sh', '-c', 'mkdir /www; echo forwarding-verified > /www/index.html; httpd -f -p 80 -h /www')
    settings = {'mode': 'private', 'private_type': 'tailscale', 'private_port': 18080, 'target': web + ':80'}
    request('/api/docker/containers/' + web + '/exposure', settings)
    tsip = json.loads(docker('exec', tailscale, 'tailscale', 'status', '--json'))['TailscaleIPs'][0]
    for _ in range(20):
        try:
            reply = docker('run', '--rm', '--network', network, '--entrypoint', 'curl', os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:reconciled'), '--max-time', '8', '-fsS', '--socks5-hostname', client + ':1055', 'http://' + tsip + ':18080', timeout=12)
            assert 'forwarding-verified' in reply, reply
            break
        except (RuntimeError, subprocess.TimeoutExpired, AssertionError) as error:
            last_error = str(error)
            time.sleep(1)
    else:
        raise AssertionError('tailnet forwarding never became reachable: ' + last_error + '\n' + docker('logs', tailscale)[-2500:])
    # Same private port cannot silently replace a different app's forwarding.
    request('/api/docker/containers/' + app + '/exposure', {**settings, 'target': app + ':3001'}, expected=500)
    docker('restart', tailscale)
    time.sleep(3)
    rules = json.loads(docker('exec', tailscale, 'tailscale', 'serve', 'status', '--json'))
    assert rules['TCP']['18080']['TCPForward'] == web + ':80', rules
    request('/api/docker/containers/' + web + '/exposure', {'mode': 'internal'})
    rules = json.loads(docker('exec', tailscale, 'tailscale', 'serve', 'status', '--json'))
    assert '18080' not in (rules or {}).get('TCP', {}), rules
    assert request('/api/status')['status'] == 'disabled'
    print('Authenticated Tailscale forwarding passed: HTTP over tailnet TCP, conflict protection, restart persistence, unexpose, no Cloudflare requirement', flush=True)
finally:
    for name in reversed(containers):
        subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for volume in volumes:
        subprocess.run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL)
    subprocess.run(['docker', 'network', 'rm', network], stdout=subprocess.DEVNULL)
