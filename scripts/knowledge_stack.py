#!/usr/bin/env python3
"""Run the knowledge graph development profile (deploy/knowledge/compose.yaml).

    python3 scripts/knowledge_stack.py up [--extractor] [--fetch-model]
    python3 scripts/knowledge_stack.py status
    python3 scripts/knowledge_stack.py down [--volumes]
    python3 scripts/knowledge_stack.py secrets

`up` creates the local secrets on first use in deploy/knowledge/.secrets/
(git-ignored; directory 0700, files 0600) and starts Neo4j Community on
127.0.0.1. `--extractor` also builds and starts the CPU extractor;
`--fetch-model` first downloads the pinned checkpoint into the model cache
volume (the only step that needs network). Secrets are never printed: the
commands print file paths and the VEDA flags that point at them.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
DEPLOY = ROOT / 'deploy' / 'knowledge'
COMPOSE = DEPLOY / 'compose.yaml'
SECRETS = DEPLOY / '.secrets'
NEO4J_USER = 'neo4j'
NEO4J_PASSWORD = SECRETS / 'neo4j_password'
NEO4J_AUTH = SECRETS / 'neo4j_auth'
EXTRACTOR_KEY = SECRETS / 'extractor.key'
NEO4J_PORT = int(os.environ.get('KNOWLEDGE_NEO4J_PORT', '7687'))
EXTRACTOR_PORT = int(os.environ.get('KNOWLEDGE_EXTRACTOR_PORT', '8790'))


def write_private(path, value):
    """Create path with mode 0600 and write value; refuse to overwrite."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as handle:
        handle.write(value + '\n')


def ensure_secrets():
    """Create missing secrets; keep existing ones (Neo4j keeps its password)."""
    SECRETS.mkdir(mode=0o700, exist_ok=True)
    os.chmod(SECRETS, 0o700)
    if not NEO4J_PASSWORD.exists():
        password = secrets.token_urlsafe(32)
        write_private(NEO4J_PASSWORD, password)
        if NEO4J_AUTH.exists():
            NEO4J_AUTH.unlink()
    if not NEO4J_AUTH.exists():
        password = NEO4J_PASSWORD.read_text().strip()
        write_private(NEO4J_AUTH, f'{NEO4J_USER}/{password}')
    if not EXTRACTOR_KEY.exists():
        write_private(EXTRACTOR_KEY, secrets.token_hex(32))
    for path in (NEO4J_PASSWORD, NEO4J_AUTH, EXTRACTOR_KEY):
        os.chmod(path, 0o600)


def compose(*args, check=True, capture=False):
    env = dict(os.environ)
    env.setdefault('KNOWLEDGE_UID', str(os.getuid()))
    env.setdefault('KNOWLEDGE_GID', str(os.getgid()))
    cmd = ['docker', 'compose', '-f', str(COMPOSE), *args]
    return subprocess.run(cmd, cwd=DEPLOY, env=env, check=check, text=True,
                          capture_output=capture)


def bolt_open(timeout=1.0):
    try:
        with socket.create_connection(('127.0.0.1', NEO4J_PORT), timeout=timeout):
            return True
    except OSError:
        return False


def extractor_ready(timeout=2.0):
    """Return (status, reason) of the extractor's /ready, or (None, error)."""
    url = f'http://127.0.0.1:{EXTRACTOR_PORT}/ready'
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            body = json.loads(resp.read(65536))
            return resp.status, body.get('status')
    except urllib.error.HTTPError as err:
        try:
            body = json.loads(err.read(65536))
        except ValueError:
            body = {}
        return err.code, f"{body.get('status')}: {body.get('reason')}"
    except (OSError, ValueError) as err:
        return None, type(err).__name__


def wait_bolt(seconds):
    """Wait until the loopback forwarder accepts Bolt connections."""
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if bolt_open():
            return True
        time.sleep(1)
    return False


