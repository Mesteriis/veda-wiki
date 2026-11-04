#!/usr/bin/env python3
"""Download the pinned GLiNER2.5 checkpoint into the local Hugging Face cache.

Run it with the extractor environment, for example:

    uv run --project services/knowledge-extractor python scripts/fetch_knowledge_model.py

Only the pinned revision is fetched. The output reports file counts and sizes,
never tokens or file contents. HF_HOME / HF_HUB_CACHE select the cache as usual.
"""
import argparse
from pathlib import Path
import sys

MODEL_ID = 'fastino/gliner2.5-multi-v1'
MODEL_REVISION = 'cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3'


def summarize(snapshot_dir):
    files = [p for p in Path(snapshot_dir).rglob('*') if p.is_file()]
    total = sum(p.stat().st_size for p in files)
    return len(files), total


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache-dir', default=None,
                        help='Hugging Face hub cache directory (default: HF_HUB_CACHE)')
    parser.add_argument('--check', action='store_true',
                        help='only verify that the pinned revision is already cached')
    args = parser.parse_args(argv)
    try:
        from huggingface_hub import snapshot_download
        from huggingface_hub.errors import LocalEntryNotFoundError
    except ImportError:
        print('huggingface_hub is missing; run this script with the extractor '
              'environment (uv run --project services/knowledge-extractor ...)', file=sys.stderr)
        return 2
    try:
        path = snapshot_download(repo_id=MODEL_ID, revision=MODEL_REVISION,
                                 cache_dir=args.cache_dir, local_files_only=args.check)
    except LocalEntryNotFoundError:
        print(f'{MODEL_ID}@{MODEL_REVISION} is not in the local cache', file=sys.stderr)
        return 1
    count, size = summarize(path)
    print(f'{MODEL_ID}@{MODEL_REVISION}: {count} files, {size / 1e6:.1f} MB in cache')
    return 0


if __name__ == '__main__':
    sys.exit(main())
