#!/usr/bin/env python3
"""Check tracked working-tree files for a few high-confidence credential forms."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

PATTERNS = {
    'private_key': re.compile(rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----'),
    'github_token': re.compile(rb'\b(?:gh[pousr]_[A-Za-z0-9_]{30,}|github_pat_[A-Za-z0-9_]{30,})\b'),
    'aws_access_key': re.compile(rb'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b'),
    'credential_url': re.compile(rb'\b(?:postgres(?:ql)?|mysql|redis)://[^\s/@:]+:[^\s/@]+@', re.I),
}


def scan_paths(root, names):
    findings = {}
    for name in names:
        path = root / name
        if path.is_symlink() or not path.is_file():
            continue
        data = path.read_bytes()
        kinds = [kind for kind, pattern in PATTERNS.items() if pattern.search(data)]
        if kinds:
            findings[name] = kinds
    return findings


def main():
    root = Path(__file__).resolve().parents[1]
    tracked = subprocess.run(['git', 'ls-files', '-z'], cwd=root, check=True, stdout=subprocess.PIPE).stdout
    names = [os.fsdecode(name) for name in tracked.split(b'\0') if name]
    findings = scan_paths(root, names)
    if findings:
        print(json.dumps({'status': 'failed', 'files_and_types': findings}, ensure_ascii=True), file=sys.stderr)
        return 1
    print(json.dumps({'status': 'passed', 'tracked_files': len(names)}))
    return 0


if __name__ == '__main__':
    sys.exit(main())
