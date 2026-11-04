#!/usr/bin/env python3
"""Capture reference screenshots of the reading interface from a disposable synthetic VEDA store."""
import argparse
from pathlib import Path
import subprocess

from ui_fixture import ROOT, running_fixture


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', required=True, help='directory for <screen>-<width>.png files')
    out = Path(parser.parse_args().out).resolve()
    # Overlay scrollbars fade in and out, which makes captures differ between runs.
    with running_fixture(chrome_args=['--hide-scrollbars']) as fixture:
        try:
            subprocess.run(['node', str(ROOT / 'scripts/ui_screens.mjs'), str(fixture.debug_port),
                            fixture.public_url, fixture.private_url, str(fixture.key_file), str(out)],
                           check=True, timeout=300)
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
            fixture.diagnose()
            raise


if __name__ == '__main__':
    main()
