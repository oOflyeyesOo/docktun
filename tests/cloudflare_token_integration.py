"""Full-app token-tunnel regressions in a disposable Docker engine.

Uses a TLS Cloudflare API fixture and real Tailscale daemons signed into a
disposable Headscale server. DOCKTUN_TEST_IMAGE can select an older image to
reproduce regressions before checking the fixed build.
Never uses an actual Cloudflare account, domain, API key or tailnet.
"""
import base64
import hashlib
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


def seed_tailscale_exposure(node=''):
    # Models pre-existing saved state, including legacy installations with no
    # dedicated-node field. No authenticated test endpoint changes the fixture.
    sql = "import sqlite3; c=sqlite3.connect('/data/app.db'); c.execute(\"INSERT OR REPLACE INTO container_exposures(container_name,mode,private_type,private_port,target,tailscale_node) VALUES(?,?,?,?,?,?)\",('fixture-app','private','tailscale',80,'fixture-app:8080'," + repr(node) + ")); c.commit()"
    inner('run','--rm','-v','data:/data','python:3.13-alpine','python','-c',sql)


def api_writes():
    trace = inner('exec','api-fixture','cat','/certs/requests.jsonl')
    return [item for line in trace.splitlines() if (item := json.loads(line))['method'] != 'GET']


def zone_discovery_requests():
    trace=inner('exec','api-fixture','cat','/certs/requests.jsonl')
    return [item for line in trace.splitlines() if (item:=json.loads(line))['method']=='GET' and item['path'].split('?',1)[0]=='/client/v4/zones']


def failure_diagnostics(names):
    # These are disposable fixtures only. Keep diagnostics bounded and avoid
    # full container inspect output, which would include environment settings.
    for name in dict.fromkeys(names):
        for label, args in [('state', ['inspect','--format','{{json .State}}',name]),
                            ('logs', ['logs','--tail','25',name])]:
            try:
                result=subprocess.run(['docker','exec',engine,'docker',*args],text=True,capture_output=True,timeout=15)
                output=(result.stdout+result.stderr).strip()
                print('DIAGNOSTIC '+name+' '+label+' (exit '+str(result.returncode)+'):\n'+output[-6000:],flush=True)
            except (OSError,subprocess.TimeoutExpired) as error:
                print('DIAGNOSTIC '+name+' '+label+' unavailable: '+str(error),flush=True)


public_exposure = {'mode':'public','hostname':'app.example.com','target':'http://fixture-app:8080'}
tunnel_network = 'tester-tunnel'


def saved_exposure(app='fixture-app'):
    query="import sqlite3,json; c=sqlite3.connect('/data/app.db'); c.row_factory=sqlite3.Row; r=c.execute('SELECT * FROM container_exposures WHERE container_name=?',("+repr(app)+",)).fetchone(); print(json.dumps(dict(r) if r else {}))"
    return json.loads(inner('run','--rm','-v','data:/data','python:3.13-alpine','python','-c',query))


def app_networks(app='fixture-app'):
    return set(json.loads(inner('inspect',app))[0]['NetworkSettings']['Networks'])

