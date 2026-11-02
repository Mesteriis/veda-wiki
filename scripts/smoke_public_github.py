"""Opt-in read-only acceptance against the public VEDA project repository.

Creates only a disposable local store. No GitHub credentials or writes are used.
"""
import base64, hashlib, json, os, tempfile, time
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import quote, urlencode
from urllib.request import Request, build_opener, ProxyHandler
from smoke import ROOT, free_port, start


def main():
    if os.environ.get('VEDA_TEST_PUBLIC_GITHUB') != '1':
        print('SKIPPED: set VEDA_TEST_PUBLIC_GITHUB=1 for anonymous live acceptance')
        return
    opener = build_opener(ProxyHandler({}))
    with tempfile.TemporaryDirectory(prefix='veda-public-gh-') as tmp:
        data = Path(tmp) / 'data'
        data.mkdir(mode=0o700)
        for name, char in [('owner.key', 'd'), ('reader.key', 'e')]:
            file=data/name;file.write_text(char*64);file.chmod(0o600)
        private, public = free_port(), free_port()
        proc = start(data, private, public)
        def api(route, body=None, public_surface=False):
            port=public if public_surface else private
            raw=None if body is None else json.dumps(body).encode()
            headers={'Content-Type':'application/json'}
            if not public_surface:headers['Authorization']='Bearer '+'d'*64
            req=Request(f'http://127.0.0.1:{port}'+route,data=raw,headers=headers)
            with opener.open(req,timeout=30) as response:return json.load(response)
        try:
            api('/api/v1/github/connections',{'id':'fixture','repository':'Mesteriis/veda-wiki','write':False})
            deadline=time.monotonic()+90
            while time.monotonic()<deadline:
                connections=api('/api/v1/github/connections')
                connection=next((x for x in connections if x.get('id')=='fixture'),None)
                if connection and connection.get('state')=='ready':break
                if connection and connection.get('state') in ('error','conflict','blocked'):
                    print(json.dumps({'status':'upstream_failed','state':connection['state'],'error':connection.get('last_error')}))
                    raise SystemExit(1)
                time.sleep(.5)
            else: raise RuntimeError('sync did not finish')
            sources=api('/api/v1/sources')
            src=next((x for x in sources if x.get('id')=='fixture'),None)
            pages=api('/api/v1/articles',public_surface=True)
            if not src or src.get('kind')!='program' or src.get('visibility')!='public' or connection.get('private') is not False or connection.get('write') is not False:
                raise RuntimeError('repository classification mismatch')
            if not pages or any(not x.get('path','').startswith('fixture/docs/') for x in pages):
                raise RuntimeError('public documentation scope mismatch')
            article_path=pages[0]['path'].removeprefix('fixture/')
            article=api('/api/v1/articles?'+urlencode({'source_id':'fixture','path':article_path}),public_surface=True)
            route='https://api.github.com/repos/Mesteriis/veda-wiki/contents/'+quote(article_path, safe='/')+'?'+urlencode({'ref':connection['last_pull']})
            with opener.open(Request(route,headers={'User-Agent':'VEDA-acceptance','Accept':'application/vnd.github+json'}),timeout=30) as response:
                upstream=json.load(response)
            if upstream.get('encoding')!='base64':raise RuntimeError('upstream content unavailable')
            upstream_bytes=base64.b64decode(upstream['content'],validate=False)
            rev=article['versions'][-1]
            if hashlib.sha256(upstream_bytes).hexdigest()!=rev['sha256'] or upstream_bytes.decode('utf-8')!=rev['text']:
                raise RuntimeError('import differs from upstream revision')
            candidates=api('/api/v1/articles/translation-needed')
            needed=[x for x in candidates.get('candidates',[]) if x.get('source_id')=='fixture']
            proc.terminate();proc.wait(timeout=10)
            proc=start(data,private,public)
            restored=api('/api/v1/articles?'+urlencode({'source_id':'fixture','path':article_path}))
            if restored['versions'][-1]['sha256']!=rev['sha256'] or restored['versions'][-1]['text']!=rev['text']:
                raise RuntimeError('import did not survive restart')
            print(json.dumps({'status':'passed','anonymous':True,'kind':'program','public':True,'pages':len(pages),'all_under_docs':True,'translation_candidates':len(needed),'partial':bool(src.get('import_partial')),'upstream_article_hash_match':True,'restart_preserved':True}))
        except HTTPError as exc:
            print(json.dumps({'status':'http_error','http':exc.code}))
            raise SystemExit(1)
        finally:
            proc.terminate();proc.wait(timeout=10)

if __name__=='__main__':main()
