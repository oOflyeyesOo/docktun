"""Disposable real PostgreSQL/app image-update and rollback acceptance test."""
import json,os,pathlib,subprocess,tempfile,time,urllib.error,urllib.request,uuid
prefix='docktun-db-update-'+uuid.uuid4().hex[:9]
image=os.environ.get('DOCKTUN_TEST_IMAGE','docktun:integrity')
containers=[];volumes=[];networks=[];images=[]
def docker(*args,input=None):
 return subprocess.check_output(['docker',*args],text=True,input=input).strip()
def inspect(name):return json.loads(docker('inspect',name))[0]
def wait(fn,seconds=120):
 deadline=time.monotonic()+seconds
 while True:
  try:return fn()
  except (OSError,AssertionError,subprocess.CalledProcessError):
   if time.monotonic()>deadline:raise
   time.sleep(1)
try:
 registry=prefix+'-registry';containers.append(registry)
 registry_port=25000+int(uuid.uuid4().hex[:4],16)%20000
 registry_url='127.0.0.1:'+str(registry_port)
 docker('run','-d','--name',registry,'--network','host','-e','REGISTRY_HTTP_ADDR='+registry_url,'registry:2')
 wait(lambda:docker('exec',registry,'wget','-qO-','http://'+registry_url+'/v2/'))
 dbnet,ingress=prefix+'-db',prefix+'-ingress'
 for net in (dbnet,ingress):docker('network','create',net);networks.append(net)
 core,app,db=prefix+'-core',prefix+'-app',prefix+'-postgres'
 data,pgdata,appdata=prefix+'-core-data',prefix+'-pgdata',prefix+'-appdata'
 for volume in (data,pgdata,appdata):docker('volume','create',volume);volumes.append(volume)
 containers.append(core)
 docker('run','-d','--name',core,'-p','127.0.0.1::3001','-v',data+':/app/data','-v','/var/run/docker.sock:/var/run/docker.sock','-e','DOCKTUN_PREPARE_PROVIDERS=false',image)
 base='http://'+docker('port',core,'3001/tcp');token=''
 def request(path,body=None,expected=200):
  headers={'Content-Type':'application/json'}
  if token:headers['Authorization']='Bearer '+token
  req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers=headers)
  try:response=urllib.request.urlopen(req,timeout=180)
  except urllib.error.HTTPError as e:response=e
  with response:
   raw=response.read();assert response.status==expected,(path,response.status,raw[:400]);return json.loads(raw) if raw and response.headers.get_content_type()=='application/json' else raw
 wait(lambda:request('/healthz'))
 secret=docker('exec',core,'cat','/app/data/setup.token')
 token=request('/api/setup/complete',{'setup_token':secret,'admin_username':'owner','admin_password':'fixture-password-123'})['access_token']
 dbtag=registry_url+'/'+prefix+'/postgres:current';apptag=registry_url+'/'+prefix+'/app:current';images += [dbtag,apptag]
 docker('build','-t',dbtag,'-',input='FROM postgres:17-alpine\nLABEL fixture.version="old"\n');docker('push',dbtag)
 containers.append(db)
 docker('run','-d','--name',db,'--network',dbnet,'--network-alias','postgres','-e','POSTGRES_PASSWORD=fixture-db-password','-e','POSTGRES_DB=fixture','-v',pgdata+':/var/lib/postgresql/data',dbtag)
 wait(lambda:docker('exec',db,'pg_isready','-U','postgres','-d','fixture'))
 docker('exec',db,'psql','-U','postgres','-d','fixture','-c',"CREATE TABLE login_marker(value text); INSERT INTO login_marker VALUES ('persistent-login');")
 with tempfile.TemporaryDirectory() as tmp:
  root=pathlib.Path(tmp)
  (root/'server.py').write_text('''from http.server import BaseHTTPRequestHandler,HTTPServer
import subprocess
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  try:
   value=subprocess.check_output(['psql','-Atqc','SELECT value FROM login_marker'],text=True).strip()
   raw=(open('/version').read().strip()+':'+value).encode();self.send_response(200);self.end_headers();self.wfile.write(raw)
  except subprocess.CalledProcessError:self.send_response(503);self.end_headers()
HTTPServer(('0.0.0.0',8080),Handler).serve_forever()
''')
  (root/'entry').write_bytes(b'#!/bin/sh\nexec python3 /server.py\n')
  dockerfile='FROM postgres:17-alpine\nRUN apk add --no-cache python3\nCOPY server.py /server.py\nCOPY entry /fixture-entry\nRUN chmod 755 /fixture-entry\nENTRYPOINT ["/fixture-entry"]\nHEALTHCHECK --interval=1s --timeout=3s --start-period=5s CMD python3 -c "import urllib.request; urllib.request.urlopen(\'http://127.0.0.1:8080/login\')"\n'
  def build(version,bad=False):
   (root/'Dockerfile').write_text(dockerfile+'RUN echo '+version+' > /version\n'+('RUN rm /fixture-entry\n' if bad else ''))
   docker('build','-t',apptag,str(root));docker('push',apptag)
  build('v1')
  containers.append(app)
  docker('run','-d','--name',app,'--network',ingress,'--network-alias','web-app','-p','127.0.0.1::8080','-v',appdata+':/appdata','-e','PGHOST=postgres','-e','PGUSER=postgres','-e','PGDATABASE=fixture','-e','PGPASSWORD=fixture-db-password',apptag)
  docker('network','connect','--alias','database-client',dbnet,app)
  def login():
   url='http://'+docker('port',app,'8080/tcp')+'/login'
   with urllib.request.urlopen(url,timeout=5) as r:return r.read().decode()
  assert wait(login)=='v1:persistent-login'
  before=inspect(app);build('v2')
  request('/api/docker/containers/'+before['Id']+'/update',{})
  after=inspect(app);assert after['Id']!=before['Id'];assert wait(login)=='v2:persistent-login'
  for key in ('Env','Entrypoint','Cmd'):assert before['Config'][key]==after['Config'][key],key
  assert before['HostConfig']['RestartPolicy']==after['HostConfig']['RestartPolicy']
  assert {m['Destination']:m.get('Name',m['Source']) for m in before['Mounts']}=={m['Destination']:m.get('Name',m['Source']) for m in after['Mounts']}
  assert set(before['NetworkSettings']['Networks'])==set(after['NetworkSettings']['Networks'])
  assert 'database-client' in after['NetworkSettings']['Networks'][dbnet]['Aliases']
  print('App update passed: real PostgreSQL login, database network/aliases, volume, environment, entrypoint and restart policy retained',flush=True)
  # Database image change within the same major must retain its original data and DNS identity.
  dbbefore=inspect(db);docker('build','-t',dbtag,'-',input='FROM postgres:17-alpine\nLABEL fixture.version="new"\n');docker('push',dbtag)
  request('/api/docker/containers/'+dbbefore['Id']+'/update',{})
  wait(lambda:docker('exec',db,'pg_isready','-U','postgres','-d','fixture'))
  assert wait(login)=='v2:persistent-login'
  assert next(m['Name'] for m in inspect(db)['Mounts'] if m['Destination']=='/var/lib/postgresql/data')==pgdata
  print('Database update passed: existing rows, data volume, postgres DNS alias and application login retained',flush=True)
  # A candidate advertising another database major is refused before stop/rename.
  # Reuse the cached base to keep this regression independent of registry limits.
  dbgood=inspect(db)
  docker('build','-t',dbtag,'-',input='FROM postgres:17-alpine\nENV PG_MAJOR=18\nLABEL fixture.version="migration-required"\n');docker('push',dbtag)
  request('/api/docker/containers/'+dbgood['Id']+'/update',{},expected=500)
  assert inspect(db)['Id']==dbgood['Id'];assert wait(login)=='v2:persistent-login'
  print('Database migration guard passed: changed-major candidate refused before replacing or stopping the working database',flush=True)
  # Bad candidate: original ID and database connectivity survive rollback.
  good=inspect(app);build('broken',bad=True)
  request('/api/docker/containers/'+good['Id']+'/update',{},expected=500)
  assert inspect(app)['Id']==good['Id'];assert wait(login)=='v2:persistent-login'
  print('Failed app update passed: original container and working database login restored',flush=True)
finally:
 for name in reversed(containers):subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 for net in networks:subprocess.run(['docker','network','rm',net],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 for volume in volumes:subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 for tag in images:subprocess.run(['docker','image','rm',tag],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
