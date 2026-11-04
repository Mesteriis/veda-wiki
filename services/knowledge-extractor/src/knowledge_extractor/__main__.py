"""Entry point: ``knowledge-extractor --key-file PATH [--device cpu|mps|cuda]``."""
from __future__ import annotations

import logging
import sys

from .app import LOOPBACK_HOSTS, create_app
from .config import ConfigError, is_loopback, parse_settings
from .model import ModelHolder


def main(argv: list[str] | None = None) -> int:
    try:
        settings = parse_settings(argv)
    except ConfigError as exc:
        print(f"knowledge-extractor: {exc}", file=sys.stderr)
        return 2
    logging.basicConfig(level=settings.log_level.upper(),
                        format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    import uvicorn

    from .gliner import GlinerModel

    holder = ModelHolder(lambda: GlinerModel.load(settings.device, settings.cache_dir))
    holder.start()
    hosts = list(LOOPBACK_HOSTS) + list(settings.allowed_hosts)
    if not is_loopback(settings.host):
        logging.getLogger("knowledge_extractor").warning(
            "listening on non-loopback address %s; keep it on a closed network", settings.host)
    app = create_app(holder, settings.key, settings.limits, allowed_hosts=hosts)
    uvicorn.run(app, host=settings.host, port=settings.port, workers=1, access_log=False,
                log_level=settings.log_level, limit_concurrency=64, timeout_keep_alive=5)
    return 0


if __name__ == "__main__":
    sys.exit(main())
