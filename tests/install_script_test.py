"""Installer tests with a fake Docker CLI inside a socket-free container."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as directory:
    fixture = Path(directory)
    fake = fixture / 'bin'
    fake.mkdir()
    scripts = {
        'docker': '''#!/bin/bash
printf '%s\\n' "$*" >> /fixtures/docker.log
case "$*" in
  'info') exit 0 ;;
  'compose version') test "${COMPOSE_STYLE:-plugin}" = plugin; exit $? ;;
  *'config --services') echo docktun ;;
  *'port docktun 3001') echo "${TEST_BIND:-127.0.0.1:9090}" ;;
  'exec docktun curl '*) test "${TEST_READY:-yes}" = yes; exit $? ;;
  'exec docktun cat '*) echo fake-ownership-token ;;
esac
exit 0
''',
        'docker-compose': '''#!/bin/bash
if [ "$*" = 'version --short' ]; then echo "${COMPOSE_VERSION:-2.40.3}"; exit 0; fi
exec /fixtures/bin/docker compose "$@"
''',
        'hostname': '#!/bin/sh\nexit 1\n',
        'ip': '#!/bin/sh\nexit 1\n',
        'sleep': '#!/bin/sh\nexit 0\n',
    }
    for name, source in scripts.items():
        (fake / name).write_text(source, newline="\n")
    # chmod runs only inside the isolated fixture container; no host installer executes.
    def run(expected=0, **environment):
        command = ['docker', 'run', '--rm', '-v', f'{root}:/src:ro', '-v', f'{fixture}:/fixtures',
                   '-w', '/fixtures', '-e', 'PATH=/fixtures/bin:/usr/local/bin:/usr/bin:/bin',
                   '-e', 'DOCKTUN_DIR=/fixtures/install space', '-e', 'PORT=9090']
        for key, value in environment.items():
            command.extend(['-e', f'{key}={value}'])
        command.extend(['golang:1.27', 'bash', '-c', 'chmod +x /fixtures/bin/* && bash /src/install.sh'])
        result = subprocess.run(command, text=True, capture_output=True, timeout=45)
        assert (result.returncode == 0) == (expected == 0), result.stdout + result.stderr
        return result.stdout + result.stderr
    output = run()
    assert 'http://127.0.0.1:9090' in output  # hostname/ip fallback did not abort.
    generated = fixture / 'install space' / 'compose.yml'
    config = json.loads(subprocess.check_output(['docker', 'compose', '-f', str(generated), 'config', '--format', 'json'], text=True))
    assert set(config['services']) == {'docktun'}
    assert config['name'] == 'docktun'
    assert config['services']['docktun']['ports'][0]['published'] == '9090'
    custom = generated.read_text().replace('9090:3001', '9000:3001') + '\n# user settings must survive\n'
    generated.write_text(custom)
    output = run(COMPOSE_STYLE='standalone', TEST_BIND='[::1]:9000')
    assert generated.read_text() == custom, 'installer overwrote existing Compose'
    assert 'http://[::1]:9000' in output
    calls = (fixture / 'docker.log').read_text()
    assert 'pull docktun' in calls and 'up -d --no-deps docktun' in calls
    assert '\ncompose up -d\n' not in calls
    assert 'v2 or newer' in run(expected=1, COMPOSE_STYLE='standalone', COMPOSE_VERSION='1.29.2')
    assert 'PORT must be' in run(expected=1, PORT='127.0.0.1:8080')
    assert 'did not become ready' in run(expected=1, TEST_READY='no')
    (generated.parent / 'docker-compose.yml').write_text(custom)
    assert 'Multiple Compose files' in run(expected=1)
    assert generated.read_text() == custom
print('Installer passed: existing settings retained, fresh Compose valid, exact service pull/start, Synology hostname fallback, IPv6 URL, version/port/readiness validation')
