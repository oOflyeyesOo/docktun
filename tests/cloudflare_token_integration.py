"""Full-app token-tunnel regressions in a disposable Docker engine.

Uses a TLS Cloudflare API fixture and a real, unsigned-in Tailscale daemon.
Never uses an actual Cloudflare account, domain, API key or tailnet.
"""
import base64
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

engine = 'docktun-cf-regression-' + uuid.uuid4().hex[:10]
image = os.environ.get('DOCKTUN_TEST_IMAGE', 'docktun:cloudflare-regressions')
base, token = '', ''
passed = False

def docker(*args, input=None, timeout=240):
    result = subprocess.run(['docker', *map(str, args)], input=input, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(result.stderr[:2000])
    return result.stdout.strip()

def inner(*args, input=None):
    return docker('exec', '-i', engine, 'docker', *args, input=input)

def request(path, body=None, expected=200, method=None):
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), method=method,
        headers={'Content-Type':'application/json', 'Authorization':'Bearer '+token})
    try:
        response = urllib.request.urlopen(req, timeout=45)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        payload = response.read()
        assert response.status == expected, (path, response.status, payload[:1000])
        return json.loads(payload) if payload and response.headers.get_content_type() == 'application/json' else payload

server = r'''
import http.server,json,ssl,pathlib
root=pathlib.Path('/certs')
configuration={'originRequest':{'connectTimeout':17},'warp-routing':{'enabled':True},'ingress':[
 {'hostname':'other.example.com','service':'http://other:80','originRequest':{'httpHostHeader':'preserve'}},
 {'hostname':'app.example.com','path':'/special','service':'http://special:80'},
 {'service':'http_status:503'}]}
records=[]
class API(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_GET(self): self.handle_api()
 def do_POST(self): self.handle_api()
 def do_PUT(self): self.handle_api()
 def handle_api(self):
  global configuration
  body=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))) or '{}')
  with (root/'requests.jsonl').open('a') as f: f.write(json.dumps({'method':self.command,'path':self.path,'body':body})+'\n')
  status=200
  if self.headers.get('Authorization')!='Bearer fixture-user-api-token' or (root/'reject').exists():
   status=403; result={'success':False,'errors':[{'message':'Insufficient permissions'}]}
  elif self.path.endswith('/configurations'):
   if self.command=='PUT': configuration=body['config']
   result={'success':True,'result':{'config':configuration}}
  elif '/cfd_tunnel/' in self.path:
   result={'success':True,'result':{'id':'fixture-token-tunnel','name':'Tester tunnel','status':'healthy','config_src':'cloudflare','connections':[{'id':'edge-connector'}]}}
  elif '/dns_records' in self.path:
   if self.command=='POST': records.append(dict(body,id='fixture-dns'))
   result={'success':True,'result': records if self.command=='GET' else records[-1]}
  elif '/zones' in self.path:
   result={'success':True,'result':[{'id':'zone123','name':'example.com'}]}
  else: status=404; result={'success':False,'errors':[{'message':'unexpected fixture request'}]}
  encoded=json.dumps(result).encode()
  self.send_response(status);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(encoded)
  (root/'config.json').write_text(json.dumps(configuration))
httpd=http.server.ThreadingHTTPServer(('0.0.0.0',443),API)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/certs/cert.pem','/certs/key.pem')
httpd.socket=ctx.wrap_socket(httpd.socket,server_side=True);httpd.serve_forever()
'''

