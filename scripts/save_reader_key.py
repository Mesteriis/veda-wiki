#!/usr/bin/env python3
"""Interactively save a VEDA reader key without shell history or stdout exposure."""
import argparse
import getpass
import os
from pathlib import Path
import re
import stat
import sys
import warnings


def save_reader_key(path, token):
    path = Path(path)
    if not path.is_absolute() or not re.fullmatch(r'[0-9a-f]{64}', token, flags=re.ASCII):
        raise ValueError('Invalid key or output path')
    dir_fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        parent = os.fstat(dir_fd)
        if not stat.S_ISDIR(parent.st_mode) or parent.st_uid != os.getuid() or stat.S_IMODE(parent.st_mode) & 0o077:
            raise ValueError('The destination directory must be owner-only and owned by this user')
        fd = os.open(path.name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=dir_fd)
        with os.fdopen(fd, 'w', encoding='ascii') as stream:
            stream.write(token)
            stream.flush()
            os.fsync(stream.fileno())
        os.fsync(dir_fd)
    finally:
        os.close(dir_fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', required=True, help='Absolute path for a new private key file')
    args = parser.parse_args()
    try:
        with warnings.catch_warnings():
            warnings.simplefilter('error', getpass.GetPassWarning)
            token = getpass.getpass('VEDA reader key (input hidden): ').strip()
        save_reader_key(args.out, token)
    except (ValueError, OSError, EOFError, getpass.GetPassWarning):
        print('Key save was not confirmed; inspect destination before retrying.', file=sys.stderr)
        return 1
    print('Reader key saved to a new owner-only file.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
