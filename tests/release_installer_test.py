import importlib.util,pathlib,tempfile,unittest,hashlib,json,subprocess
root=pathlib.Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('release_installer',root/'scripts/release_installer.py');module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
class Installer(unittest.TestCase):
 def test_immutable_standalone_asset(self):
  with tempfile.TemporaryDirectory() as directory:
   output=pathlib.Path(directory);module.generate(root/'compose.prebuilt.yml',output,'sha256:'+'a'*64,'v2026.11.8','b'*40)
   metadata=json.loads((output/'release.json').read_text());self.assertEqual(metadata['compose_sha256'],hashlib.sha256((output/'compose.yml').read_bytes()).hexdigest())
   config=json.loads(subprocess.check_output(['docker','compose','-f',str(output/'compose.yml'),'config','--format','json'],text=True))
   self.assertTrue(all(service['image']==metadata['image'] and 'build' not in service for service in config['services'].values()))
   self.assertEqual(config['services']['docktun']['environment']['DOCKTUN_UPDATE_IMAGE'],metadata['image'])
 def test_invalid_metadata_rejected(self):
  with tempfile.TemporaryDirectory() as directory:
   with self.assertRaises(ValueError):module.generate(root/'compose.prebuilt.yml',pathlib.Path(directory),'mutable:latest','v2026.11.8','b'*40)
if __name__=='__main__':unittest.main()
