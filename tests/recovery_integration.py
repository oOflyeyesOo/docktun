"""Isolated Docker Desktop/Linux recovery acceptance; touches only unique fixtures."""
import http.cookiejar,json,os,subprocess,time,urllib.error,urllib.request,uuid,hashlib
prefix='docktun-recovery-'+uuid.uuid4().hex[:9]
image=os.environ.get('DOCKTUN_TEST_IMAGE','docktun:roadmap')
containers=[];volumes=[]
def docker(*args,input=None):return subprocess.check_output(['docker',*args],text=True,input=input,stderr=subprocess.DEVNULL).strip()
def wait(fn,seconds=240):
 deadline=time.monotonic()+seconds
 while True:
  try:return fn()
  except (AssertionError,OSError,subprocess.CalledProcessError):
   if time.monotonic()>deadline:raise
   time.sleep(1)
def volume(suffix):
 name=prefix+'-'+suffix;docker('volume','create',name);volumes.append(name);return name
def core(suffix,data):
 name=prefix+'-'+suffix;containers.append(name)
 docker('run','-d','--name',name,'-p','127.0.0.1::3001','-v',data+':/app/data','-v','/var/run/docker.sock:/var/run/docker.sock','-e','DOCKTUN_PREPARE_PROVIDERS=false','-e','ADMIN_USERNAME=owner','-e','ADMIN_PASSWORD=fixture-password-123',image)
 return name
class Client:
 def __init__(self,name):self.name=name;self.token='';self.opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
 def request(self,path,body=None,expected=200,method=None,raw=False,return_bytes=False):
  self.base='http://'+docker('port',self.name,'3001/tcp').splitlines()[0]
  headers={'Content-Type':'application/octet-stream' if raw else 'application/json'}
  if self.token:headers['Authorization']='Bearer '+self.token
  data=body if raw else (None if body is None else json.dumps(body).encode())
  req=urllib.request.Request(self.base+path,data=data,headers=headers,method=method)
  try:response=self.opener.open(req,timeout=180)
  except urllib.error.HTTPError as e:response=e
  with response:
   value=response.read();assert response.status==expected,(path,response.status,value[:500])
   return value if return_bytes else (json.loads(value) if value and response.headers.get_content_type()=='application/json' else value)
 def login(self):
  wait(lambda:self.request('/healthz'));self.token=self.request('/auth/login',{'username':'owner','password':'fixture-password-123'})['access_token']
 def operation(self,id):
  items=self.request('/api/operations');item=next(i for i in items if i['id']==id)
  assert item['phase'] in ('complete','failed'),item
  assert item['phase']=='complete',item
  return item
