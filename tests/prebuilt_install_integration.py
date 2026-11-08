"""Single prebuilt Compose file on an empty, uniquely named disposable engine."""
import json,os,pathlib,subprocess,tempfile,time,urllib.error,urllib.request,uuid
root=pathlib.Path(__file__).resolve().parents[1];engine='docktun-install-test-'+uuid.uuid4().hex[:10]
image=os.environ.get('DOCKTUN_TEST_IMAGE','docktun:test');installer=os.environ.get('DOCKTUN_INSTALLER')
def docker(*args,input=None):return subprocess.check_output(['docker',*map(str,args)],input=input,text=True,stderr=subprocess.STDOUT).strip()
def inner(*args):return docker('exec',engine,'docker',*args)
def wait(fn,seconds=180):
 end=time.monotonic()+seconds
 while True:
  try:return fn()
  except (OSError,AssertionError,subprocess.CalledProcessError):
   if time.monotonic()>end:raise
   time.sleep(1)
try:
 docker('run','-d','--privileged','--name',engine,'-e','DOCKER_TLS_CERTDIR=','-p','127.0.0.1::8080','docker:29-dind')
 wait(lambda:inner('info'))
 if installer:
  compose=pathlib.Path(installer).read_text(encoding='utf-8')
 else:
  # The candidate is not published during PR validation. Load it locally and
  # substitute only its image references; release runs consume the real asset.
  with tempfile.TemporaryDirectory() as directory:
   archive=pathlib.Path(directory)/'image.tar';docker('save','-o',archive,image)
   with archive.open('rb') as stream:subprocess.run(['docker','exec','-i',engine,'docker','load'],stdin=stream,check=True,stdout=subprocess.DEVNULL)
  candidate=inner('image','inspect','--format','{{.Id}}',image)
  compose=(root/'compose.prebuilt.yml').read_text(encoding='utf-8').replace('ghcr.io/ooflyeyesoo/docktun:latest',candidate)
 docker('exec',engine,'mkdir','-p','/installer')
 with tempfile.TemporaryDirectory() as directory:
  file=pathlib.Path(directory)/'compose.yml';file.write_text(compose,encoding='utf-8');docker('cp',file,engine+':/installer/compose.yml')
 inner('compose','-f','/installer/compose.yml','up','-d')
 base='http://'+docker('port',engine,'8080/tcp');token=''
 def request(path,body=None,expected=200):
  headers={'Content-Type':'application/json'}
  if token:headers['Authorization']='Bearer '+token
  req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers=headers)
  try:response=urllib.request.urlopen(req,timeout=30)
  except urllib.error.HTTPError as e:response=e
  with response:
   value=response.read();assert response.status==expected,(path,response.status,value[:300]);return json.loads(value) if response.headers.get_content_type()=='application/json' else value
 wait(lambda:request('/healthz'))
 token=request('/api/setup/complete',{'setup_token':inner('exec','docktun','cat','/app/data/setup.token'),'admin_username':'owner','admin_password':'fresh-installer-fixture-password'})['access_token']
 assert b'<!DOCTYPE HTML>' in request('/').upper()
 before=json.loads(inner('inspect','docktun'))[0]
 assert next(m for m in before['Mounts'] if m['Destination']=='/app/data')['Type']=='volume'
 assert inner('inspect','--format','{{.State.Running}}','docktun-zeroconf')=='true'
 inner('compose','-f','/installer/compose.yml','up','-d','--force-recreate')
 wait(lambda:request('/healthz'));request('/api/projects')
 token=request('/auth/login',{'username':'owner','password':'fresh-installer-fixture-password'})['access_token']
 after=json.loads(inner('inspect','docktun'))[0]
 assert before['Id']!=after['Id']
 assert next(m['Name'] for m in before['Mounts'] if m['Destination']=='/app/data')==next(m['Name'] for m in after['Mounts'] if m['Destination']=='/app/data')
 docker('exec',engine,'sh','-c','test ! -e /installer/.env')
 print('Single-file prebuilt installation passed: empty nested engine, no checkout/compiler/.env, dashboard plus local proxy, persistent data and login/session continuity after Compose recreation')
finally:
 subprocess.run(['docker','rm','-f','-v',engine],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
