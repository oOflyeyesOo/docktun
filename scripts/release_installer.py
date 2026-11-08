"""Generate the single-file release installer from a verified immutable digest."""
import argparse,hashlib,json,pathlib,re

def generate(source,output,digest,version,revision):
 if not re.fullmatch(r'sha256:[0-9a-f]{64}',digest):raise ValueError('invalid image digest')
 if not re.fullmatch(r'v[0-9][A-Za-z0-9._-]*',version):raise ValueError('invalid release tag')
 if not re.fullmatch(r'[0-9a-f]{40}',revision):raise ValueError('invalid source revision')
 output.mkdir(parents=True,exist_ok=True)
 text=source.read_text(encoding='utf-8');image='ghcr.io/ooflyeyesoo/docktun@'+digest
 if text.count('ghcr.io/ooflyeyesoo/docktun:latest')!=2:raise ValueError('installer image set changed; review generator')
 text='# Release '+version+'; source '+revision+'; immutable image '+digest+'\n'+text.replace('ghcr.io/ooflyeyesoo/docktun:latest',image)
 # Keep native Compose service updates on this reviewed artifact too. Channel UI
 # explains this deliberate immutable override; downloading a newer asset changes it.
 text=text.replace('      DOCKER_NETWORK:', '      DOCKTUN_UPDATE_IMAGE: '+image+'\n      DOCKER_NETWORK:',1)
 target=output/'compose.yml';target.write_text(text,encoding='utf-8',newline='\n')
 metadata={'version':version,'revision':revision,'image':image,'compose_sha256':hashlib.sha256(target.read_bytes()).hexdigest()}
 (output/'release.json').write_text(json.dumps(metadata,indent=2)+'\n',encoding='utf-8')
 (output/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in [target,output/'release.json']),encoding='utf-8')
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--digest',required=True);p.add_argument('--version',required=True);p.add_argument('--revision',required=True);p.add_argument('--output',type=pathlib.Path,default=pathlib.Path('release-assets'));args=p.parse_args()
 generate(pathlib.Path(__file__).resolve().parents[1]/'compose.prebuilt.yml',args.output,args.digest,args.version,args.revision)
