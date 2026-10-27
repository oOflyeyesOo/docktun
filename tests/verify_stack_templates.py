"""Validate bundled templates without starting apps or needing any tunnel network."""
import json
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
script = "import {defaultComposeExample,composeTemplates} from './src/lib/compose-templates.mjs'; process.stdout.write(JSON.stringify([{id:'default',compose:defaultComposeExample,ports:['8080:80']},...composeTemplates]));"
templates = json.loads(subprocess.check_output(['docker','run','--rm','-v',f'{root / "frontend"}:/app:ro','-w','/app','node:24-alpine','node','--input-type=module','-e',script],text=True))
for template in templates:
    result = subprocess.run(['docker','compose','-p','docktun-template-test','-f','-','config','--format','json'], input=template['compose'], text=True, capture_output=True, check=True)
    config = json.loads(result.stdout)
    assert set(config['networks']) == {'default'}, template['id']
    assert not config['networks']['default'].get('external'), template['id']
    published = set()
    for service in config['services'].values():
        assert set(service['networks']) == {'default'}, template['id']
        for port in service.get('ports', []):
            published.add(f"{port['published']}:{port['target']}")
    assert published == set(template['ports']), (template['id'], published, template['ports'])
print(f'{len(templates)} stack templates passed: valid Compose, private default networks, accurate published-port hints, no provider prerequisites')
