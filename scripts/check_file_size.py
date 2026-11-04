#!/usr/bin/env python3
"""Fail when files matched by --include globs exceed a line limit."""
import argparse
import os
from pathlib import Path
import re
import sys

EXCLUDED_NAMES = {'package-lock.json', 'go.sum'}
EXCLUDED_SUFFIXES = ('.lock',)
EXCLUDED_PREFIXES = ('web/dist/',)
EXCLUDED_DIRS = {'node_modules'}
# Python service environments and build output, only below services/.
SERVICE_ROOT = 'services'
SERVICE_EXCLUDED_DIRS = {'.venv', 'venv', '__pycache__', 'build', 'dist', '.pytest_cache'}
PRUNED_DIRS = {'.git'}


def is_excluded(rel):
    parts = rel.split('/')
    name = parts[-1]
    if name in EXCLUDED_NAMES or name.endswith(EXCLUDED_SUFFIXES):
        return True
    if rel.startswith(EXCLUDED_PREFIXES):
        return True
    if any(part in EXCLUDED_DIRS for part in parts[:-1]):
        return True
    return parts[0] == SERVICE_ROOT and any(part in SERVICE_EXCLUDED_DIRS for part in parts[1:-1])


def prune_dir(rel_dir, name):
    """Directories never descended into (their files are excluded anyway)."""
    if name in PRUNED_DIRS or name in EXCLUDED_DIRS:
        return True
    parts = rel_dir.split('/') if rel_dir else []
    return bool(parts) and parts[0] == SERVICE_ROOT and name in SERVICE_EXCLUDED_DIRS


def glob_regex(pattern):
    """pathlib-style glob: '**/' spans zero or more directories, '*' stays in one."""
    out, i = [], 0
    while i < len(pattern):
        if pattern.startswith('**/', i):
            out.append('(?:.*/)?')
            i += 3
        elif pattern.startswith('**', i):
            out.append('.*')
            i += 2
        elif pattern[i] == '*':
            out.append('[^/]*')
            i += 1
        elif pattern[i] == '?':
            out.append('[^/]')
            i += 1
        else:
            out.append(re.escape(pattern[i]))
            i += 1
    return re.compile(''.join(out) + r'\Z')


def walk_files(root):
    """Relative POSIX paths of regular, non-symlink files with pruning."""
    stack = ['']
    while stack:
        rel_dir = stack.pop()
        with os.scandir(os.path.join(root, rel_dir) if rel_dir else root) as entries:
            for entry in entries:
                rel = f'{rel_dir}/{entry.name}' if rel_dir else entry.name
                if entry.is_symlink():
                    continue
                if entry.is_dir():
                    if not prune_dir(rel_dir, entry.name):
                        stack.append(rel)
                elif entry.is_file():
                    yield rel


def count_lines(path):
    data = path.read_bytes()
    if not data:
        return 0
    return data.count(b'\n') + (0 if data.endswith(b'\n') else 1)


def find_violations(root, includes, max_lines):
    root = Path(root)
    patterns = [glob_regex(pattern) for pattern in includes]
    found = {}
    for rel in walk_files(root):
        if is_excluded(rel) or not any(p.match(rel) for p in patterns):
            continue
        lines = count_lines(root / rel)
        if lines > max_lines:
            found[rel] = lines
    return sorted(found.items())


def main(argv=None, root=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--max', type=int, required=True, dest='max_lines')
    parser.add_argument('--include', action='append', required=True, metavar='GLOB')
    args = parser.parse_args(argv)
    root = Path(root) if root else Path(__file__).resolve().parents[1]
    violations = find_violations(root, args.include, args.max_lines)
    for rel, lines in violations:
        print(f'{rel}: {lines} lines')
    return 1 if violations else 0


if __name__ == '__main__':
    sys.exit(main())
