"""Upgrade a genuine published pre-fix image, retaining password, data and rollback.
Only uniquely named disposable fixtures are controlled.
"""
import json,os,subprocess,time,urllib.request,urllib.error,uuid
prefix='docktun-historical-'+uuid.uuid4().hex[:10]
name=prefix+'-core';volume=prefix+'-data'
previous=os.environ.get('DOCKTUN_PREVIOUS_IMAGE','ghcr.io/ooflyeyesoo/docktun:sha-43a92aa3cf90329fd2cdb9beb7c2a32dd4e84a4a')
candidate=os.environ.get('DOCKTUN_TEST_IMAGE','docktun:test')
def docker(*args):return subprocess.check_output(['docker',*args],text=True).strip()
try:
 docker('pull',previous)
 candidate_id=docker('image','inspect','--format','{{.Id}}',candidate)
 docker('volume','create',volume)
 docker('run','-d','--name',name,'-p','127.0.0.1::3001','-v',volume+':/app/data','-v','/var/run/docker.sock:/var/run/docker.sock','-e','DOCKTUN_PREPARE_PROVIDERS=false','-e','ADMIN_USERNAME=owner','-e','ADMIN_PASSWORD=bootstrap-password-123','-e','DOCKTUN_UPDATE_IMAGE='+candidate_id,previous)
 base='';token=''
 def request(path,body=None,expected=200,auth=True):
  global base
  base='http://'+docker('port',name,'3001/tcp').splitlines()[0]
  headers={'Content-Type':'application/json'}
  if auth and token:headers['Authorization']='Bearer '+token
  req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers=headers)
  try:response=urllib.request.urlopen(req,timeout=15)
  except urllib.error.HTTPError as error:response=error
  with response:
   raw=response.read();assert response.status==expected,(path,response.status,raw[:300]);return json.loads(raw) if raw and response.headers.get_content_type()=='application/json' else raw
 def wait(fn,seconds=180):
  deadline=time.monotonic()+seconds
  while True:
   try:return fn()
   except (OSError,AssertionError,subprocess.CalledProcessError):
    if time.monotonic()>deadline:raise
    time.sleep(1)
 wait(lambda:request('/healthz',auth=False))
 token=request('/auth/login',{'username':'owner','password':'bootstrap-password-123'},auth=False)['access_token']
 request('/api/settings/password',{'current_password':'bootstrap-password-123','new_password':'changed-password-123'})
 token=request('/auth/login',{'username':'owner','password':'changed-password-123'},auth=False)['access_token']
 project=request('/api/projects',{'name':'retained-project','path':'/opt/projects/retained'},expected=201)
 docker('exec',name,'sh','-c','printf retained > /app/data/historical-marker')
 old=json.loads(docker('inspect',name))[0]
 request('/api/self-update/install',{},expected=202)
 def updated():
  fresh=json.loads(docker('inspect',name))[0];assert fresh['Image']==candidate_id
  request('/healthz',auth=False);return fresh
 fresh=wait(updated)
 request('/api/projects',expected=401)
 request('/auth/login',{'username':'owner','password':'bootstrap-password-123'},expected=401,auth=False)
 token=request('/auth/login',{'username':'owner','password':'changed-password-123'},auth=False)['access_token']
 assert any(p['id']==project['id'] for p in request('/api/projects'))
 assert docker('exec',name,'cat','/app/data/historical-marker')=='retained'
 docker('restart',name);wait(lambda:request('/healthz',auth=False));request('/api/projects')
 assert next(m['Name'] for m in fresh['Mounts'] if m['Destination']=='/app/data')==volume
 print('Historical upgrade passed: published old image, changed password, one-time legacy session reauthentication, projects/data, persistent mount and new-session restart continuity')
finally:
 # Workers use the exact old target label; do not enumerate unrelated helpers.
 if 'old' in locals():
  workers=docker('ps','-aq','--filter','label=docktun.update.target='+old['Id']).split()
  if workers:subprocess.run(['docker','rm','-f',*workers],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