server = r'''
import http.server,json,ssl,pathlib,urllib.parse
root=pathlib.Path('/certs')
configuration={'originRequest':{'connectTimeout':17},'warp-routing':{'enabled':True},'ingress':[
 {'hostname':'other.example.com','service':'http://other:80','originRequest':{'httpHostHeader':'preserve'}},
 {'hostname':'app.example.com','path':'/special','service':'http://special:80'},
 {'hostname':'remote-only.example.com','service':'http://remote-only-app:8080'},
 {'hostname':'cold-only.example.com','service':'http://cold-only-app:8080'},
 {'service':'http_status:503'}]}
records=[]
next_record_id=0
(root/'requests.jsonl').touch()
def new_record(body):
 global next_record_id
 next_record_id+=1
 return dict(body,id='fixture-dns-'+str(next_record_id))
def dns_request(method,path,query,body):
 global records
 suffix=path.split('/dns_records',1)[1]
 if method=='GET' and not suffix:
  found=records
  if 'name' in query:
   name=query['name'][0].lower().rstrip('.')
   found=[record for record in found if record['name'].lower().rstrip('.')==name]
  if 'type' in query:
   found=[record for record in found if record['type'].upper()==query['type'][0].upper()]
  page=max(1,int(query.get('page',['1'])[0]));per_page=max(1,int(query.get('per_page',['100'])[0]))
  selected=found[(page-1)*per_page:page*per_page]
  return 200,{'success':True,'result':selected,'result_info':{'page':page,'per_page':per_page,'count':len(selected),'total_count':len(found),'total_pages':max(1,(len(found)+per_page-1)//per_page)}}
 if method=='POST' and suffix=='/batch':
  # Apply the API's transactional batch to records, rather than incorrectly
  # treating the whole {deletes,posts} payload as a single DNS record.
  updated=[dict(record) for record in records]
  result={'deletes':[],'patches':[],'puts':[],'posts':[]}
  for operation in ('deletes','patches','puts'):
   for change in body.get(operation,[]):
    record=next((record for record in updated if record['id']==change['id']),None)
    if record is None:return 404,{'success':False,'errors':[{'message':'DNS record not found'}]}
    if operation=='deletes':updated.remove(record);result[operation].append({'id':change['id']})
    else:
     replacement=dict(record,**change) if operation=='patches' else dict(change)
     if not all(key in replacement for key in ('name','type','content')):return 400,{'success':False,'errors':[{'message':'DNS record is missing required fields'}]}
     updated[updated.index(record)]=replacement;result[operation].append(replacement)
  for change in body.get('posts',[]):
   if not all(key in change for key in ('name','type','content')):return 400,{'success':False,'errors':[{'message':'DNS record is missing required fields'}]}
   record=new_record(change);updated.append(record);result['posts'].append(record)
  records=updated
  return 200,{'success':True,'result':result}
 if method=='POST' and not suffix:
  if not all(key in body for key in ('name','type','content')):return 400,{'success':False,'errors':[{'message':'DNS record is missing required fields'}]}
  record=new_record(body);records.append(record)
  return 200,{'success':True,'result':record}
 if suffix.startswith('/') and suffix.count('/')==1:
  record=next((record for record in records if record['id']==suffix[1:]),None)
  if record is None:return 404,{'success':False,'errors':[{'message':'DNS record not found'}]}
  if method in ('PUT','PATCH'):
   replacement=dict(record,**body) if method=='PATCH' else dict(body,id=record['id'])
   if not all(key in replacement for key in ('name','type','content')):return 400,{'success':False,'errors':[{'message':'DNS record is missing required fields'}]}
   records[records.index(record)]=replacement
   return 200,{'success':True,'result':replacement}
  if method=='DELETE':records.remove(record);return 200,{'success':True,'result':{'id':record['id']}}
  if method=='GET':return 200,{'success':True,'result':record}
 return 404,{'success':False,'errors':[{'message':'unexpected DNS fixture request'}]}
class API(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_GET(self): self.handle_api()
 def do_POST(self): self.handle_api()
 def do_PUT(self): self.handle_api()
 def do_PATCH(self): self.handle_api()
 def do_DELETE(self): self.handle_api()
 def handle_api(self):
  global configuration
  body=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))) or '{}')
  with (root/'requests.jsonl').open('a') as f: f.write(json.dumps({'method':self.command,'path':self.path,'body':body})+'\n')
  url=urllib.parse.urlsplit(self.path);path=url.path;query=urllib.parse.parse_qs(url.query)
  status=200
  authorization=self.headers.get('Authorization')
  dns_only=authorization=='Bearer fixture-dns-only-token'
  if authorization not in ('Bearer fixture-user-api-token','Bearer fixture-dns-only-token') or (root/'reject').exists():
   status=403; result={'success':False,'errors':[{'message':'Insufficient permissions'}]}
  elif dns_only and '/cfd_tunnel/' in path:
   status=403; result={'success':False,'errors':[{'message':'Insufficient account tunnel permissions; token only has zone DNS access'}]}
  elif path.endswith('/configurations') and self.command=='PUT' and (root/'reject-put').exists():
   status=403; result={'success':False,'errors':[{'message':'Insufficient permissions to edit tunnel configuration'}]}
  elif '/dns_records' in path and (root/'reject-dns').exists():
   status=403; result={'success':False,'errors':[{'message':'Insufficient zone DNS record permissions'}]}
  elif path=='/client/v4/zones' and (root/'reject-zones').exists():
   status=403; result={'success':False,'errors':[{'code':10000,'message':'Zone discovery is not permitted'}]}
  elif path.endswith('/configurations'):
   if self.command=='PUT': configuration=body['config']
   result={'success':True,'result':{'config':configuration}}
  elif '/cfd_tunnel/' in path:
   result={'success':True,'result':{'id':'fixture-token-tunnel','name':'Tester tunnel','status':'healthy','config_src':'cloudflare','connections':[{'id':'edge-connector'}]}}
  elif '/dns_records' in path:
   status,result=dns_request(self.command,path,query,body)
  elif path=='/client/v4/zones':
   zones=[{'id':'zone123','name':'example.com','status':'active'}]
   if 'name' in query:zones=[zone for zone in zones if zone['name']==query['name'][0]]
   if 'status' in query:zones=[zone for zone in zones if zone['status']==query['status'][0]]
   result={'success':True,'result':zones,'result_info':{'page':1,'per_page':100,'count':len(zones),'total_count':len(zones),'total_pages':1}}
  else: status=404; result={'success':False,'errors':[{'message':'unexpected fixture request'}]}
  encoded=json.dumps(result).encode()
  self.send_response(status);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(encoded)
  # Avoid config.json: Headscale shares these test certificates and its CLI
  # discovers config.* beside config.yaml when a config path is not explicit.
  (root/'cloudflare-config.json').write_text(json.dumps(configuration))
httpd=http.server.ThreadingHTTPServer(('0.0.0.0',443),API)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/certs/cert.pem','/certs/key.pem')
httpd.socket=ctx.wrap_socket(httpd.socket,server_side=True);httpd.serve_forever()
'''