def wait_healthy(service, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        out = compose('ps', '--format', 'json', service, capture=True, check=False).stdout
        for line in out.splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get('Health') == 'healthy':
                return True
        time.sleep(2)
    return False


def print_flags(extractor):
    print('VEDA flags (secret files are 0600 and git-ignored):')
    print('  --knowledge --knowledge-index=neo4j')
    print(f'  --knowledge-neo4j-uri=bolt://127.0.0.1:{NEO4J_PORT}')
    print(f'  --knowledge-neo4j-user={NEO4J_USER}')
    print(f'  --knowledge-neo4j-password-file={NEO4J_PASSWORD}')
    print(f'  --knowledge-extractor-key-file={EXTRACTOR_KEY}')
    url = f'http://127.0.0.1:{EXTRACTOR_PORT}'
    print(f'  --knowledge-extractor-url={url}' + ('' if extractor else '  (start it with up --extractor)'))


def cmd_up(args):
    ensure_secrets()
    started = time.monotonic()
    services = ['neo4j', 'neo4j-port']
    if args.extractor or args.fetch_model:
        compose('--profile', 'extractor', 'build', 'extractor')
    if args.fetch_model:
        compose('--profile', 'fetch-model', 'run', '--rm', 'fetch-model')
    if args.extractor:
        services += ['extractor', 'extractor-port']
    profile = ['--profile', 'extractor'] if args.extractor else []
    compose(*profile, 'up', '-d', *services)
    if not wait_healthy('neo4j', args.timeout) or not wait_bolt(args.timeout):
        print('neo4j did not become healthy; see: docker compose -f deploy/knowledge/compose.yaml logs neo4j',
              file=sys.stderr)
        return 1
    print(f'neo4j healthy after {time.monotonic() - started:.1f}s')
    if args.extractor:
        deadline = time.monotonic() + args.timeout
        status, detail = extractor_ready()
        while status != 200 and time.monotonic() < deadline:
            time.sleep(3)
            status, detail = extractor_ready()
        print(f'extractor /ready: {status} {detail}')
    print_flags(args.extractor)
    return 0


def cmd_status(_args):
    out = compose('--profile', 'extractor', 'ps', '--all', '--format', 'json', capture=True, check=False)
    rows = []
    for line in out.stdout.splitlines():
        try:
            rows.append(json.loads(line))
        except ValueError:
            continue
    if not rows:
        print('no knowledge stack containers')
    for row in rows:
        print(f"{row.get('Service')}: {row.get('State')} {row.get('Health') or ''}".rstrip())
    print(f"neo4j bolt 127.0.0.1:{NEO4J_PORT}: {'open' if bolt_open() else 'closed'}")
    status, detail = extractor_ready()
    print(f'extractor /ready 127.0.0.1:{EXTRACTOR_PORT}: {status} {detail}')
    print(f"secrets: {'present' if NEO4J_PASSWORD.exists() and EXTRACTOR_KEY.exists() else 'missing'} in {SECRETS}")
    return 0


def cmd_down(args):
    extra = ['--volumes'] if args.volumes else []
    compose('--profile', 'extractor', '--profile', 'fetch-model', 'down', *extra)
    return 0


def cmd_secrets(_args):
    ensure_secrets()
    print(f'secrets ready in {SECRETS} (0600, git-ignored); values are not printed')
    print_flags(False)
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    up = sub.add_parser('up', help='start Neo4j (and the extractor)')
    up.add_argument('--extractor', action='store_true', help='build and start the CPU extractor')
    up.add_argument('--fetch-model', action='store_true', help='download the pinned model into the cache volume')
    up.add_argument('--timeout', type=float, default=180, help='seconds to wait for health')
    up.set_defaults(func=cmd_up)
    sub.add_parser('status', help='show containers and endpoints').set_defaults(func=cmd_status)
    down = sub.add_parser('down', help='stop the stack')
    down.add_argument('--volumes', action='store_true', help='also delete Neo4j data and the model cache')
    down.set_defaults(func=cmd_down)
    sub.add_parser('secrets', help='create the local secrets only').set_defaults(func=cmd_secrets)
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except subprocess.CalledProcessError as err:
        print(f'docker compose failed with exit code {err.returncode}', file=sys.stderr)
        return err.returncode or 1


if __name__ == '__main__':
    sys.exit(main())
