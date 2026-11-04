#!/usr/bin/env python3
"""Exercise browser reading and search with a disposable synthetic VEDA store."""
import subprocess

from ui_fixture import ROOT, running_fixture


def main():
    with running_fixture() as fixture:
        try:
            subprocess.run(['node', str(ROOT / 'scripts/smoke_ui.mjs'), str(fixture.debug_port),
                            fixture.public_url, fixture.private_url, str(fixture.key_file)],
                           check=True, timeout=120)
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
            fixture.diagnose()
            raise


if __name__ == '__main__':
    main()
