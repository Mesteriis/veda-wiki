#!/usr/bin/env python3
"""Fail when files matched by --include globs exceed a line limit."""
import argparse
from pathlib import Path
import sys

EXCLUDED_NAMES = {'package-lock.json', 'go.sum'}
EXCLUDED_SUFFIXES = ('.lock',)
EXCLUDED_PREFIXES = ('web/dist/',)
EXCLUDED_DIRS = {'node_modules'}


def is_excluded(rel):
    parts = rel.split('/')
    name = parts[-1]
    if name in EXCLUDED_NAMES or name.endswith(EXCLUDED_SUFFIXES):
        return True
    if rel.startswith(EXCLUDED_PREFIXES):
        return True
    return any(part in EXCLUDED_DIRS for part in parts[:-1])


def count_lines(path):
    data = path.read_bytes()
    if not data:
        return 0
    return data.count(b'\n') + (0 if data.endswith(b'\n') else 1)


def find_violations(root, includes, max_lines):
    root = Path(root)
    found = {}
    for pattern in includes:
        for path in root.glob(pattern):
            if path.is_symlink() or not path.is_file():
                continue
            rel = path.relative_to(root).as_posix()
            if rel in found or is_excluded(rel):
                continue
            lines = count_lines(path)
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
