"""Production HTTP acceptance for session revocation and atomic backup/restore."""
import io,json,os,subprocess,tarfile,time,urllib.error,urllib.request,uuid
name='docktun-auth-restore-'+uuid.uuid4().hex[:10]
def docker(*args):return subprocess.check_output(['docker',*args],text=True).strip()
try:
 docker('run','-d','--name',name,'-p','127.0.0.1::3001',os.environ.get('DOCKTUN_TEST_IMAGE','docktun:test'))
 base='http://'+docker('port',name,'3001/tcp')
 def request(path,body=None,token='',expected=200,method=None,ctype='application/json'):
  headers={'Content-Type':ctype}
  if token:headers['Authorization']='Bearer '+token
  data=None if body is None else body if isinstance(body,bytes) else json.dumps(body).encode()
  req=urllib.request.Request(base+path,data=data,headers=headers,method=method)
  try:response=urllib.request.urlopen(req,timeout=10)
  except urllib.error.HTTPError as error:response=error
  with response:
   raw=response.read();assert response.status==expected,(path,response.status,raw[:500]);return json.loads(raw) if raw and response.headers.get_content_type()=='application/json' else raw
 for _ in range(60):
  try:request('/healthz');break
  except OSError:time.sleep(1)
 else:raise AssertionError('app did not become ready')
 secret=docker('exec',name,'cat','/app/data/setup.token')
 password='fixture-password-123'
 session=request('/api/setup/complete',{'setup_token':secret,'admin_username':'owner','admin_password':password})
 access=session['access_token'];refresh=session['refresh_token']
 request('/api/projects',token=refresh,expected=401)
 rotated=request('/auth/refresh',{'refresh_token':refresh});request('/auth/refresh',{'refresh_token':refresh},expected=401)
 request('/auth/logout',{},rotated['access_token'])
 request('/api/projects',token=access,expected=401)
 request('/auth/refresh',{'refresh_token':rotated['refresh_token']},expected=401)
 session=request('/auth/login',{'username':'owner','password':password});access=session['access_token']
 request('/api/settings/password',{'current_password':password,'new_password':'x'*73},access,expected=400)
 request('/auth/login',{'username':'owner','password':password})
 def archive(name,mode=0o755):
  raw=io.BytesIO()
  with tarfile.open(fileobj=raw,mode='w:gz') as tf:
   value=b'#!/bin/sh\necho restored\n';header=tarfile.TarInfo('files/'+name);header.mode=mode;header.size=len(value);tf.addfile(header,io.BytesIO(value))
  return raw.getvalue()
 def restore(title,path,data,expected):
  boundary='audit-'+uuid.uuid4().hex
  parts=[]
  for key,value in [('name',title),('path',path)]:parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="'+key+'"\r\n\r\n'+value+'\r\n').encode())
  parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="backup"; filename="backup.tar.gz"\r\nContent-Type: application/gzip\r\n\r\n').encode()+data+b'\r\n')
  parts.append(('--'+boundary+'--\r\n').encode())
  return request('/api/projects/restore',b''.join(parts),access,expected=expected,ctype='multipart/form-data; boundary='+boundary)
 restore('invalid','/opt/projects/invalid',b'not-gzip',400)
 assert not any(p['name']=='invalid' for p in request('/api/projects',token=access))
 docker('exec',name,'sh','-c','mkdir -p /app/data/restore-existing /app/data/outside; printf keep > /app/data/outside/marker; ln -s /app/data/outside /app/data/restore-existing/link')
 restore('existing','/app/data/restore-existing',archive('link/marker'),400)
 assert docker('exec',name,'cat','/app/data/outside/marker')=='keep'
 restored=restore('restored','/opt/projects/restored',archive('run.sh'),201)
 assert docker('exec',name,'stat','-c','%a','/opt/projects/restored/run.sh')=='700'
 backup=request('/api/projects/'+str(restored['id'])+'/backup',token=access)
 copy=restore('copy','/opt/projects/copy',backup,201)
 assert docker('exec',name,'cat','/opt/projects/copy/run.sh')=='#!/bin/sh\necho restored'
 request('/api/settings/password',{'current_password':password,'new_password':'replacement-password-123'},access)
 request('/api/projects',token=access,expected=401)
 request('/auth/login',{'username':'owner','password':password},expected=401)
 request('/auth/login',{'username':'owner','password':'replacement-password-123'})
 print('Production auth/restore passed: session purposes, refresh rotation, logout revocation, rejected password safety, atomic restore, confinement, executable preservation and backup round-trip')
finally:
 subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
