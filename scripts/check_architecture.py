#!/usr/bin/env python3
"""Guard Go package boundaries and collection ownership.

Prints one `path: reason` line per violation and exits 1 if there are any.

Checks:
1. internal/domain stays pure: it imports no I/O package.
2. Area packages import other areas only through ALLOWED_AREA_IMPORTS, and
   leaf packages import no area.
3. internal/knowledge imports only domain; its adapter subpackages import
   only knowledge and domain; only adapter and assembly packages import it.
4. Database collections are written only by their owner package, by
   internal/domain and by internal/store.
"""
import json
from pathlib import Path
import re
import subprocess
import sys

MODULE_INTERNAL = '/internal/'

DOMAIN_FORBIDDEN_IMPORTS = {'os', 'net', 'net/http', 'database/sql', 'os/exec'}

AREAS = {'auth', 'importer', 'transfer', 'syncer', 'agentctx'}
# Adapter and assembly packages: may import anything.
ASSEMBLY = {'httpapi', 'app', 'mcp'}
# Leaf packages: may not import areas. Subpackages (store/storetest,
# github/githubtest, app/commands) are classified with their parent.
LEAVES = {'domain', 'render', 'search', 'github', 'store', 'webui'}

# Knowledge package (path below internal/) -> internal packages it may
# import. Adapters depend on the knowledge contracts, never on each other;
# only ASSEMBLY packages import knowledge from outside.
KNOWLEDGE = 'knowledge'
KNOWLEDGE_IMPORTS = {
    'knowledge': {'domain'},
    'knowledge/jobs': {'knowledge', 'domain'},
    'knowledge/extractorclient': {'knowledge', 'domain'},
    'knowledge/memindex': {'knowledge', 'domain'},
    'knowledge/neo4jindex': {'knowledge', 'domain'},
}

# Area -> area imports that are allowed, with the reason.
#   importer  <- syncer    applies pulled batches
#   transfer  <- syncer    moves deletion transitions
#   importer  <- agentctx  reads import observations for source status
ALLOWED_AREA_IMPORTS = {
    ('syncer', 'importer'): 'applies pulled batches',
    ('syncer', 'transfer'): 'moves deletion transitions',
    ('agentctx', 'importer'): 'reads import observations for source status',
}

# Collection -> owner package. Writers outside the owner, internal/domain
# and internal/store are violations.
COLLECTION_OWNERS = {
    'Outbox': 'syncer',
    'Connections': 'syncer',
    'Accounts': 'syncer',
    'SourceMirrors': 'syncer',
    'Moves': 'transfer',
    'AccessKeys': 'auth',
    'Users': 'auth',
    'Teams': 'auth',
}
ALWAYS_ALLOWED_WRITERS = ('internal/domain/', 'internal/store/')
OWNERSHIP_EXCLUDED = ('internal/store/storetest/',)

_names = '|'.join(sorted(COLLECTION_OWNERS))
ASSIGN_RE = re.compile(r'\.(' + _names + r')(?:\[[^\n]*?\]\s*|\s+)(?:[-+*/|&^%]|<<|>>|&\^)?=(?!=)')
DELETE_RE = re.compile(r'\bdelete\(\s*[\w.\[\]()]+\.(' + _names + r')\s*,')


def package_name(import_path):
    """Return the first component below internal/, or None outside it."""
    if MODULE_INTERNAL not in import_path:
        return None
    return import_path.split(MODULE_INTERNAL, 1)[1].split('/')[0]


def internal_path(import_path):
    """Return the path below internal/, or None outside it."""
    if MODULE_INTERNAL not in import_path:
        return None
    return import_path.split(MODULE_INTERNAL, 1)[1]


def check_knowledge(pkg, where):
    own = internal_path(pkg.get('ImportPath', ''))
    if own not in KNOWLEDGE_IMPORTS:
        return [f'{where}: unknown knowledge package, add it to KNOWLEDGE_IMPORTS']
    violations = []
    for imp in pkg.get('Imports') or []:
        target = internal_path(imp)
        if target is not None and target not in KNOWLEDGE_IMPORTS[own]:
            violations.append(f'{where}: {own} must not import {target}')
    return violations


def list_packages(root):
    out = subprocess.run(['go', 'list', '-json', './...'], cwd=root, check=True,
                         capture_output=True, text=True).stdout
    decoder, packages, pos = json.JSONDecoder(), [], 0
    while pos < len(out):
        while pos < len(out) and out[pos].isspace():
            pos += 1
        if pos >= len(out):
            break
        pkg, pos = decoder.raw_decode(out, pos)
        packages.append(pkg)
    return packages


def package_dir(pkg, root):
    try:
        return Path(pkg['Dir']).resolve().relative_to(Path(root).resolve()).as_posix()
    except (KeyError, ValueError):
        return pkg.get('ImportPath', '?')


def check_imports(packages, root):
    violations = []
    for pkg in packages:
        own = package_name(pkg.get('ImportPath', ''))
        if own is None:
            continue
        where = package_dir(pkg, root)
        if own == KNOWLEDGE:
            violations.extend(check_knowledge(pkg, where))
            continue
        if own not in ASSEMBLY and any(package_name(imp) == KNOWLEDGE for imp in pkg.get('Imports') or []):
            violations.append(f'{where}: {own} must not import knowledge')
        if own not in AREAS | ASSEMBLY | LEAVES:
            violations.append(f'{where}: unknown internal package, add it to AREAS/LEAVES/ASSEMBLY')
            continue
        for imp in pkg.get('Imports') or []:
            if own == 'domain' and imp in DOMAIN_FORBIDDEN_IMPORTS:
                violations.append(f'{where}: domain must not import {imp}')
            target = package_name(imp)
            if target is None or target == own or target not in AREAS:
                continue
            if own in ASSEMBLY:
                continue
            if own in LEAVES:
                violations.append(f'{where}: leaf package {own} must not import area {target}')
            elif own in AREAS and (own, target) not in ALLOWED_AREA_IMPORTS:
                violations.append(f'{where}: area {own} must not import area {target}')
        if own in AREAS:
            for imp in pkg.get('Imports') or []:
                if package_name(imp) in ASSEMBLY:
                    violations.append(f'{where}: area {own} must not import adapter {package_name(imp)}')
    return violations


def check_ownership(root):
    root = Path(root)
    violations = []
    for path in sorted((root / 'internal').rglob('*.go')):
        rel = path.relative_to(root).as_posix()
        if path.name.endswith('_test.go') or rel.startswith(OWNERSHIP_EXCLUDED):
            continue
        if rel.startswith(ALWAYS_ALLOWED_WRITERS):
            continue
        area = rel.split('/')[1]
        for number, line in enumerate(path.read_text().splitlines(), 1):
            if line.lstrip().startswith('//'):
                continue
            for regex in (ASSIGN_RE, DELETE_RE):
                for match in regex.finditer(line):
                    collection = match.group(1)
                    if COLLECTION_OWNERS[collection] != area:
                        violations.append(
                            f'{rel}:{number}: writes {collection}, owned by {COLLECTION_OWNERS[collection]}')
    return violations


def main(root=None, packages=None):
    root = Path(root) if root else Path(__file__).resolve().parents[1]
    if packages is None:
        packages = list_packages(root)
    violations = check_imports(packages, root) + check_ownership(root)
    for line in violations:
        print(line)
    return 1 if violations else 0


if __name__ == '__main__':
    sys.exit(main())
