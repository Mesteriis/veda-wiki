#!/usr/bin/env python3
"""Check concurrent loopback HTTP and private HTTPS with synthetic state."""
import http.client
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request

binary=os.environ.get('VEDA_TEST_BINARY',str(Path(__file__).resolve().parents[1]/'veda'))
def port():
 with socket.socket() as s:
  s.bind(('127.0.0.1',0));return s.getsockname()[1]
with tempfile.TemporaryDirectory(prefix='veda-dual-listener-') as tmp:
 root=Path(tmp);data=root/'data';data.mkdir(mode=0o700);cert=root/'cert.pem';key=root/'key.pem'
 (data/'owner.key').write_text('d'*64);(data/'owner.key').chmod(0o600)
 subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(cert),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
 rejected=subprocess.run([binary,'--data',str(data),'--listen','0.0.0.0:0','--https-listen','127.0.0.1:0','--tls-cert',str(cert),'--tls-key',str(key)],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,timeout=5)
 if rejected.returncode==0 or b'HTTP listener must be loopback' not in rejected.stderr:raise RuntimeError('non-loopback HTTP listener was not rejected')
 ports=set()
 while len(ports)<3:ports.add(port())
 plain,public,tls=ports
 p=subprocess.Popen([binary,'--data',str(data),'--listen',f'127.0.0.1:{plain}','--public-listen',f'127.0.0.1:{public}','--https-listen',f'127.0.0.1:{tls}','--tls-cert',str(cert),'--tls-key',str(key)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 try:
  context=ssl.create_default_context(cafile=str(cert));op=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=context))
  for _ in range(100):
   if p.poll() is not None:raise RuntimeError('server exited')
   try:
    with op.open(f'https://127.0.0.1:{tls}/health',timeout=2) as r:
     if r.status==200:break
   except (OSError,urllib.error.URLError):time.sleep(.05)
  else:raise RuntimeError('HTTPS unavailable')
  with op.open(f'http://127.0.0.1:{plain}/health',timeout=3) as r:
   if r.status!=200:raise RuntimeError('loopback HTTP unavailable')
  request=urllib.request.Request(f'https://127.0.0.1:{tls}/api/admin/session/login',data=json.dumps({'key':'d'*64}).encode(),headers={'Content-Type':'application/json','Origin':f'https://127.0.0.1:{tls}'})
  with op.open(request,timeout=3) as r:
   if r.status!=200 or 'Secure' not in r.headers.get('Set-Cookie',''):raise RuntimeError('HTTPS login failed')
  conn=http.client.HTTPConnection('127.0.0.1',tls,timeout=3)
  try:
   conn.request('GET','/health'); response=conn.getresponse()
   if response.status==200:raise RuntimeError('plaintext accepted on HTTPS port')
  except (OSError,http.client.HTTPException):pass
  finally:conn.close()
  print('loopback HTTP, verified HTTPS, secure login cookie and HTTPS-only port: passed')
 finally:
  p.terminate();p.wait(timeout=10)
