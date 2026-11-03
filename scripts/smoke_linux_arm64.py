"""Opt-in ARM64 Linux archive smoke in an existing local Docker image.

Runs only synthetic data in networkless, unprivileged, disposable containers.
The image must already be present; no image is pulled and no host service starts.
"""
import argparse
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
BASELINE = r'''import json, pathlib, platform, subprocess, tempfile, time, urllib.request
if platform.machine() not in ('aarch64', 'arm64'): raise RuntimeError('wrong architecture')
with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
 root=pathlib.Path(tmp); root.chmod(0o700)
 for name,value in [('owner.key','d'*64),('reader.key','e'*64)]:
  key=root/name; key.write_text(value); key.chmod(0o600)
 p=subprocess.Popen(['/workspace/veda','--data',tmp,'--listen','127.0.0.1:18080','--public-listen','127.0.0.1:18081'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
 try:
  for _ in range(100):
   if p.poll() is not None: raise RuntimeError('server exited')
   try:
    with op.open('http://127.0.0.1:18080/health',timeout=1) as r: health=json.load(r)
    break
   except OSError: time.sleep(.05)
  else: raise RuntimeError('server not ready')
  if health.get('pages') != 0: raise RuntimeError('nonempty fixture')
  def write(route,body):
   req=urllib.request.Request('http://127.0.0.1:18080'+route,data=json.dumps(body).encode(),headers={'Content-Type':'application/json','Authorization':'Bearer '+'d'*64})
   with op.open(req,timeout=3) as r:
    if r.status != 201: raise RuntimeError('write failed')
  write('/api/v1/sources',{'id':'fixture','title':'Fixture','kind':'wiki','visibility':'private'})
  write('/api/v1/articles',{'source_id':'fixture','path':'guide.md','text':'# Synthetic ARM64','expected_revision':0})
  with op.open('http://127.0.0.1:18081/api/v1/articles',timeout=3) as r:
   if json.load(r) != []: raise RuntimeError('private article exposed')
  with op.open('http://127.0.0.1:18081/',timeout=3) as r:
   if r.status != 200 or b'/assets/' not in r.read(): raise RuntimeError('UI missing')
 finally:
  p.terminate()
  if p.wait(timeout=10) != 0: raise RuntimeError('server shutdown failed')
print('PASS: Linux ARM64 health, API write, public isolation and embedded UI')
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--image', default='python:3.14-slim')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='veda-arm64-smoke-') as tmp:
        root = Path(tmp)
        root.chmod(0o755)
        with tarfile.open(args.archive) as tar:
            matches = [m for m in tar.getmembers() if m.name.endswith('-linux-arm64/veda') and m.isfile()]
            if len(matches) != 1 or matches[0].size > 100 << 20:
                raise RuntimeError('archive does not contain exactly one bounded ARM64 binary')
            with tar.extractfile(matches[0]) as source:
                binary = root / 'veda'
                binary.write_bytes(source.read())
                binary.chmod(0o755)
        scripts = root / 'scripts'
        scripts.mkdir(mode=0o755)
        for name in ('smoke_directory_restore.py', 'smoke_translation_auth.py'):
            shutil.copyfile(ROOT / 'scripts' / name, scripts / name)
            (scripts / name).chmod(0o644)
        baseline = root / 'baseline.py'
        baseline.write_text(BASELINE)
        baseline.chmod(0o644)
        command = ['docker', 'run', '--rm', '--pull=never', '--network', 'none', '--read-only',
                   '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--user', '65534:65534',
                   '--pids-limit', '64', '--memory', '256m', '--cpus', '1', '--tmpfs', '/tmp:rw,size=128m',
                   '--mount', f'type=bind,src={root},dst=/workspace,readonly', args.image, 'python']
        for test in (['/workspace/baseline.py'], ['/workspace/scripts/smoke_directory_restore.py'],
                     ['/workspace/scripts/smoke_directory_restore.py', '--index'],
                     ['/workspace/scripts/smoke_translation_auth.py', '--protocol', 'openai']):
            subprocess.run(command + test, check=True, timeout=90)


if __name__ == '__main__':
    main()