try:
    print('Starting Cloudflare regression with image '+image+' in '+engine,flush=True)
    for fixture_image in ['python:3.13-alpine', 'alpine:3.23', 'tailscale/tailscale:latest', 'headscale/headscale:0.29.6']:
        docker('pull', fixture_image)
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
    inner('network','create',tunnel_network)
    inner('network','create','cloudflared')
    inner('network','create','docktun-shadow')
    inner('run','-d','--name','api-fixture','--network','docktun','--network-alias','api.cloudflare.com','-v','certs:/certs','python:3.13-alpine',
        'sh','-c','apk add --no-cache openssl >/dev/null && openssl req -x509 -nodes -newkey rsa:2048 -keyout /certs/key.pem -out /certs/cert.pem -days 1 -subj /CN=api.cloudflare.com -addext subjectAltName=DNS:api.cloudflare.com,DNS:headscale 2>/dev/null && exec python -u -c "$1"','sh',server)
    for _ in range(90):
        try:
            inner('exec','api-fixture','test','-s','/certs/cert.pem'); break
        except RuntimeError: time.sleep(1)
    else: raise AssertionError('TLS fixture did not start')
    inner('run','-d','--name','docktun-cloudflared','--network',tunnel_network,'alpine:3.23','sleep','3600')
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
        try: user=json.loads(inner('exec','headscale','headscale','--config','/etc/headscale/config.yaml','users','create','tester','-o','json'));break
        except RuntimeError:time.sleep(1)
    else:raise AssertionError('test Headscale did not start')
    authkey=json.loads(inner('exec','headscale','headscale','--config','/etc/headscale/config.yaml','preauthkeys','create','--user',str(user['id']),'--expiration','1h','-o','json'))['key']
    inner('run','-d','--name','docktun-tailscale','--network','docktun',
        '-v','legacy-tailscale-state:/var/lib/tailscale','-v','certs:/testcert:ro',
        '-e','SSL_CERT_FILE=/testcert/cert.pem','-e','TS_USERSPACE=true','-e','TS_STATE_DIR=/var/lib/tailscale',
        '-e','TS_AUTH_ONCE=true','tailscale/tailscale:latest')
    for _ in range(30):
        try: inner('exec','docktun-tailscale','tailscale','up','--login-server=https://headscale:443','--authkey='+authkey,'--hostname=legacy-shared','--accept-dns=false');break
        except RuntimeError:time.sleep(1)
    else:raise AssertionError('legacy Tailscale daemon did not join the disposable tailnet')
    inner('network','create','origin-only')
    inner('run','-d','--name','fixture-app','--network','origin-only','python:3.13-alpine','python','-u','-c',"from http.server import BaseHTTPRequestHandler,HTTPServer\nclass Handler(BaseHTTPRequestHandler):\n def do_GET(self): self.send_response(200);self.end_headers();self.wfile.write(b'selected-origin-ok')\nHTTPServer(('0.0.0.0',8080),Handler).serve_forever()")
    inner('network','connect','docktun-shadow','fixture-app')
    inner('run','-d','--name','remote-only-app','--network',tunnel_network,'python:3.13-alpine','sleep','3600')
    inner('run','-d','--name','cold-only-app','--network',tunnel_network,'python:3.13-alpine','sleep','3600')
    inner('run','-d','--name','docktun','--network','docktun','-p','3001:3001','-v','data:/app/data','-v','cloudflare:/etc/cloudflared','-v','certs:/test-certs:ro',
        '-v','/var/run/docker.sock:/var/run/docker.sock','-e','DOCKTUN_PREPARE_PROVIDERS=false','-e','DOCKER_NETWORK='+tunnel_network,
        '-e','SSL_CERT_FILE=/test-certs/cert.pem','-e','NO_PROXY=api.cloudflare.com','-e','HTTPS_PROXY=','-e','HTTP_PROXY=',image)
    base='http://'+docker('port',engine,'3001/tcp')
    for _ in range(90):
        try: request('/healthz'); break
        except OSError: time.sleep(1)
    secret=inner('exec','docktun','cat','/app/data/setup.token')
    token=request('/api/setup/complete',{'setup_token':secret,'admin_username':'owner','admin_password':'fixture-password-123'})['access_token']
    print('Ready: app, TLS Cloudflare fixture, origin, and disposable Tailscale control plane',flush=True)
    local='tunnel: stale-local-id\ningress:\n  - hostname: stale.example.com\n    service: http://fixture-app:9090\n  - service: http_status:404\n'
    inner('exec','-i','docktun','sh','-c','cat > /etc/cloudflared/config.yml',input=local)
    connector=base64.b64encode(json.dumps({'a':'account123','t':'fixture-token-tunnel','s':'fixture-connector-secret'}).encode()).decode()
    identity=request('/api/provider/cloudflare/credentials',{'token':connector},method='PUT')
    assert identity['tunnel_id']=='fixture-token-tunnel' and identity['account_id']=='account123'
    assert connector not in json.dumps(identity) and 'fixture-connector-secret' not in json.dumps(identity)
    unknown=request('/api/remote-access/overview')['cloudflare']
    assert unknown['tunnel_id']=='fixture-token-tunnel' and not unknown['routes_known'],unknown
    # A running connector does not grant management API permissions. Neither a
    # missing API token nor a DNS-only zone token can publish a remote route.
    writes_before=api_writes()
    networks_before=app_networks()
    failure=request('/api/docker/containers/fixture-app/exposure',public_exposure,expected=500)
    assert 'API token' in failure['error'],failure
    assert api_writes()==writes_before, 'missing API token changed Cloudflare configuration'
    assert app_networks()==networks_before, 'missing API token changed app networks'
    request('/api/cloudflare/settings',{'api_token':'fixture-dns-only-token','account_id':'account123','zone_id':'zone123'})
    failure=request('/api/docker/containers/fixture-app/exposure',public_exposure,expected=500)
    assert 'Tunnel' in failure['error'] and 'Edit' in failure['error'],failure
    assert api_writes()==writes_before, 'DNS-only API token changed Cloudflare configuration'
    assert app_networks()==networks_before, 'DNS-only API token changed app networks'
    assert request('/api/docker/containers/fixture-app/exposure')['mode']!='public'
    print('PASS: missing and DNS-only API tokens report errors without route or DNS writes',flush=True)
    # Save the API token through the same endpoint used by the visible settings form.
    inner('exec','api-fixture','touch','/certs/reject-zones')
    discoveries_before=zone_discovery_requests()
    request('/api/cloudflare/settings',{'api_token':'fixture-user-api-token','account_id':'account123','zone_id':'zone123'})
    assert request('/api/cloudflare/tunnel')['status']=='healthy'
    assert len(request('/api/cloudflare/configuration')['config']['ingress'])==5
    # Configured routes discovered from Cloudflare are registered even without
    # an exposure row, while inactive local YAML never masks the active source.
    assert request('/api/docker/containers/remote-only-app/exposure')['hostname']=='remote-only.example.com'
    assert saved_exposure('remote-only-app')=={}
    assert request('/api/docker/containers/fixture-app/exposure')['mode']=='internal'
    unpublished=next(c for c in request('/api/docker/containers') if c['name']=='fixture-app')
    assert not unpublished['connected_to_cloudflared'],unpublished
    # Reject only the configuration write so this exercises endpoint rollback
    # after preflight succeeds, preserving both original and user-added links.
    inner('exec','api-fixture','touch','/certs/reject-put')
    failure=request('/api/docker/containers/fixture-app/exposure',public_exposure,expected=500)
    assert 'permissions' in failure['error'] and app_networks()==networks_before,failure
    inner('network','connect',tunnel_network,'fixture-app')
    user_networks=app_networks()
    failure=request('/api/docker/containers/fixture-app/exposure',public_exposure,expected=500)
    assert 'permissions' in failure['error'] and app_networks()==user_networks,failure
    inner('exec','api-fixture','rm','/certs/reject-put')
    inner('network','disconnect',tunnel_network,'fixture-app')
    print('PASS: remote-only routes are registered; stale YAML and substring network names ignored; failed-save endpoint rollback preserves user networks',flush=True)
    # Seed the old exposure entry that triggers the tester's missing Serve-rule error.
    seed_tailscale_exposure()
    for _ in range(30):
        try:
            assert json.loads(inner('exec','docktun-tailscale','tailscale','serve','status','--json'))=={}; break
        except (RuntimeError,AssertionError):time.sleep(1)
    assert inner('inspect','--format','{{.State.Status}}','fixture-app')=='running'
    # The real CLI reproduces the original error when asked to remove nothing.
    try: inner('exec','docktun-tailscale','tailscale','serve','--bg','--tcp=80','off')
    except RuntimeError as error: assert 'does not exist' in str(error),error
    else: raise AssertionError('the fixture did not reproduce the tester error')
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert request('/api/docker/containers/fixture-app/exposure')['mode']=='public'
    assert app_networks()==networks_before|{tunnel_network},app_networks()
    assert 'cloudflared' not in app_networks(), 'publishing joined an unused legacy network'
    listed=next(c for c in request('/api/docker/containers') if c['name']=='fixture-app')
    inspected=request('/api/docker/containers/fixture-app')
    assert listed['connected_to_cloudflared'] and inspected['connected_to_cloudflared'],(listed,inspected)
    assert inner('exec','docktun-cloudflared','wget','-qO-','http://fixture-app:8080')=='selected-origin-ok'
    config=json.loads(inner('exec','api-fixture','cat','/certs/cloudflare-config.json'))
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
    assert zone_discovery_requests()==discoveries_before, 'saved Zone ID still attempted Zone Read discovery'
    print('PASS: corrected API token publishes route/DNS, origin is reachable, unrelated settings preserved',flush=True)
    # Regression: the old implementation tries docker exec on a stopped shared
    # daemon, so a Cloudflare save fails with "container ... is not running".
    inner('exec','docktun-tailscale','tailscale','serve','--bg','--tcp=80','tcp://fixture-app:8080')
    inner('exec','docktun-tailscale','tailscale','serve','--bg','--tcp=81','tcp://other:8080')
    shared_rules=json.loads(inner('exec','docktun-tailscale','tailscale','serve','status','--json'))['TCP']
    assert shared_rules['80']['TCPForward']=='fixture-app:8080' and shared_rules['81']['TCPForward']=='other:8080',shared_rules
    shared_identity=json.loads(inner('exec','docktun-tailscale','tailscale','status','--json'))['Self']['ID']
    seed_tailscale_exposure()
    inner('stop','docktun-tailscale')
    try: inner('exec','docktun-tailscale','tailscale','serve','status','--json')
    except RuntimeError as error: assert 'is not running' in str(error),error
    else: raise AssertionError('stopped daemon did not reproduce the tester error')
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert inner('inspect','--format','{{.State.Status}}','docktun-tailscale')=='exited'
    assert request('/api/docker/containers/fixture-app/exposure')['mode']=='public'
    print('PASS: Cloudflare publish succeeds while previous shared Tailscale provider remains stopped',flush=True)
    # Explicitly enabling the shared provider applies its deferred cleanup,
    # removing only this app's former rule and retaining unrelated routes.
    request('/api/remote-access/tailscale/control',{'action':'start'})
    for _ in range(30):
        try:
            shared_status=json.loads(inner('exec','docktun-tailscale','tailscale','status','--json'))
            if shared_status['BackendState']=='Running':break
        except RuntimeError:pass
        time.sleep(1)
    else:raise AssertionError('shared Tailscale daemon did not resume after explicit start')
    assert shared_status['Self']['ID']==shared_identity, 'shared fixture lost its persistent identity'
    shared_rules=json.loads(inner('exec','docktun-tailscale','tailscale','serve','status','--json'))['TCP']
    assert '80' not in shared_rules and shared_rules['81']['TCPForward']=='other:8080',shared_rules
    print('PASS: explicitly starting shared Tailscale cleans only the deferred app rule',flush=True)
    inner('stop','docktun-tailscale')
    # Removed legacy daemons must not be recreated or required by Cloudflare.
    inner('rm','docktun-tailscale')
    seed_tailscale_exposure()
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert 'docktun-tailscale' not in inner('ps','-a','--format','{{.Names}}').splitlines()
    print('PASS: missing legacy Tailscale provider is not recreated by Cloudflare publish',flush=True)

    # An owned per-app helper can also have been stopped outside DockTun. Its
    # persistent Serve rule must not block publishing or return after reconnect.
    node='docktun-ts-'+hashlib.sha256(b'fixture-app').hexdigest()[:20]
    label='io.docktun.tailscale-app=fixture-app'
    inner('network','create','--label',label,node)
    inner('volume','create','--label',label,node+'-state')
    inner('network','connect',node,'fixture-app')
    inner('run','-d','--name',node,'--label',label,'--network',node,
        '-v',node+'-state:/var/lib/tailscale','-v','certs:/testcert:ro','-e','SSL_CERT_FILE=/testcert/cert.pem',
        '--entrypoint','tailscaled','tailscale/tailscale:latest','--tun=userspace-networking','--state=/var/lib/tailscale/tailscaled.state')
    inner('network','connect','docktun',node)
    helper_key=json.loads(inner('exec','headscale','headscale','--config','/etc/headscale/config.yaml','preauthkeys','create','--user',str(user['id']),'--expiration','1h','-o','json'))['key']
    for _ in range(30):
        try:
            inner('exec',node,'tailscale','up','--login-server=https://headscale:443','--authkey='+helper_key,
                '--hostname=fixture-app','--accept-dns=false','--accept-routes=false');break
        except RuntimeError:time.sleep(1)
    else:raise AssertionError('owned Tailscale helper did not join the disposable tailnet')
    helper_identity=json.loads(inner('exec',node,'tailscale','status','--json'))['Self']['ID']
    inner('exec',node,'tailscale','serve','--bg','--tcp=80','tcp://fixture-app:8080')
    # An active helper is stopped while leaving Tailscale. Failed Cloudflare
    # writes must restart it, wait for its local socket, and restore the route.
    seed_tailscale_exposure(node)
    inner('exec','api-fixture','touch','/certs/reject')
    failure=request('/api/docker/containers/fixture-app/exposure',public_exposure,expected=500)
    assert 'permissions' in failure['error'],failure
    assert inner('inspect','--format','{{.State.Status}}',node)=='running'
    assert node in json.loads(inner('inspect','fixture-app'))[0]['NetworkSettings']['Networks']
    active_rules=json.loads(inner('exec',node,'tailscale','serve','status','--json'))['TCP']
    assert active_rules['80']['TCPForward']=='fixture-app:8080',active_rules
    assert saved_exposure()['mode']=='private'
    projected=request('/api/docker/containers/fixture-app/exposure')
    assert projected['mode']=='public' and projected['private_type']=='tailscale',projected
    inner('exec','api-fixture','rm','/certs/reject')
    print('PASS: rejected Cloudflare save restores running helper and its actual Serve route',flush=True)
    inner('stop',node)
    seed_tailscale_exposure(node)
    # A rejected Cloudflare save rolls back deferred cleanup/network changes;
    # it does not start the unused helper or lose the previous saved exposure.
    inner('exec','api-fixture','touch','/certs/reject')
    failure=request('/api/docker/containers/fixture-app/exposure',public_exposure,expected=500)
    assert 'permissions' in failure['error'],failure
    assert inner('inspect','--format','{{.State.Status}}',node)=='exited'
    assert node in json.loads(inner('inspect','fixture-app'))[0]['NetworkSettings']['Networks']
    previous=request('/api/docker/containers/fixture-app/exposure')
    assert saved_exposure()['mode']=='private' and previous['mode']=='public' and previous['tailscale_node']==node,previous
    print('PASS: rejected Cloudflare save retains stopped helper, private bridge, and previous exposure',flush=True)
    inner('exec','api-fixture','rm','/certs/reject')
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert inner('inspect','--format','{{.State.Status}}',node)=='exited'
    assert node not in json.loads(inner('inspect','fixture-app'))[0]['NetworkSettings']['Networks']
    assert inner('exec','docktun-cloudflared','wget','-qO-','http://fixture-app:8080')=='selected-origin-ok'
    print('PASS: successful publish detaches dormant app bridge and keeps its Tailscale helper stopped',flush=True)
    # Cleanup is durable, rather than an in-memory task lost on a core update.
    inner('restart','docktun')
    for _ in range(60):
        try:
            request('/healthz');break
        except OSError:time.sleep(1)
    else:raise AssertionError('DockTun did not restart')
    # Disable a remote-only app before warming the new process's inventory
    # snapshot. Mutation lookup must be authoritative even with a cold cache.
    assert saved_exposure('cold-only-app')=={}
    request('/api/docker/containers/cold-only-app/exposure',{'mode':'internal'})
    cold_routes=request('/api/cloudflare/configuration')['config']['ingress']
    assert not any(rule.get('hostname')=='cold-only.example.com' for rule in cold_routes),cold_routes
    assert saved_exposure('cold-only-app')['mode']=='internal'
    print('PASS: cold-cache disable removes a remote-only route before recording internal access',flush=True)
    for _ in range(60):
        try:
            assert request('/api/docker/containers/fixture-app/exposure')['mode']=='public';break
        except (OSError,AssertionError):time.sleep(1)
    else:raise AssertionError('DockTun did not recover its saved exposure after restart')
    assert inner('inspect','--format','{{.State.Status}}',node)=='exited'
    status=request('/api/docker/containers/fixture-app/tailscale',{})
    assert status['state']=='Running',status
    assert json.loads(inner('exec',node,'tailscale','status','--json'))['Self']['ID']==helper_identity
    assert not json.loads(inner('exec',node,'tailscale','serve','status','--json')).get('TCP'), 'stale private rule returned on reconnect'
    print('PASS: cleanup survives DockTun restart; explicit reconnect retains identity without stale Serve rules',flush=True)
    # Missing owned helpers are equally optional; their state volumes remain.
    inner('rm','-f',node)
    seed_tailscale_exposure(node)
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert node not in inner('ps','-a','--format','{{.Names}}').splitlines()
    assert inner('volume','inspect','--format','{{.Name}}',node+'-state')==node+'-state'
    print('PASS: missing owned helper is not recreated and its identity volume is retained',flush=True)
    # Permission failures must not produce a successful save or overwrite routes.
    inner('exec','api-fixture','touch','/certs/reject')
    failure=request('/api/docker/containers/fixture-app/exposure',{'mode':'public','hostname':'failed.example.com','target':'http://fixture-app:80'},expected=500)
    assert 'permissions' in failure['error'],failure
    assert request('/api/docker/containers/fixture-app/exposure')['hostname']=='app.example.com'
    inner('exec','api-fixture','rm','/certs/reject')
    # A route accepted by Cloudflare remains visible if the separate zone DNS
    # permission fails. The API must identify this partial result explicitly.
    inner('network','disconnect',tunnel_network,'fixture-app')
    partial_networks_before=app_networks()
    inner('exec','api-fixture','touch','/certs/reject-dns')
    failure=request('/api/docker/containers/fixture-app/exposure',{'mode':'public','hostname':'partial.example.com','target':'http://fixture-app:8080'},expected=500)
    assert 'hostname route saved' in failure['error'] and 'Zone DNS' in failure['error'],failure
    assert 'Insufficient zone DNS record permissions' in failure['error'] and 'HTTP 403' in failure['error'],failure
    assert 'Account DNS Settings Write does not grant DNS record access' in failure['error'] and 'Zone:Zone:Read' in failure['error'],failure
    assert app_networks()==partial_networks_before|{tunnel_network}, 'accepted route lost its newly needed connector endpoint after DNS failure'
    assert 'cloudflared' not in app_networks(), 'partial DNS failure joined unused legacy network'
    assert saved_exposure()['hostname']=='app.example.com'
    projected=request('/api/docker/containers/fixture-app/exposure')
    assert projected['mode']=='public' and projected['hostname']=='partial.example.com',projected
    listed=next(c for c in request('/api/docker/containers') if c['name']=='fixture-app')
    inspected=request('/api/docker/containers/fixture-app')
    assert listed['hostname']=='partial.example.com' and inspected['hostname']=='partial.example.com',(listed,inspected)
    inner('exec','api-fixture','rm','/certs/reject-dns')
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert zone_discovery_requests()==discoveries_before, 'explicit-zone publication required Zone Read'
    # Without a Zone ID, discovery denial prioritizes entering that ID; no
    # additional Zone Read permission is required when it is supplied.
    # Model an installation with no saved Zone ID. The settings endpoint keeps
    # existing credential fields when a caller omits/empties them.
    clear_zone="import sqlite3; c=sqlite3.connect('/data/app.db'); c.execute(\"UPDATE system_settings SET value='' WHERE key='cf_zone_id'\"); c.commit()"
    inner('run','--rm','-v','data:/data','python:3.13-alpine','python','-c',clear_zone)
    failure=request('/api/docker/containers/fixture-app/exposure',{'mode':'public','hostname':'needs-zone.example.com','target':'http://fixture-app:8080'},expected=500)
    assert "enter the domain's Zone ID in API credentials, or optionally grant Zone Read" in failure['error'],failure
    assert 'Zone discovery is not permitted' in failure['error'] and 'HTTP 403' in failure['error'] and 'Cloudflare code 10000' in failure['error'],failure
    assert saved_exposure()['hostname']=='app.example.com'
    assert request('/api/docker/containers/fixture-app/exposure')['hostname']=='needs-zone.example.com'
    request('/api/cloudflare/settings',{'api_token':'fixture-user-api-token','account_id':'account123','zone_id':'zone123'})
    discoveries_after_denial=zone_discovery_requests()
    request('/api/docker/containers/fixture-app/exposure',public_exposure)
    assert zone_discovery_requests()==discoveries_after_denial, 'entering Zone ID did not stop discovery requests'
    # Disabling a discovered remote-only app removes its selected actual route
    # even though it had never been saved in DockTun's exposure table.
    request('/api/docker/containers/remote-only-app/exposure',{'mode':'internal'})
    remote_config=request('/api/cloudflare/configuration')['config']['ingress']
    assert not any(rule.get('hostname')=='remote-only.example.com' for rule in remote_config),remote_config
    assert request('/api/docker/containers/remote-only-app/exposure')['mode']=='internal'
    assert tunnel_network in app_networks('remote-only-app'), 'disabling a route removed an existing user network'
    print('PASS: accepted route remains visible after DNS permission failure; disabling discovered route updates Cloudflare without deleting user networks',flush=True)
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
    print('Cloudflare token integration passed: token identity, missing/DNS-only token failures and corrected-token recovery, healthy remote status, API route/DNS writes, connector-to-app HTTP reachability, preserved unrelated settings, running/stopped/missing legacy and owned Tailscale switching, persistent identity without stale Serve rules, permission failures and local-YAML compatibility',flush=True)
    passed = True
    if os.environ.get('DOCKTUN_KEEP_TEST_ENGINE')=='1':
        Path('../../outputs/cloudflare-test-preview.json').write_text(json.dumps({'engine':engine,'url':base,'username':'owner','password':'fixture-password-123'},indent=2))
except Exception:
    failure_diagnostics(['docktun','docktun-tailscale','headscale']+([node] if 'node' in globals() else []))
    raise
finally:
    if not passed or os.environ.get('DOCKTUN_KEEP_TEST_ENGINE')!='1':
        subprocess.run(['docker','rm','-f','-v',engine],stdout=subprocess.DEVNULL,check=False)