try:
    docker('pull', 'python:3.13-alpine')
    docker('run', '-d', '--privileged', '--name', engine, '-e', 'DOCKER_TLS_CERTDIR=', '-p', '127.0.0.1::3001', os.environ.get('DOCKTUN_TEST_ENGINE_IMAGE','docker:29-dind'))
    for _ in range(90):
        try: inner('info'); break
        except RuntimeError: time.sleep(1)
    else: raise AssertionError('test engine did not become ready')
    with tempfile.TemporaryDirectory() as directory:
        archive=Path(directory)/'images.tar'
        docker('save','-o',archive,image,'alpine:3.23','tailscale/tailscale:latest','python:3.13-alpine','headscale/headscale:0.29.6')
        with archive.open('rb') as stream:
            result=subprocess.run(['docker','exec','-i',engine,'docker','load'],stdin=stream,capture_output=True,timeout=240)
            assert result.returncode==0,result.stderr[:1000]
    inner('network','create','docktun')
    inner('run','-d','--name','api-fixture','--network','docktun','--network-alias','api.cloudflare.com','-v','certs:/certs','python:3.13-alpine',
        'sh','-c','apk add --no-cache openssl >/dev/null && openssl req -x509 -nodes -newkey rsa:2048 -keyout /certs/key.pem -out /certs/cert.pem -days 1 -subj /CN=api.cloudflare.com -addext subjectAltName=DNS:api.cloudflare.com,DNS:headscale 2>/dev/null && exec python -u -c "$1"','sh',server)
    for _ in range(90):
        try:
            inner('exec','api-fixture','test','-s','/certs/cert.pem'); break
        except RuntimeError: time.sleep(1)
    else: raise AssertionError('TLS fixture did not start')
    inner('run','-d','--name','docktun-cloudflared','--network','docktun','alpine:3.23','sleep','3600')
    headscale_config = {
        'server_url':'https://headscale:443', 'listen_addr':'0.0.0.0:443',
        'tls_cert_path':'/etc/headscale/cert.pem','tls_key_path':'/etc/headscale/key.pem',
        'noise':{'private_key_path':'/var/lib/headscale/noise.key'},
        'prefixes':{'v4':'100.64.0.0/10','v6':'fd7a:115c:a1e0::/48'},
        'database':{'type':'sqlite','sqlite':{'path':'/var/lib/headscale/db.sqlite'}},
        'dns':{'magic_dns':True,'base_domain':'docktun-test.internal','override_local_dns':False},
        'derp':{'urls':['https://controlplane.tailscale.com/derpmap/default'],'auto_update_enabled':False},
        'disable_check_updates':True,'unix_socket':'/var/lib/headscale/headscale.sock',
    }
    inner('exec','-i','api-fixture','python','-c',"import sys,pathlib; pathlib.Path('/certs/config.yaml').write_text(sys.stdin.read())",input=json.dumps(headscale_config))
    inner('run','-d','--name','headscale','--network','docktun','-v','certs:/etc/headscale','-v','headscale-data:/var/lib/headscale','headscale/headscale:0.29.6','serve')
    for _ in range(30):
        try: user=json.loads(inner('exec','headscale','headscale','users','create','tester','-o','json'));break
        except RuntimeError:time.sleep(1)
    else:raise AssertionError('test Headscale did not start')
    authkey=json.loads(inner('exec','headscale','headscale','preauthkeys','create','--user',str(user['id']),'--expiration','1h','-o','json'))['key']
    inner('run','-d','--name','docktun-tailscale','--network','docktun','-v','certs:/testcert:ro','-e','SSL_CERT_FILE=/testcert/cert.pem','-e','TS_USERSPACE=true','tailscale/tailscale:latest')
    for _ in range(30):
        try: inner('exec','docktun-tailscale','tailscale','up','--login-server=https://headscale:443','--authkey='+authkey,'--hostname=legacy-shared','--accept-dns=false');break
        except RuntimeError:time.sleep(1)
    else:raise AssertionError('legacy Tailscale daemon did not join the disposable tailnet')
    inner('network','create','origin-only')
    inner('run','-d','--name','fixture-app','--network','origin-only','python:3.13-alpine','python','-u','-c',"from http.server import BaseHTTPRequestHandler,HTTPServer\nclass Handler(BaseHTTPRequestHandler):\n def do_GET(self): self.send_response(200);self.end_headers();self.wfile.write(b'selected-origin-ok')\nHTTPServer(('0.0.0.0',8080),Handler).serve_forever()")
    inner('run','-d','--name','docktun','--network','docktun','-p','3001:3001','-v','data:/app/data','-v','cloudflare:/etc/cloudflared','-v','certs:/test-certs:ro',
        '-v','/var/run/docker.sock:/var/run/docker.sock','-e','DOCKTUN_PREPARE_PROVIDERS=false','-e','DOCKER_NETWORK=docktun',
        '-e','SSL_CERT_FILE=/test-certs/cert.pem','-e','NO_PROXY=api.cloudflare.com','-e','HTTPS_PROXY=','-e','HTTP_PROXY=',image)
    base='http://'+docker('port',engine,'3001/tcp')
    for _ in range(90):
        try: request('/healthz'); break
        except OSError: time.sleep(1)
    secret=inner('exec','docktun','cat','/app/data/setup.token')
    token=request('/api/setup/complete',{'setup_token':secret,'admin_username':'owner','admin_password':'fixture-password-123'})['access_token']
    local='tunnel: stale-local-id\ningress:\n  - service: http_status:404\n'
    inner('exec','-i','docktun','sh','-c','cat > /etc/cloudflared/config.yml',input=local)
    connector=base64.b64encode(json.dumps({'a':'account123','t':'fixture-token-tunnel','s':'fixture-connector-secret'}).encode()).decode()
    identity=request('/api/provider/cloudflare/credentials',{'token':connector},method='PUT')
    assert identity['tunnel_id']=='fixture-token-tunnel' and identity['account_id']=='account123'
    assert connector not in json.dumps(identity) and 'fixture-connector-secret' not in json.dumps(identity)
    unknown=request('/api/remote-access/overview')['cloudflare']
    assert unknown['tunnel_id']=='fixture-token-tunnel' and not unknown['routes_known'],unknown
    # Save the API token through the same endpoint used by the visible settings form.
    request('/api/cloudflare/settings',{'api_token':'fixture-user-api-token','account_id':'account123','zone_id':'zone123'})
    assert request('/api/cloudflare/tunnel')['status']=='healthy'
    assert len(request('/api/cloudflare/configuration')['config']['ingress'])==3
    # Seed the old exposure entry that triggers the tester's missing Serve-rule error.
    sql="import sqlite3; c=sqlite3.connect('/data/app.db'); c.execute(\"INSERT OR REPLACE INTO container_exposures(container_name,mode,private_type,private_port,target) VALUES(?,?,?,?,?)\",('fixture-app','private','tailscale',80,'fixture-app:80')); c.commit()"
    inner('run','--rm','-v','data:/data','python:3.13-alpine','python','-c',sql)
    for _ in range(30):
        try:
            assert json.loads(inner('exec','docktun-tailscale','tailscale','serve','status','--json'))=={}; break
        except (RuntimeError,AssertionError):time.sleep(1)
    assert inner('inspect','--format','{{.State.Status}}','fixture-app')=='running'
    # The real CLI reproduces the original error when asked to remove nothing.
    try: inner('exec','docktun-tailscale','tailscale','serve','--bg','--tcp=80','off')
    except RuntimeError as error: assert 'does not exist' in str(error),error
    else: raise AssertionError('the fixture did not reproduce the tester error')
    request('/api/docker/containers/fixture-app/exposure',{'mode':'public','hostname':'app.example.com','target':'http://fixture-app:8080'})
    assert request('/api/docker/containers/fixture-app/exposure')['mode']=='public'
    assert inner('exec','docktun-cloudflared','wget','-qO-','http://fixture-app:8080')=='selected-origin-ok'
    config=json.loads(inner('exec','api-fixture','cat','/certs/config.json'))
    assert config['ingress'][0]['originRequest']['httpHostHeader']=='preserve'
    assert config['ingress'][1]['path']=='/special'
    assert config['ingress'][-1]['service']=='http_status:503'
    assert config['ingress'][-2]['service']=='http://fixture-app:8080'
    assert config['originRequest']['connectTimeout']==17 and config['warp-routing']['enabled']
    assert inner('exec','docktun','cat','/etc/cloudflared/config.yml')==local.strip()
    assert json.loads(inner('exec','docktun-tailscale','tailscale','serve','status','--json'))=={}
    trace=inner('exec','api-fixture','cat','/certs/requests.jsonl')
    assert 'fixture-token-tunnel.cfargotunnel.com' in trace
    assert 'stale-local-id' not in trace
    # Permission failures must not produce a successful save or overwrite routes.
    inner('exec','api-fixture','touch','/certs/reject')
    failure=request('/api/docker/containers/fixture-app/exposure',{'mode':'public','hostname':'failed.example.com','target':'http://fixture-app:80'},expected=500)
    assert 'permissions' in failure['error'],failure
    assert request('/api/docker/containers/fixture-app/exposure')['hostname']=='app.example.com'
    inner('exec','api-fixture','rm','/certs/reject')
    # Switching to local credentials retains the working YAML editor path.
    request('/api/provider/cloudflare/credentials',{'token':''},method='PUT')
    request('/api/control/stop',{})
    request('/api/config',{'content':local},method='PUT')
    request('/api/docker/containers/fixture-app/exposure',{'mode':'public','hostname':'local.example.com','target':'http://fixture-app:8080'})
    assert 'local.example.com' in inner('exec','docktun','cat','/etc/cloudflared/config.yml')
    assert inner('inspect','--format','{{.State.Status}}','docktun-cloudflared')=='exited'
    request('/api/provider/cloudflare/credentials',{'token':connector},method='PUT')
    time.sleep(6) # Overview intentionally caches for five seconds.
    summary=request('/api/remote-access/overview')['cloudflare']
    assert summary['routes_known'] and summary['ingress_count']==3 and summary['tunnel_id']=='fixture-token-tunnel',summary
    print('Cloudflare token integration passed: detected token identity, API credentials, healthy remote status, actual API route/DNS writes, connector-to-app HTTP reachability, preserved unrelated settings, legacy empty Tailscale switch, permission failures and local-YAML compatibility')
    passed = True
    if os.environ.get('DOCKTUN_KEEP_TEST_ENGINE')=='1':
        Path('../../outputs/cloudflare-test-preview.json').write_text(json.dumps({'engine':engine,'url':base,'username':'owner','password':'fixture-password-123'},indent=2))
finally:
    if not passed or os.environ.get('DOCKTUN_KEEP_TEST_ENGINE')!='1':
        subprocess.run(['docker','rm','-f','-v',engine],stdout=subprocess.DEVNULL,check=False)