try:
 source=core('source',volume('source-data'));client=Client(source);client.login()
 data=volume('application-data');writer=prefix+'-writer';containers.append(writer)
 docker('run','-d','--name',writer,'-v',data+':/data','--entrypoint','sh',image,'-c','echo persistent-marker > /data/value; exec sleep 10000')
 # A live writer cannot be silently included in a filesystem backup.
 rejected=client.request('/api/recovery/backups',{'volumes':[data],'passphrase':'separate-fixture-recovery-key'},202)
 def failed():
  item=next(x for x in client.request('/api/recovery/backups') if x['id']==rejected['id']);assert item['phase']=='failed',item;return item
 wait(failed)
 databases=[]
 for engine,dbimage,user in [('postgres','postgres:17-alpine','postgres'),('mysql','mysql:8.4','root'),('mariadb','mariadb:11.4','root')]:
  name=prefix+'-'+engine;containers.append(name)
  env=['-e','POSTGRES_PASSWORD=fixture-db-password','-e','POSTGRES_DB=fixture'] if engine=='postgres' else ['-e',('MARIADB' if engine=='mariadb' else 'MYSQL')+'_ROOT_PASSWORD=fixture-db-password','-e',('MARIADB' if engine=='mariadb' else 'MYSQL')+'_DATABASE=fixture']
  docker('run','-d','--name',name,*env,dbimage)
  def sql(query,engine=engine,name=name):
   if engine=='postgres':return docker('exec',name,'psql','-At','-U','postgres','-d','fixture','-c',query)
   return docker('exec','-e','MYSQL_PWD=fixture-db-password',name,'mariadb' if engine=='mariadb' else 'mysql','-N','-uroot','fixture','-e',query)
  wait(lambda:sql('SELECT 1'))
  sql("CREATE TABLE marker(value varchar(100)); INSERT INTO marker VALUES ('recovered-login');")
  databases.append({'container':name,'engine':engine,'database':'fixture','user':user,'sql':sql})
 key='separate-fixture-recovery-key'
 backup=client.request('/api/recovery/backups',{'instance':True,'volumes':[data],'stop_containers':[writer],'databases':[{k:v for k,v in d.items() if k!='sql'} for d in databases],'passphrase':key},202)
 wait(lambda:client.operation(backup['operation_id']),600)
 assert docker('inspect','--format','{{.State.Running}}',writer)=='true'
 ticket=client.request('/api/recovery/'+backup['id']+'/download-ticket',{},200)
 assert '?' not in ticket['url'];encrypted=client.request(ticket['url']);assert encrypted[:8]==b'DTREC001'
 client.request(ticket['url'],expected=401)
 assert b'recovered-login' not in encrypted
 # New clean core, no original data volume; import standalone artifact.
 destination=core('destination',volume('destination-data'));fresh=Client(destination);fresh.login()
 imported=fresh.request('/api/recovery/import',encrypted,201,raw=True)
 archive=imported['id'];fresh.request('/api/recovery/'+archive+'/inspect',{'passphrase':'incorrect-fixture-key'},400)
 corrupt=bytearray(encrypted);corrupt[len(corrupt)//2]^=1
 tampered=fresh.request('/api/recovery/import',bytes(corrupt),201,raw=True)
 fresh.request('/api/recovery/'+tampered['id']+'/inspect',{'passphrase':key},400)
 plan=fresh.request('/api/recovery/'+archive+'/inspect',{'passphrase':key});assert not plan['ready']
 newapp=prefix+'-restored-app';newinstance=prefix+'-restored-instance';volumes.extend([newapp,newinstance])
 mapping={'passphrase':key,'volumes':{data:newapp},'instance_volume':newinstance,'databases':[]}
 for entry in plan['manifest']['entries']:
  if entry['kind']!='database':continue
  db=next(d for d in databases if d['engine']==entry['engine'])
  db['sql']('CREATE DATABASE restored')
  mapping['databases'].append({'entry':entry['file'],'container':db['container'],'database':'restored','user':db['user']})
 checked=fresh.request('/api/recovery/'+archive+'/inspect',mapping);assert checked['ready'],checked
 job=fresh.request('/api/recovery/'+archive+'/restore',mapping,202);wait(lambda:fresh.operation(job['id']),600)
 marker=docker('run','--rm','-v',newapp+':/data','--entrypoint','cat',image,'/data/value');assert marker=='persistent-marker'
 # Verify restored rows using native clients; empty-target check blocks a second restore.
 for db in databases:
  name=db['container'];engine=db['engine']
  args=['psql','-At','-U','postgres','-d','restored','-c','SELECT value FROM marker'] if engine=='postgres' else ['mariadb' if engine=='mariadb' else 'mysql','-N','-uroot','restored','-e','SELECT value FROM marker']
  assert docker('exec','-e','MYSQL_PWD=fixture-db-password',name,*args)=='recovered-login'
 assert not fresh.request('/api/recovery/'+archive+'/inspect',mapping)['ready']
 recovered=core('recovered',newinstance);restored=Client(recovered);restored.login();assert restored.request('/api/recovery/status')['review_required']
 preview=fresh.request('/api/support-bundles',{'include_operations':True});report=json.dumps(preview)
 assert 'fixture-db-password' not in report and key not in report
 artifact=fresh.request('/api/support-bundles/'+preview['id'],return_bytes=True);assert hashlib.sha256(artifact).hexdigest()==preview['sha256']
 print('Recovery passed: live-writer refusal, stop/resume, authenticated encryption/tampering, native PostgreSQL/MySQL/MariaDB exports and empty-target restores, new-volume data, clean-instance login/review mode, one-use download, support preview checksum')
finally:
 if containers:subprocess.run(['docker','rm','-f','-v',*containers],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 if volumes:subprocess.run(['docker','volume','rm',*volumes],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
